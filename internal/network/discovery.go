package network

import (
	"context"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
	"parade/internal/core/eventbus"
)

// PeerInfo 表示一个已发现节点。
type PeerInfo struct {
	PubKeyBase64 string
	IPAddress    string
}

// Discovery 维护节点发现状态，并将变更发布到事件总线。
type Discovery struct {
	bus       eventbus.EventBus
	mu        sync.RWMutex
	peers     map[string]PeerInfo
	lastSeen  map[string]time.Time
	ttl       time.Duration
	sweepTick time.Duration

	// mDNS 相关字段
	mdnsServer *mdns.Server
	mdnsDone   chan struct{}

	localPubKey string
}

func NewDiscovery(bus eventbus.EventBus) *Discovery {
	return &Discovery{
		bus:       bus,
		peers:     make(map[string]PeerInfo),
		lastSeen:  make(map[string]time.Time),
		ttl:       30 * time.Second,
		sweepTick: 5 * time.Second,
	}
}

// UpsertPeer 添加或更新节点，同时发布加入事件。
func (d *Discovery) UpsertPeer(peer PeerInfo) {
	if peer.PubKeyBase64 == "" {
		return
	}

	d.mu.Lock()
	d.peers[peer.PubKeyBase64] = peer
	d.lastSeen[peer.PubKeyBase64] = time.Now()
	d.mu.Unlock()

	d.bus.Publish(eventbus.TopicPeerJoined, eventbus.PeerEventPayload{
		PubKeyBase64: peer.PubKeyBase64,
		IPAddress:    peer.IPAddress,
	})
}

// RemovePeer 删除节点，同时发布离开事件。
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

// Snapshot 返回当前发现节点的快照。
func (d *Discovery) Snapshot() []PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]PeerInfo, 0, len(d.peers))
	for _, peer := range d.peers {
		out = append(out, peer)
	}
	return out
}

// GetPeerByPubKey 根据公钥获取对等节点信息。
func (d *Discovery) GetPeerByPubKey(pubKey string) (PeerInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peer, exists := d.peers[pubKey]
	return peer, exists
}

// SetLocalPubKey 设置本地节点的公钥，用于在 discovery 中过滤自身。
func (d *Discovery) SetLocalPubKey(pubKey string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.localPubKey = pubKey
}

// Run 启动发现循环和 mDNS 服务。
func (d *Discovery) Run(ctx context.Context) {
	d.mdnsDone = make(chan struct{})
	defer close(d.mdnsDone)

	// 启动 mDNS 查询（持续发现其他节点）
	go d.startMDNSQuery(ctx)

	// 主循环：定期清理过期的对等节点
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

// startMDNSQuery 启动持续的 mDNS 查询来发现 _parade._tcp 服务。
func (d *Discovery) startMDNSQuery(ctx context.Context) {
	// 设置 mDNS 查询参数
	entriesCh := make(chan *mdns.ServiceEntry, 32)
	go func() {
		mdns.Query(&mdns.QueryParam{
			Service:             "_parade._tcp",
			Domain:              "local.",
			Timeout:             time.Second,
			Entries:             entriesCh,
			WantUnicastResponse: false,
		})
	}()

	// 持续处理发现的服务
	queryTicker := time.NewTicker(5 * time.Second)
	defer queryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-entriesCh:
			if entry != nil {
				d.handleMDNSEntry(entry)
			}
		case <-queryTicker.C:
			// 周期性重新查询
			go func() {
				mdns.Query(&mdns.QueryParam{
					Service:             "_parade._tcp",
					Domain:              "local.",
					Timeout:             time.Second,
					Entries:             entriesCh,
					WantUnicastResponse: false,
				})
			}()
		}
	}
}

// handleMDNSEntry 处理发现的 mDNS 服务条目，并过滤自身节点。
func (d *Discovery) handleMDNSEntry(entry *mdns.ServiceEntry) {
	if entry == nil || entry.Name == "" {
		return
	}

	// 从 TXT 记录中提取公钥
	pubKey := ""
	for _, txt := range entry.InfoFields {
		if len(txt) > 7 && txt[:7] == "pubkey=" {
			pubKey = txt[7:]
			break
		}
	}

	if pubKey == "" {
		return
	}

	// 过滤自身
	d.mu.RLock()
	localKey := d.localPubKey
	d.mu.RUnlock()
	if localKey != "" && pubKey == localKey {
		return
	}

	// 获取 IP 地址
	ipAddr := ""
	if entry.AddrV4 != nil {
		ipAddr = entry.AddrV4.String()
	} else if entry.AddrV6 != nil {
		ipAddr = entry.AddrV6.String()
	}

	if ipAddr == "" {
		return
	}

	// 更新或插入对等节点
	peer := PeerInfo{
		PubKeyBase64: pubKey,
		IPAddress:    ipAddr,
	}
	d.UpsertPeer(peer)
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
