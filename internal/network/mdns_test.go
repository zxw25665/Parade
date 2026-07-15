package network

import (
	"os"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"parade/internal/core/eventbus"
)

const mDNSTestTimeout = 15 * time.Second

// TestMDNS_Discovery verifies two Parade hosts discover each other via mDNS
// using the _parade._tcp service type. Validates the service name fix
// (was "parade._p2p._tcp.local." which zeroconf rejects as malformed)
// and the mdnsEnabled flag propagation fix.
func TestMDNS_Discovery(t *testing.T) {
	if os.Getenv("SKIP_MDNS_TEST") != "" {
		t.Skip("SKIP_MDNS_TEST set")
	}

	priv1 := make([]byte, 32)
	priv2 := make([]byte, 32)
	for i := range priv1 {
		priv1[i] = byte(i + 1)
		priv2[i] = byte(i + 100)
	}

	host1, err := NewLibp2pHost(priv1, 0, "0.0.0.0", nil, nil, nil)
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer host1.Close()

	host2, err := NewLibp2pHost(priv2, 0, "0.0.0.0", nil, nil, nil)
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	defer host2.Close()

	t.Logf("host1 ID: %s", host1.ID().ShortString())
	t.Logf("host2 ID: %s", host2.ID().ShortString())

	bus1 := eventbus.New()
	bus2 := eventbus.New()

	mdns1 := NewMDNSService(host1, bus1, nil)
	mdns2 := NewMDNSService(host2, bus2, nil)

	peer1Found := make(chan peer.ID, 1)
	peer2Found := make(chan peer.ID, 1)

	mdns1.SetOnPeerFound(func(info peer.AddrInfo) {
		t.Logf("host1 discovered: %s (addrs: %v)", info.ID.ShortString(), info.Addrs)
		if info.ID == host2.ID() {
			select {
			case peer1Found <- info.ID:
			default:
			}
		}
	})

	mdns2.SetOnPeerFound(func(info peer.AddrInfo) {
		t.Logf("host2 discovered: %s (addrs: %v)", info.ID.ShortString(), info.Addrs)
		if info.ID == host1.ID() {
			select {
			case peer2Found <- info.ID:
			default:
			}
		}
	})

	if err := mdns1.Start(); err != nil {
		t.Fatalf("start mdns1: %v", err)
	}
	defer mdns1.Stop()

	if err := mdns2.Start(); err != nil {
		t.Fatalf("start mdns2: %v", err)
	}
	defer mdns2.Stop()

	timer := time.NewTimer(mDNSTestTimeout)
	defer timer.Stop()

	host1Discovered := false
	host2Discovered := false

	for !host1Discovered || !host2Discovered {
		select {
		case <-peer1Found:
			host1Discovered = true
			t.Log("✓ host1 discovered host2")
		case <-peer2Found:
			host2Discovered = true
			t.Log("✓ host2 discovered host1")
		case <-timer.C:
			if !host1Discovered && !host2Discovered {
				t.Fatal("mDNS discovery failed: neither host discovered the other within timeout")
			}
			if !host1Discovered {
				t.Fatal("host1 did not discover host2 via mDNS")
			}
			if !host2Discovered {
				t.Fatal("host2 did not discover host1 via mDNS")
			}
		}
	}
}

func TestMDNS_ServiceName(t *testing.T) {
	if ServiceName != "_parade._tcp" {
		t.Errorf("ServiceName = %q, want %q", ServiceName, "_parade._tcp")
	}
}

func TestMDNS_StartStop(t *testing.T) {
	if os.Getenv("SKIP_MDNS_TEST") != "" {
		t.Skip("SKIP_MDNS_TEST set")
	}

	priv := make([]byte, 32)
	host, err := NewLibp2pHost(priv, 0, "0.0.0.0", nil, nil, nil)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	defer host.Close()

	bus := eventbus.New()
	mdns := NewMDNSService(host, bus, nil)

	if err := mdns.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if !mdns.IsStarted() {
		t.Error("IsStarted should return true after Start")
	}

	if err := mdns.Start(); err != nil {
		t.Fatalf("second Start should be idempotent: %v", err)
	}

	if err := mdns.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if mdns.IsStarted() {
		t.Error("IsStarted should return false after Stop")
	}

	if err := mdns.Stop(); err != nil {
		t.Fatalf("second Stop should be idempotent: %v", err)
	}
}