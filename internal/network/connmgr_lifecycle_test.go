package network

import (
	"testing"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	chatpb "parade/internal/network/pb/chatpb"
)

// testLogger is a minimal logger that satisfies logger.Logger.
type testLogger struct{}

func (l testLogger) Trace(source, msg string) {}
func (l testLogger) Debug(source, msg string) {}
func (l testLogger) Info(source, msg string)  {}
func (l testLogger) Warn(source, msg string)  {}
func (l testLogger) Error(source, msg string) {}

// verify testLogger satisfies the Logger interface at compile time
var _ logger.Logger = testLogger{}

// minimalEngine returns a bare Engine with enough fields for ConnMgr to operate.
func minimalEngine() *Engine {
	return &Engine{
		bus:  eventbus.New(),
		logr: testLogger{},
	}
}

// minimalCrypto is a stub CryptoOps for tests.
type minimalCrypto struct{}

func (m minimalCrypto) GetPublicKeyBase64() string                                { return "test-key" }
func (m minimalCrypto) DecryptTeam(ciphertext []byte) ([]byte, error)             { return ciphertext, nil }
func (m minimalCrypto) DecryptTeamForTeam(teamID string, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}
func (m minimalCrypto) DecryptPrivate(ciphertext []byte, remotePubKeyBase64 string) ([]byte, error) {
	return ciphertext, nil
}
func (m minimalCrypto) EncryptTeam(plaintext []byte) ([]byte, error) { return plaintext, nil }
func (m minimalCrypto) TeamKeyHash() string                          { return "test-hash" }
func (m minimalCrypto) GetActiveTeam() string                        { return "test-team" }

var _ CryptoOps = minimalCrypto{}

func TestRegisterIncoming_CreatesIncomingOnlyPeerConn(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "nonexistent-peer-key"
	cm.RegisterIncoming(pubKey, nil)

	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()

	if !exists {
		t.Fatal("expected incoming-only PeerConn to be created for unknown pubKey")
	}
	if pc.conn != nil {
		t.Fatal("expected incoming-only PeerConn to have nil conn")
	}
	if pc.pubKey != pubKey {
		t.Fatalf("expected pubKey %q, got %q", pubKey, pc.pubKey)
	}
}

func TestSendViaIncoming_NoIncomingStream(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "half-built-peer"

	// Simulate a PeerConn with no incoming stream set
	cm.mu.Lock()
	cm.peers[pubKey] = &PeerConn{
		pubKey: pubKey,
		sendCh: make(chan *chatpb.Envelope, 64),
		logr:   testLogger{},
		// conn is nil, incoming is nil — the bug condition
	}
	cm.mu.Unlock()

	envelope := &chatpb.Envelope{
		SenderId: "sender",
		Payload:  []byte("test"),
	}
	ok := cm.SendViaIncoming(pubKey, envelope)
	if ok {
		t.Fatal("expected SendViaIncoming to return false when incoming stream is nil")
	}
}

func TestSendViaIncoming_IncomingOnlyPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "incoming-only-peer"

	// Create incoming-only PeerConn via RegisterIncoming with a nil stream
	cm.RegisterIncoming(pubKey, nil)

	// Verify it exists
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		t.Fatal("expected incoming-only PeerConn to exist")
	}

	// SendViaIncoming should return false since incoming stream is nil
	envelope := &chatpb.Envelope{SenderId: "sender", Payload: []byte("test")}
	ok := cm.SendViaIncoming(pubKey, envelope)
	if ok {
		t.Fatal("expected SendViaIncoming to return false when stream is nil on incoming-only peer")
	}

	// Verify the PC still exists (not removed on failure)
	cm.mu.RLock()
	_, exists = cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		t.Fatal("expected incoming-only PeerConn to still exist after SendViaIncoming failure")
	}

	// Verify conn is nil for incoming-only peer
	if pc.conn != nil {
		t.Fatal("expected incoming-only PeerConn to have nil conn")
	}
}

func TestClose_IncomingOnlyPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "incoming-only-close-test"
	cm.RegisterIncoming(pubKey, nil)

	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		t.Fatal("expected incoming-only PeerConn to exist")
	}

	// Close should be safe — no nil pointer deref on cancel or conn
	pc.Close()
}

func TestCloseAll_IncludesIncomingOnly(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	cm.RegisterIncoming("incoming-1", nil)
	cm.RegisterIncoming("incoming-2", nil)

	if len(cm.peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cm.peers))
	}

	cm.CloseAll()

	if len(cm.peers) != 0 {
		t.Fatalf("expected 0 peers after CloseAll, got %d", len(cm.peers))
	}
}
