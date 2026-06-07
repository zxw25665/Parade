package network

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

// PeerInfo represents a known peer on the LAN.
type PeerInfo struct {
	PubKeyBase64 string
	IPAddress    string
}

// PeerStatus represents a peer with online/offline status.
type PeerStatus struct {
	PeerInfo
	Status         string    // "online" or "offline"
	LastHeartbeat  time.Time
	LastOnlineAt   time.Time
}

const (
	PeerStatusOnline  = "online"
	PeerStatusOffline = "offline"
)

// Discovery is an in-memory peer registry. Peers are added via UpsertPeer
// (called from StreamChat, Handshake, ConnectToPeer, and Type 8 envelope
// handlers) and removed via RemovePeer.
type Discovery struct {
	bus       EventPublisher
	mu        sync.RWMutex
	peers     map[string]PeerInfo
	statuses  map[string]PeerStatus
	lastSeen  map[string]time.Time
	peerTeams map[string]map[string]bool

	logr logger.Logger
}

func NewDiscovery(bus EventPublisher) *Discovery {
	return &Discovery{
		bus:       bus,
		peers:     make(map[string]PeerInfo),
		statuses:  make(map[string]PeerStatus),
		lastSeen:  make(map[string]time.Time),
		peerTeams: make(map[string]map[string]bool),
	}
}

func (d *Discovery) WithLogger(l logger.Logger) *Discovery {
	d.logr = l
	return d
}

func (d *Discovery) log(level logger.LogLevel, source, msg string) {
	if d.logr != nil {
		switch level {
		case logger.Trace:
			d.logr.Trace(source, msg)
		case logger.Debug:
			d.logr.Debug(source, msg)
		case logger.Info:
			d.logr.Info(source, msg)
		case logger.Warning:
			d.logr.Warn(source, msg)
		case logger.Error:
			d.logr.Error(source, msg)
		}
	}
}

// UpsertPeer adds or updates a peer. Publishes TopicPeerJoined if the peer is new.
func (d *Discovery) UpsertPeer(peer PeerInfo) {
	if peer.PubKeyBase64 == "" {
		return
	}

	d.mu.Lock()
	_, exists := d.peers[peer.PubKeyBase64]
	d.peers[peer.PubKeyBase64] = peer
	now := time.Now()
	d.lastSeen[peer.PubKeyBase64] = now
	if _, ok := d.statuses[peer.PubKeyBase64]; !ok {
		d.statuses[peer.PubKeyBase64] = PeerStatus{
			PeerInfo:      peer,
			Status:        PeerStatusOffline,
			LastHeartbeat: time.Time{},
			LastOnlineAt:  time.Time{},
		}
	}
	d.mu.Unlock()

	if !exists {
		d.bus.Publish(eventbus.TopicPeerJoined, eventbus.PeerEventPayload{
			PubKeyBase64: peer.PubKeyBase64,
			IPAddress:    peer.IPAddress,
		})
	}
}

// RemovePeer removes a peer and publishes TopicPeerLeft.
func (d *Discovery) RemovePeer(pubKey string) {
	if pubKey == "" {
		return
	}

	d.mu.Lock()
	peer, ok := d.peers[pubKey]
	if ok {
		delete(d.peers, pubKey)
		delete(d.lastSeen, pubKey)
		delete(d.peerTeams, pubKey)
	}
	d.mu.Unlock()

	if ok {
		d.bus.Publish(eventbus.TopicPeerLeft, eventbus.PeerEventPayload{
			PubKeyBase64: peer.PubKeyBase64,
			IPAddress:    peer.IPAddress,
		})
	}
}

// Snapshot returns a copy of all known peers.
func (d *Discovery) Snapshot() []PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]PeerInfo, 0, len(d.peers))
	for _, peer := range d.peers {
		out = append(out, peer)
	}
	return out
}

// GetPeerByPubKey looks up a peer by public key.
func (d *Discovery) GetPeerByPubKey(pubKey string) (PeerInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peer, exists := d.peers[pubKey]
	return peer, exists
}

// AddPeerTeamHash records that a peer belongs to a specific team hash.
func (d *Discovery) AddPeerTeamHash(pubKey, teamHash string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.peerTeams[pubKey] == nil {
		d.peerTeams[pubKey] = make(map[string]bool)
	}
	d.peerTeams[pubKey][teamHash] = true
}

// RefreshLastSeen updates the last-seen timestamp for a peer.
func (d *Discovery) RefreshLastSeen(pubKey string) {
	if pubKey == "" {
		return
	}
	d.mu.Lock()
	if _, ok := d.lastSeen[pubKey]; ok {
		d.lastSeen[pubKey] = time.Now()
	}
	d.mu.Unlock()
}

// MarkHeartbeat records a heartbeat from a peer and transitions to online if needed.
func (d *Discovery) MarkHeartbeat(pubKey string) {
	if pubKey == "" {
		return
	}
	d.mu.Lock()
	now := time.Now()
	d.lastSeen[pubKey] = now
	s, exists := d.statuses[pubKey]
	wasOffline := !exists || s.Status == PeerStatusOffline
	if wasOffline {
		d.statuses[pubKey] = PeerStatus{
			PeerInfo:     d.peers[pubKey],
			Status:       PeerStatusOnline,
			LastHeartbeat: now,
			LastOnlineAt:  now,
		}
		d.log(levelFromStatusChange(!exists, wasOffline), "discovery", fmt.Sprintf("peer online: %s", truncateKey(pubKey)))
		d.bus.Publish(eventbus.TopicPeerOnline, eventbus.PeerEventPayload{
			PubKeyBase64: pubKey,
			IPAddress:    d.peers[pubKey].IPAddress,
		})
	} else {
		s.LastHeartbeat = now
		d.statuses[pubKey] = s
	}
	d.mu.Unlock()
}

func levelFromStatusChange(isNew, wasOffline bool) logger.LogLevel {
	if isNew {
		return logger.Info
	}
	return logger.Debug
}

// CheckTimeouts transitions peers without recent heartbeats to offline.
// onlineTimeout: time without heartbeat before marking offline (e.g. 45s).
// removedTimeout: if 0, peers are never auto-removed (manual deletion only).
func (d *Discovery) CheckTimeouts(onlineTimeout, removedTimeout time.Duration) {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	for pubKey, s := range d.statuses {
		if s.Status != PeerStatusOnline {
			continue
		}
		if now.Sub(s.LastHeartbeat) > onlineTimeout {
			s.Status = PeerStatusOffline
			d.statuses[pubKey] = s
			d.log(logger.Info, "discovery", fmt.Sprintf("peer offline: %s (last heartbeat %v ago)", truncateKey(pubKey), now.Sub(s.LastHeartbeat)))
			d.bus.Publish(eventbus.TopicPeerOffline, eventbus.PeerEventPayload{
				PubKeyBase64: pubKey,
				IPAddress:    s.IPAddress,
			})
		}
	}
}

// ListWithStatus returns all peers with their online/offline status.
func (d *Discovery) ListWithStatus() []PeerStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]PeerStatus, 0, len(d.statuses))
	for _, s := range d.statuses {
		out = append(out, s)
	}
	return out
}

// PeersFile is the default path for peer list persistence.
const PeersFile = "./.parade_peers"

// SavePeers writes the current peer list to a JSON file.
func (d *Discovery) SavePeers(filename string) error {
	d.mu.RLock()
	peers := make([]PeerInfo, 0, len(d.peers))
	for _, p := range d.peers {
		peers = append(peers, p)
	}
	d.mu.RUnlock()

	data, err := json.Marshal(peers)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0600)
}

// LoadPeers reads a peer list from a JSON file and adds each peer.
func (d *Discovery) LoadPeers(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var peers []PeerInfo
	if err := json.Unmarshal(data, &peers); err != nil {
		return err
	}
	for _, p := range peers {
		d.UpsertPeer(p)
	}
	return nil
}

// RemovePeerAndSave removes a peer and persists the updated list.
func (d *Discovery) RemovePeerAndSave(pubKey string) {
	d.RemovePeer(pubKey)
	_ = d.SavePeers(PeersFile)
}

// truncateKey returns the first 16 chars of a key for log display.
func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:16]
}
