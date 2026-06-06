package network

import (
	"net"
	"testing"

	"google.golang.org/grpc"
	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
	chatpb "parade/internal/network/pb/chatpb"
)

// fullTestCrypto extends minimalCrypto to satisfy crypto.Engine.
// Embedding minimalCrypto covers CryptoOps; the remaining methods are no-ops.
type fullTestCrypto struct {
	minimalCrypto
}

func (f fullTestCrypto) RegisterIdentity(password, filepath string) error { return nil }
func (f fullTestCrypto) LoadIdentity(password, filepath string) error     { return nil }
func (f fullTestCrypto) SetTeamKey(teamPassword string)                   {}
func (f fullTestCrypto) SetTeamKeyForTeam(teamID, teamPassword string)    {}
func (f fullTestCrypto) RemoveTeamKey(teamID string)                      {}
func (f fullTestCrypto) SetActiveTeam(teamID string) error                { return nil }
func (f fullTestCrypto) GetTeamIDs() []string                             { return nil }
func (f fullTestCrypto) TeamKeyHashFor(teamID string) string              { return "" }
func (f fullTestCrypto) EncryptLocal(plaintext []byte) ([]byte, error)    { return plaintext, nil }
func (f fullTestCrypto) DecryptLocal(ciphertext []byte) ([]byte, error)   { return ciphertext, nil }
func (f fullTestCrypto) EncryptPrivate(plaintext []byte, remotePubKeyBase64 string) ([]byte, error) {
	return plaintext, nil
}

var _ crypto.Engine = fullTestCrypto{}

// serverEngine returns a fully initialized Engine for ChatServiceImpl tests.
func serverEngine() *Engine {
	bus := eventbus.New()
	eng := &Engine{
		bus:    bus,
		logr:   testLogger{},
		crypto: fullTestCrypto{},
	}
	eng.connMgr = NewConnMgr(bus, minimalCrypto{}, eng.logr, 4327)
	eng.chatServiceImpl = &ChatServiceImpl{engine: eng}
	return eng
}

// spawnChatServer starts a real gRPC server with ChatServiceImpl on a random port,
// returning the port and a stop function.
func spawnChatServer(t *testing.T) (int, func()) {
	t.Helper()
	eng := serverEngine()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server := grpc.NewServer()
	chatpb.RegisterChatServiceServer(server, eng.chatServiceImpl)
	go func() {
		server.Serve(listener)
	}()
	stop := func() {
		server.GracefulStop()
		listener.Close()
	}
	return port, stop
}

func TestGetOrDial_CreatesPeerConn(t *testing.T) {
	port, stop := spawnChatServer(t)
	t.Cleanup(stop)

	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, port)
	t.Cleanup(func() { cm.CloseAll() })

	pc, err := cm.GetOrDial("test-pubkey", "127.0.0.1")
	if err != nil {
		t.Fatalf("GetOrDial failed: %v", err)
	}
	if pc == nil {
		t.Fatal("expected non-nil PeerConn")
	}
	if pc.Conn() == nil {
		t.Fatal("expected conn != nil")
	}

	cm.mu.RLock()
	_, exists := cm.peers["test-pubkey"]
	cm.mu.RUnlock()
	if !exists {
		t.Fatal("expected peer in peers map")
	}
}

func TestGetOrDial_DialFailure(t *testing.T) {
	// Find a free port, then close it so nothing is listening there.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	deadPort := l.Addr().(*net.TCPAddr).Port
	l.Close()

	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, deadPort)

	_, err = cm.GetOrDial("test-pubkey", "127.0.0.1")
	if err == nil {
		t.Fatal("expected error dialing a port with no server")
	}
}

func TestSendViaChannel_UnknownPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	envelope := &chatpb.Envelope{
		SenderId: "sender",
		Payload:  []byte("test"),
	}
	ok := cm.SendViaChannel("unknown-pubkey", envelope)
	if ok {
		t.Fatal("expected SendViaChannel to return false for unknown pubKey")
	}
}

func TestIncomingOnly_UpgradedByGetOrDial(t *testing.T) {
	port, stop := spawnChatServer(t)
	t.Cleanup(stop)

	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, port)
	t.Cleanup(func() { cm.CloseAll() })

	pubKey := "upgrade-test-key"

	cm.RegisterIncoming(pubKey, nil)

	cm.mu.RLock()
	pcBefore, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		t.Fatal("expected incoming-only peer to exist")
	}
	if pcBefore.conn != nil {
		t.Fatal("expected incoming-only peer to have nil conn before dial")
	}

	pcAfter, err := cm.GetOrDial(pubKey, "127.0.0.1")
	if err != nil {
		t.Fatalf("GetOrDial failed: %v", err)
	}
	if pcAfter == nil {
		t.Fatal("expected non-nil PeerConn after dial")
	}
	if pcAfter.Conn() == nil {
		t.Fatal("expected upgraded PeerConn to have non-nil conn")
	}
	if pcAfter.pubKey != pubKey {
		t.Fatalf("expected pubKey %q, got %q", pubKey, pcAfter.pubKey)
	}
}

func TestCloseAll_NoPanic(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	cm.RegisterIncoming("peer-1", nil)
	cm.RegisterIncoming("peer-2", nil)
	cm.RegisterIncoming("peer-3", nil)

	cm.mu.RLock()
	count := len(cm.peers)
	cm.mu.RUnlock()
	if count != 3 {
		t.Fatalf("expected 3 peers, got %d", count)
	}

	cm.CloseAll()

	cm.mu.RLock()
	count = len(cm.peers)
	cm.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 peers after CloseAll, got %d", count)
	}
}

func TestGetOrDial_SecondCallReuses(t *testing.T) {
	port, stop := spawnChatServer(t)
	t.Cleanup(stop)

	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, port)
	t.Cleanup(func() { cm.CloseAll() })

	pubKey := "reuse-test-key"

	pc1, err := cm.GetOrDial(pubKey, "127.0.0.1")
	if err != nil {
		t.Fatalf("first GetOrDial failed: %v", err)
	}
	if pc1.Conn() == nil {
		t.Fatal("expected first dial to have non-nil conn")
	}

	pc2, err := cm.GetOrDial(pubKey, "127.0.0.1")
	if err != nil {
		t.Fatalf("second GetOrDial failed: %v", err)
	}
	if pc2.Conn() == nil {
		t.Fatal("expected second dial to have non-nil conn")
	}

	if pc1 != pc2 {
		t.Fatal("expected second GetOrDial to return the same PeerConn reference")
	}
}
