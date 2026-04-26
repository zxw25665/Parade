package network

import (
	"context"
	"sync"

	"parade/internal/core/eventbus"
)

// PeerInfo 表示一个已发现节点。
type PeerInfo struct {
	PubKeyBase64 string
	IPAddress    string
}

// Discovery 维护节点发现状态，并将变更发布到事件总线。
type Discovery struct {
	bus   eventbus.EventBus
	mu    sync.RWMutex
	peers map[string]PeerInfo
}

func NewDiscovery(bus eventbus.EventBus) *Discovery {
	return &Discovery{
		bus:   bus,
		peers: make(map[string]PeerInfo),
	}
}

// UpsertPeer 添加或更新节点，同时发布加入事件。
func (d *Discovery) UpsertPeer(peer PeerInfo) {
	if peer.PubKeyBase64 == "" {
		return
	}

	d.mu.Lock()
	d.peers[peer.PubKeyBase64] = peer
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

// Run 预留发现循环入口，后续可替换为真实 mDNS/memberlist。
func (d *Discovery) Run(ctx context.Context) {
	<-ctx.Done()
}
