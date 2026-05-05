package network

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/mdns"
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

func TestHandleMDNSEntry_FiltersNoPubKeyAndWrongService(t *testing.T) {
	bus := eventbus.New()
	d := NewDiscovery(bus)

	entry := &mdns.ServiceEntry{
		Name:       "Chromecast._http._tcp.local.",
		AddrV4:     net.ParseIP("192.168.1.100"),
		InfoFields: []string{"id=abc123"},
	}

	d.handleMDNSEntry(entry)

	if peers := d.Snapshot(); len(peers) != 0 {
		t.Fatalf("expected 0 peers after entry with no pubkey and wrong service, got %d", len(peers))
	}
}

func TestHandleMDNSEntry_AcceptsPubKeyTXTWithoutServiceName(t *testing.T) {
	bus := eventbus.New()
	d := NewDiscovery(bus)

	joinedCh := make(chan eventbus.PeerEventPayload, 1)
	bus.Subscribe(eventbus.TopicPeerJoined, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if ok {
			joinedCh <- payload
		}
	})

	entry := &mdns.ServiceEntry{
		Name:       "SomeDevice._http._tcp.local.",
		AddrV4:     net.ParseIP("192.168.1.200"),
		InfoFields: []string{"pubkey=test-pubkey-12345"},
	}

	d.handleMDNSEntry(entry)

	select {
	case got := <-joinedCh:
		if got.PubKeyBase64 != "test-pubkey-12345" {
			t.Fatalf("unexpected joined pubkey: %s", got.PubKeyBase64)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive peer joined event for pubkey TXT entry")
	}

	if peers := d.Snapshot(); len(peers) != 1 {
		t.Fatalf("expected 1 peer for pubkey TXT entry, got %d", len(peers))
	}
}

func TestHandleMDNSEntry_AcceptsCorrectService(t *testing.T) {
	bus := eventbus.New()
	d := NewDiscovery(bus)

	joinedCh := make(chan eventbus.PeerEventPayload, 1)
	bus.Subscribe(eventbus.TopicPeerJoined, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if ok {
			joinedCh <- payload
		}
	})

	entry := &mdns.ServiceEntry{
		Name:      "Parade-abc123._parade._tcp.local.",
		AddrV4:    net.ParseIP("192.168.1.10"),
		InfoFields: []string{"pubkey=test-pubkey-12345"},
	}

	d.handleMDNSEntry(entry)

	select {
	case got := <-joinedCh:
		if got.PubKeyBase64 != "test-pubkey-12345" {
			t.Fatalf("unexpected joined pubkey: %s", got.PubKeyBase64)
		}
		if got.IPAddress != "192.168.1.10" {
			t.Fatalf("unexpected IP: %s", got.IPAddress)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive peer joined event for correct service")
	}

	snapshot := d.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 peer in snapshot, got %d", len(snapshot))
	}
	if snapshot[0].PubKeyBase64 != "test-pubkey-12345" {
		t.Fatalf("unexpected peer in snapshot: %s", snapshot[0].PubKeyBase64)
	}
}

func TestRefreshLastSeen_PreventsSweep(t *testing.T) {
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
		PubKeyBase64: "peer-keepalive",
		IPAddress:    "192.168.1.20",
	}
	d.UpsertPeer(peer)

	d.mu.Lock()
	d.lastSeen[peer.PubKeyBase64] = time.Now().Add(-1 * time.Second)
	d.mu.Unlock()

	d.RefreshLastSeen(peer.PubKeyBase64)

	d.sweepExpiredPeers()

	select {
	case <-leftCh:
		t.Fatal("received unexpected peer left event after refresh")
	case <-time.After(100 * time.Millisecond):
	}

	snapshot := d.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 peer after sweep with refresh, got %d", len(snapshot))
	}
	if snapshot[0].PubKeyBase64 != peer.PubKeyBase64 {
		t.Fatalf("unexpected peer after sweep: %s", snapshot[0].PubKeyBase64)
	}
}
