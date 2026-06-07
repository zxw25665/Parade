package network

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"parade/internal/core/eventbus"
	chatpb "parade/internal/network/pb/chatpb"
)

// mockIncomingStream implements chatpb.ChatService_StreamChatServer
// and captures envelopes sent via the bidi stream.
type mockIncomingStream struct {
	ctx    context.Context
	sendCh chan *chatpb.Envelope
}

func (m *mockIncomingStream) SetHeader(md metadata.MD) error  { return nil }
func (m *mockIncomingStream) SendHeader(md metadata.MD) error { return nil }
func (m *mockIncomingStream) SetTrailer(md metadata.MD)       {}
func (m *mockIncomingStream) Context() context.Context         { return m.ctx }
func (m *mockIncomingStream) SendMsg(msg any) error { return nil }
func (m *mockIncomingStream) RecvMsg(msg any) error { return io.EOF }
func (m *mockIncomingStream) Send(env *chatpb.Envelope) error {
	select {
	case m.sendCh <- env:
		return nil
	default:
		return nil
	}
}
func (m *mockIncomingStream) Recv() (*chatpb.Envelope, error) {
	return nil, io.EOF
}

// TestBroadcastTeam_NoPeers verifies that when no peers exist,
// BroadcastTeam decrypts the payload and self-publishes to the event bus.
func TestBroadcastTeam_NoPeers(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	received := make(chan eventbus.MsgReceivedPayload, 1)
	eng.bus.Subscribe(eventbus.TopicMsgReceived, func(ctx context.Context, ev eventbus.Event) {
		payload := ev.Payload.(eventbus.MsgReceivedPayload)
		received <- payload
	})

	msg := eventbus.MsgReceivedPayload{Content: []byte("hello"), Type: 0, SenderID: "test-sender"}
	jsonPayload, _ := json.Marshal(msg)

	err := cm.BroadcastTeam("test-pubkey", "test-team", jsonPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case receivedMsg := <-received:
		if string(receivedMsg.Content) != "hello" {
			t.Fatalf("expected content 'hello', got '%s'", string(receivedMsg.Content))
		}
		if receivedMsg.Type != 0 {
			t.Fatalf("expected type 0, got %d", receivedMsg.Type)
		}
		if receivedMsg.SenderID != "test-sender" {
			t.Fatalf("expected SenderID 'test-sender', got '%s'", receivedMsg.SenderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for self-published message")
	}
}

// TestSendViaChannel_Success verifies that an envelope can be queued
// on a peer's sendCh via SendViaChannel.
func TestSendViaChannel_Success(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "test-peer"
	sendCh := make(chan *chatpb.Envelope, 1)
	cm.mu.Lock()
	cm.peers[pubKey] = &PeerConn{pubKey: pubKey, sendCh: sendCh, logr: testLogger{}}
	cm.mu.Unlock()

	envelope := &chatpb.Envelope{
		Type:     0,
		SenderId: "sender",
		Payload:  []byte("test-payload"),
	}

	ok := cm.SendViaChannel(pubKey, envelope)
	if !ok {
		t.Fatal("expected SendViaChannel to succeed")
	}

	select {
	case received := <-sendCh:
		if string(received.Payload) != "test-payload" {
			t.Fatalf("expected payload 'test-payload', got '%s'", string(received.Payload))
		}
		if received.Type != 0 {
			t.Fatalf("expected type 0, got %d", received.Type)
		}
		if received.SenderId != "sender" {
			t.Fatalf("expected SenderId 'sender', got '%s'", received.SenderId)
		}
	default:
		t.Fatal("expected envelope to be queued on sendCh")
	}
}

// TestSendViaChannel_NoPeer verifies that SendViaChannel returns false
// for an unknown public key.
func TestSendViaChannel_NoPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	envelope := &chatpb.Envelope{Type: 0, SenderId: "sender"}
	ok := cm.SendViaChannel("unknown-peer", envelope)
	if ok {
		t.Fatal("expected SendViaChannel to return false for unknown peer")
	}
}

// TestBroadcastTeam_WithPeer verifies that when peers exist,
// BroadcastTeam sends the encrypted payload to connected peers
// through the persistent stream's sendCh.
func TestBroadcastTeam_WithPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	pubKey := "peer-key"

	// Add peer to discovery so BroadcastTeam sees it.
	cm.discovery.UpsertPeer(PeerInfo{PubKeyBase64: pubKey, IPAddress: "127.0.0.1"})

	// Create a PeerConn with a mock incoming stream so SendViaIncoming
	// succeeds, avoiding the need for a real gRPC dial.
	sendCh := make(chan *chatpb.Envelope, 1)
	stream := &mockIncomingStream{
		ctx:    context.Background(),
		sendCh: sendCh,
	}

	cm.mu.Lock()
	cm.peers[pubKey] = &PeerConn{
		pubKey:   pubKey,
		incoming: stream,
		sendCh:   make(chan *chatpb.Envelope, 64),
		logr:     testLogger{},
	}
	cm.mu.Unlock()

	msg := eventbus.MsgReceivedPayload{Content: []byte("hello"), Type: 0}
	jsonPayload, _ := json.Marshal(msg)

	cm.BroadcastTeam("test-pubkey", "test-team", jsonPayload)

	select {
	case env := <-sendCh:
		if env.Type != 0 {
			t.Fatalf("expected type 0, got %d", env.Type)
		}
		if string(env.Payload) == "" || string(env.Payload) == "null" {
			t.Fatalf("expected non-empty payload, got '%s'", string(env.Payload))
		}
		if env.SenderId != "test-pubkey" {
			t.Fatalf("expected SenderId 'test-pubkey', got '%s'", env.SenderId)
		}
		if env.ReceiverId != "" {
			t.Fatalf("expected empty ReceiverId for team broadcast, got '%s'", env.ReceiverId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for broadcast to reach mock incoming stream")
	}
}

// TestUnicastPrivate_EmptyTarget verifies that UnicastPrivate returns
// an error when targetPubKey is empty.
func TestUnicastPrivate_EmptyTarget(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	err := cm.UnicastPrivate("my-pubkey", "", "team-id", []byte("test"))
	if err == nil {
		t.Fatal("expected error for empty target pubkey")
	}
}

// TestUnicastPrivate_UnknownPeer verifies that UnicastPrivate does not
// panic when the target peer is not found, and returns nil immediately.
func TestUnicastPrivate_UnknownPeer(t *testing.T) {
	eng := minimalEngine()
	cm := NewConnMgr(eng.bus, minimalCrypto{}, eng.logr, 4327)

	err := cm.UnicastPrivate("my-pubkey", "unknown-peer", "team-id", []byte("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Give the background goroutine time to execute and log.
	time.Sleep(100 * time.Millisecond)
}
