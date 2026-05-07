package network

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
)

type mockCrypto struct{}

func (m *mockCrypto) RegisterIdentity(_, _ string) error                { return errors.New("not implemented") }
func (m *mockCrypto) LoadIdentity(_, _ string) error                    { return errors.New("not implemented") }
func (m *mockCrypto) GetPublicKeyBase64() string                        { return "mock-pubkey-base64" }
func (m *mockCrypto) SetTeamKey(_ string)                              {}
func (m *mockCrypto) TeamKeyHash() string                                { return "mock-team-hash" }
func (m *mockCrypto) EncryptLocal(_ []byte) ([]byte, error)             { return nil, errors.New("not implemented") }
func (m *mockCrypto) DecryptLocal(_ []byte) ([]byte, error)             { return nil, errors.New("not implemented") }
func (m *mockCrypto) EncryptTeam(plaintext []byte) ([]byte, error)      { return plaintext, nil }
func (m *mockCrypto) DecryptTeam(ciphertext []byte) ([]byte, error)     { return ciphertext, nil }
func (m *mockCrypto) EncryptPrivate(_ []byte, _ string) ([]byte, error) { return nil, errors.New("not implemented") }
func (m *mockCrypto) DecryptPrivate(_ []byte, _ string) ([]byte, error) { return nil, errors.New("not implemented") }
func (m *mockCrypto) SetTeamKeyForTeam(_, _ string)                             {}
func (m *mockCrypto) RemoveTeamKey(_ string)                                    {}
func (m *mockCrypto) SetActiveTeam(_ string) error                              { return nil }
func (m *mockCrypto) GetActiveTeam() string                                     { return "" }
func (m *mockCrypto) GetTeamIDs() []string                                      { return nil }
func (m *mockCrypto) TeamKeyHashFor(_ string) string                            { return "mock-team-hash" }
func (m *mockCrypto) DecryptTeamForTeam(_ string, ciphertext []byte) ([]byte, error) { return ciphertext, nil }

var _ crypto.Engine = (*mockCrypto)(nil)

func TestPeerSessionLifecycle(t *testing.T) {
	bus := eventbus.New()
	eng := NewEngine(bus, &mockCrypto{})

	conn, err := grpc.Dial("localhost:4327",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	ps := NewPeerSession("peer-test-1", "127.0.0.1", eng, conn)
	ps.Start()

	time.Sleep(50 * time.Millisecond)

	ps.Stop()

	sessionCount := 0
	eng.sessionMu.RLock()
	sessionCount = len(eng.peerSessions)
	eng.sessionMu.RUnlock()
	if sessionCount > 0 {
		t.Fatalf("expected 0 sessions after stop, got %d", sessionCount)
	}
}

func TestPeerSessionReconnectOnFailure(t *testing.T) {
	bus := eventbus.New()
	eng := NewEngine(bus, &mockCrypto{})

	targetAddr := "127.0.0.1"
	conn, err := grpc.Dial(targetAddr+":4327",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}

	conn.Close()

	if state := conn.GetState(); state != connectivity.Shutdown {
		t.Fatalf("expected Shutdown state after close, got %v", state)
	}

	ps := NewPeerSession("peer-reconnect-1", targetAddr, eng, conn)

	reconFlagSeen := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100000; i++ {
			if ps.reconnecting.Load() {
				reconFlagSeen = true
				return
			}
			runtime.Gosched()
		}
	}()

	runtime.Gosched()
	result := ps.checkAndReconnect()

	<-done

	if !result {
		t.Fatal("checkAndReconnect returned false for Shutdown connection")
	}

	eng.connMu.RLock()
	newConn, exists := eng.clientConns["peer-reconnect-1"]
	eng.connMu.RUnlock()
	if !exists || newConn == nil {
		t.Fatal("no new connection stored after reconnect")
	}

	snapshot := eng.discovery.Snapshot()
	found := false
	for _, p := range snapshot {
		if p.PubKeyBase64 == "peer-reconnect-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("peer not found in discovery after reconnect")
	}

	if newConn.GetState() != connectivity.Shutdown {
		t.Logf("reconnected connection state: %v", newConn.GetState())
	}

	if reconFlagSeen {
		t.Log("reconnecting flag was observed during reconnect")
	}
}
