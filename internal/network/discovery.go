package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

type PeerInfo struct {
	PubKeyBase64 string
	IPAddress    string
}

type Discovery struct {
	bus       eventbus.EventBus
	browser   ServiceBrowser
	mu        sync.RWMutex
	peers     map[string]PeerInfo
	lastSeen  map[string]time.Time
	ttl       time.Duration
	sweepTick time.Duration

	mdnsDone    chan struct{}
	triggerQuery chan struct{}

	localPubKey string
	teamHashes map[string]bool
	peerTeams  map[string]map[string]bool
	iface      *net.Interface

	onPeerDiscovered func(PeerInfo)

	logr logger.Logger
}

func NewDiscovery(bus eventbus.EventBus, browser ServiceBrowser) *Discovery {
	return &Discovery{
		bus:          bus,
		browser:      browser,
		peers:        make(map[string]PeerInfo),
		lastSeen:     make(map[string]time.Time),
		ttl:          300 * time.Second,
		sweepTick:    5 * time.Second,
		teamHashes:   make(map[string]bool),
		peerTeams:    make(map[string]map[string]bool),
		triggerQuery: make(chan struct{}, 1),
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

		d.mu.RLock()
		cb := d.onPeerDiscovered
		d.mu.RUnlock()
		if cb != nil {
			cb(peer)
		}
	}
}

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

func (d *Discovery) Snapshot() []PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]PeerInfo, 0, len(d.peers))
	for _, peer := range d.peers {
		out = append(out, peer)
	}
	return out
}

func (d *Discovery) GetPeerByPubKey(pubKey string) (PeerInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peer, exists := d.peers[pubKey]
	return peer, exists
}

func (d *Discovery) SetLocalPubKey(pubKey string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.localPubKey = pubKey
}

func (d *Discovery) SetTeamHashes(hashes []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.teamHashes = make(map[string]bool)
	for _, h := range hashes {
		if h != "" {
			d.teamHashes[h] = true
		}
	}
}

func (d *Discovery) SetTeamHash(hash string) {
	if hash == "" {
		d.SetTeamHashes(nil)
	} else {
		d.SetTeamHashes([]string{hash})
	}
}

func (d *Discovery) GetPeersForTeam(teamHash string) []PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var out []PeerInfo
	for pubKey, teams := range d.peerTeams {
		if teams[teamHash] {
			if peer, ok := d.peers[pubKey]; ok {
				out = append(out, peer)
			}
		}
	}
	return out
}

// AddPeerTeamHash records that a peer belongs to a specific team hash,
// so that GetPeersForTeam can include manually-connected peers.
func (d *Discovery) AddPeerTeamHash(pubKey, teamHash string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.peerTeams[pubKey] == nil {
		d.peerTeams[pubKey] = make(map[string]bool)
	}
	d.peerTeams[pubKey][teamHash] = true
}

func (d *Discovery) SetIface(iface *net.Interface) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.iface = iface
}

func (d *Discovery) SetOnPeerDiscovered(fn func(PeerInfo)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onPeerDiscovered = fn
}

func (d *Discovery) TriggerQuery() {
	select {
	case d.triggerQuery <- struct{}{}:
	default:
	}
}

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

func (d *Discovery) Run(ctx context.Context) {
	d.mdnsDone = make(chan struct{})
	defer close(d.mdnsDone)

	go d.startMDNSQuery(ctx)

	ticker := time.NewTicker(d.sweepTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweepExpiredPeers()
		}
	}
}

func (d *Discovery) startMDNSQuery(ctx context.Context) {
	queryTicker := time.NewTicker(queryInterval)
	defer queryTicker.Stop()

	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}

	d.runMDNSQuery(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.triggerQuery:
			d.runMDNSQuery(ctx)
		case <-queryTicker.C:
			d.runMDNSQuery(ctx)
		}
	}
}

func (d *Discovery) runMDNSQuery(ctx context.Context) {
	d.mu.RLock()
	iface := d.iface
	d.mu.RUnlock()

	entries, err := d.browser.Browse(ctx, "_parade._tcp", "local.", iface)
	if err != nil {
		d.log(logger.Debug, "discovery", fmt.Sprintf("mDNS browse query failed: %v", err))
		return
	}

	if len(entries) == 0 {
		d.log(logger.Debug, "discovery", "mDNS query completed: no _parade._tcp entries found")
	}

	for _, entry := range entries {
		d.handleServiceEntry(entry)
	}
}

func (d *Discovery) handleServiceEntry(entry *ServiceEntry) {
	if entry == nil || entry.Name == "" {
		return
	}

	d.log(logger.Debug, "discovery", fmt.Sprintf("mDNS entry: Name=%q AddrV4=%v Port=%d TXT=%v",
		entry.Name, entry.AddrV4, entry.Port, entry.InfoFields))

	var pubKey string
	var entryTeamHashes []string
	for _, txt := range entry.InfoFields {
		if strings.HasPrefix(txt, "pubkey=") {
			pubKey = txt[7:]
		}
		if strings.HasPrefix(txt, "team=") {
			teamVal := txt[5:]
			for _, h := range strings.Split(teamVal, ",") {
				h = strings.TrimSpace(h)
				if h != "" {
					entryTeamHashes = append(entryTeamHashes, h)
				}
			}
		}
	}

	if pubKey == "" {
		return
	}

	d.mu.RLock()
	localKey := d.localPubKey
	localHashes := d.teamHashes
	d.mu.RUnlock()

	if localKey != "" && pubKey == localKey {
		return
	}

	if len(localHashes) > 0 && len(entryTeamHashes) > 0 {
		matched := false
		for _, eh := range entryTeamHashes {
			if localHashes[eh] {
				matched = true
				break
			}
		}
		if !matched {
			d.log(logger.Debug, "discovery", fmt.Sprintf("mDNS filtered: peer %s has no common team (local hashes=%v remote hashes=%v)",
				truncateKey(pubKey), localHashes, entryTeamHashes))
			return
		}
	}

	ipAddr := ""
	if entry.AddrV4 != nil {
		ipAddr = entry.AddrV4.String()
	} else if entry.AddrV6 != nil {
		ipAddr = entry.AddrV6.String()
	}

	if ipAddr == "" {
		return
	}

	d.log(logger.Info, "discovery", fmt.Sprintf("mDNS discovered: %s @ %s (pubkey=%s)", entry.Name, ipAddr, truncateKey(pubKey)))

	peer := PeerInfo{
		PubKeyBase64: pubKey,
		IPAddress:    ipAddr,
	}
	d.UpsertPeer(peer)

	d.mu.Lock()
	if d.peerTeams[pubKey] == nil {
		d.peerTeams[pubKey] = make(map[string]bool)
	}
	for _, h := range entryTeamHashes {
		d.peerTeams[pubKey][h] = true
	}
	d.mu.Unlock()
}

func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:16]
}

func (d *Discovery) sweepExpiredPeers() {
	now := time.Now()

	expired := make([]PeerInfo, 0)

	d.mu.Lock()
	for pubKey, last := range d.lastSeen {
		if now.Sub(last) <= d.ttl {
			continue
		}
		peer, ok := d.peers[pubKey]
		if !ok {
			delete(d.lastSeen, pubKey)
			continue
		}
		delete(d.peers, pubKey)
		delete(d.lastSeen, pubKey)
		delete(d.peerTeams, pubKey)
		expired = append(expired, peer)
	}
	d.mu.Unlock()

	for _, peer := range expired {
		d.bus.Publish(eventbus.TopicPeerLeft, eventbus.PeerEventPayload{
			PubKeyBase64: peer.PubKeyBase64,
			IPAddress:    peer.IPAddress,
		})
	}
}

func SelectLANInterface() (*net.Interface, net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("list interfaces: %w", err)
	}

	var bestIface *net.Interface
	var bestIP net.IP
	bestScore := -1

	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !isPrivateLAN(ip4) {
				continue
			}

			score := 0
			if iface.Flags&net.FlagMulticast != 0 {
				score += 10
			}
			if bestIface == nil || score > bestScore {
				bestIface = iface
				bestIP = ip4
				bestScore = score
			}
		}
	}

	if bestIface == nil {
		return nil, nil, fmt.Errorf("no suitable LAN interface found")
	}
	return bestIface, bestIP, nil
}

func isPrivateLAN(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}
	if len(ip) != 4 {
		return false
	}
	if ip[0] == 10 {
		return true
	}
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}