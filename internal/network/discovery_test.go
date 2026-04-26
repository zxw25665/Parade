package network

import (
	"context"
	"testing"
	"time"

	"parade/internal/core/eventbus"
)

func TestDiscoveryUpsertRemoveAndSnapshot(t *testing.T) {
	bus := eventbus.New()
	d := NewDiscovery(bus)

	joinedCh := make(chan eventbus.PeerEventPayload, 1)
	leftCh := make(chan eventbus.PeerEventPayload, 1)

	bus.Subscribe(eventbus.TopicPeerJoined, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if ok {
			joinedCh <- payload
		}
	})
	bus.Subscribe(eventbus.TopicPeerLeft, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if ok {
			leftCh <- payload
		}
	})

	peer := PeerInfo{
		PubKeyBase64: "peer-a",
		IPAddress:    "192.168.1.9",
	}
	d.UpsertPeer(peer)

	select {
	case got := <-joinedCh:
		if got.PubKeyBase64 != peer.PubKeyBase64 {
			t.Fatalf("unexpected joined peer: %s", got.PubKeyBase64)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive peer joined event")
	}

	snapshot := d.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 peer in snapshot, got %d", len(snapshot))
	}

	d.RemovePeer(peer.PubKeyBase64)

	select {
	case got := <-leftCh:
		if got.PubKeyBase64 != peer.PubKeyBase64 {
			t.Fatalf("unexpected left peer: %s", got.PubKeyBase64)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive peer left event")
	}

	snapshot = d.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected 0 peers after remove, got %d", len(snapshot))
	}
}

func TestDiscoverySweepExpiredPeers(t *testing.T) {
	bus := eventbus.New()
	d := NewDiscovery(bus)
	d.ttl = 10 * time.Millisecond

	leftCh := make(chan eventbus.PeerEventPayload, 1)
	bus.Subscribe(eventbus.TopicPeerLeft, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if ok {
			leftCh <- payload
		}
	})

	peer := PeerInfo{
		PubKeyBase64: "peer-expired",
		IPAddress:    "192.168.1.10",
	}
	d.UpsertPeer(peer)

	d.mu.Lock()
	d.lastSeen[peer.PubKeyBase64] = time.Now().Add(-1 * time.Second)
	d.mu.Unlock()

	d.sweepExpiredPeers()

	select {
	case got := <-leftCh:
		if got.PubKeyBase64 != peer.PubKeyBase64 {
			t.Fatalf("unexpected expired peer left event: %s", got.PubKeyBase64)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive peer left event for expired peer")
	}

	if peers := d.Snapshot(); len(peers) != 0 {
		t.Fatalf("expected 0 peers after sweep, got %d", len(peers))
	}
}
