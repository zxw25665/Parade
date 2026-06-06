package network

import (
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

// Discovery is an in-memory peer registry. Peers are added via UpsertPeer
// (called from StreamChat, Handshake, ConnectToPeer, and Type 8 envelope
// handlers) and removed via RemovePeer.
type Discovery struct {
	bus       EventPublisher
	mu        sync.RWMutex
	peers     map[string]PeerInfo
	lastSeen  map[string]time.Time
	peerTeams map[string]map[string]bool

	logr logger.Logger
}

func NewDiscovery(bus EventPublisher) *Discovery {
	return &Discovery{
		bus:       bus,
		peers:     make(map[string]PeerInfo),
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
	d.lastSeen[peer.PubKeyBase64] = time.Now()
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

// truncateKey returns the first 16 chars of a key for log display.
func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:16]
}
