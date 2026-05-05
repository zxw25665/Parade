package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"parade/internal/core/eventbus"
)

type PeerInfo struct {
	PubKeyBase64 string
	IPAddress    string
}

type Discovery struct {
	bus       eventbus.EventBus
	mu        sync.RWMutex
	peers     map[string]PeerInfo
	lastSeen  map[string]time.Time
	ttl       time.Duration
	sweepTick time.Duration

	mdnsDone    chan struct{}
	triggerQuery chan struct{}

	localPubKey string
	teamHash    string
	iface       *net.Interface

	onPeerDiscovered func(PeerInfo)
}

func NewDiscovery(bus eventbus.EventBus, browser ServiceBrowser) *Discovery {
	return &Discovery{
		bus:          bus,
		peers:        make(map[string]PeerInfo),
		lastSeen:     make(map[string]time.Time),
		ttl:          300 * time.Second,
		sweepTick:    5 * time.Second,
		triggerQuery: make(chan struct{}, 1),
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

func (d *Discovery) SetTeamHash(hash string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.teamHash = hash
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
		fmt.Printf("[mDNS] browse query failed: %v\n", err)
		return
	}

	if len(entries) == 0 {
		fmt.Printf("[mDNS] query completed: no _parade._tcp entries found\n")
	}

	for _, entry := range entries {
		d.handleServiceEntry(entry)
	}
}

func (d *Discovery) handleServiceEntry(entry *ServiceEntry) {
	if entry == nil || entry.Name == "" {
		return
	}

	fmt.Printf("[mDNS] entry: Name=%q AddrV4=%v Port=%d TXT=%v\n",
		entry.Name, entry.AddrV4, entry.Port, entry.InfoFields)

	var pubKey, entryTeamHash string
	for _, txt := range entry.InfoFields {
		if strings.HasPrefix(txt, "pubkey=") {
			pubKey = txt[7:]
		}
		if strings.HasPrefix(txt, "team=") {
			entryTeamHash = txt[5:]
		}
	}

	if pubKey == "" {
		return
	}

	d.mu.RLock()
	localKey := d.localPubKey
	localTeamHash := d.teamHash
	d.mu.RUnlock()

	if localKey != "" && pubKey == localKey {
		return
	}

	if localTeamHash != "" && entryTeamHash != localTeamHash {
		fmt.Printf("[mDNS] filtered: peer %s team mismatch (local=%s remote=%s)\n",
			truncateKey(pubKey), localTeamHash[:8], truncateKey(entryTeamHash))
		return
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

	fmt.Printf("[mDNS] discovered: %s @ %s (pubkey=%s)\n", entry.Name, ipAddr, truncateKey(pubKey))

	peer := PeerInfo{
		PubKeyBase64: pubKey,
		IPAddress:    ipAddr,
	}
	d.UpsertPeer(peer)
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