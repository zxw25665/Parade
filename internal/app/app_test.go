package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
)

type MockNetwork struct {
	LastPayload []byte
}

func (m *MockNetwork) Start(p int) error { return nil }
func (m *MockNetwork) Stop() error { return nil }
func (m *MockNetwork) BroadcastTeam(b []byte) error {
	m.LastPayload = b
	return nil
}
func (m *MockNetwork) UnicastPrivate(t string, b []byte) error { return nil }
func (m *MockNetwork) Peers() []map[string]string { return nil }
func (m *MockNetwork) StartDownload(t, v, l string) error { return nil }

type MockFile struct{}

func (m *MockFile) GetVirtualTree(p string) (interface{}, error) { return nil, nil }
func (m *MockFile) StartDownload(t, v, l string) error { return nil }
func (m *MockFile) ShareDirectory(p string) error { return nil }
func (m *MockFile) UnshareDirectory(p string) error { return nil }
func (m *MockFile) GetDirectoryChildren(p string) (interface{}, error) { return nil, nil }

type MockUI struct {
	EventName string
	Payload   interface{}
}

func (m *MockUI) Notify(name string, data interface{}) {
	m.EventName = name
	m.Payload = data
}

func setup(t *testing.T) (*App, *MockNetwork, *MockUI, func()) {
	dbP, idP := "./test.db", "./test.id"
	_ = os.Remove(dbP)
	_ = os.Remove(idP)

	eb := eventbus.New()
	cr := crypto.NewEngine()
	d, _ := db.NewSQLiteDB(dbP)
	net := &MockNetwork{}
	file := &MockFile{}
	ui := &MockUI{}

	app := NewApp(eb, cr, d, net, file, ui)
	app.Startup(context.Background())

	return app, net, ui, func() {
		d.Close()
		os.Remove(dbP)
		os.Remove(idP)
	}
}

func TestApp_FullFlow(t *testing.T) {
	a, net, ui, cleanup := setup(t)
	defer cleanup()

	_ = a.Register("123")
	_ = a.Login("123")
	_ = a.JoinTeam("team")

	txt := "Hello World"
	_ = a.SendTeamChat(txt)

	hist, _ := a.GetRecentHistory(1, 0)
	if hist[0]["content"] != txt {
		t.Errorf("DB content mismatch")
	}

	dec, _ := a.crypto.DecryptTeam(net.LastPayload)
	var netPayload eventbus.MsgReceivedPayload
	_ = json.Unmarshal(dec, &netPayload)
	if string(netPayload.Content) != txt {
		t.Errorf("Network payload mismatch")
	}

	incoming := eventbus.MsgReceivedPayload{
		HLC:      "2026-04-13T12:00:00.000Z_0001_REMOTE",
		SenderID: "remote_node",
		Content:  []byte("Incoming Message"),
	}
	a.evBus.Publish(eventbus.TopicMsgReceived, incoming)

	time.Sleep(100 * time.Millisecond)

	if ui.EventName != "ui_new_message" {
		t.Errorf("UI not notified")
	}
	uiData := ui.Payload.(map[string]interface{})
	if uiData["content"] != "Incoming Message" {
		t.Errorf("UI content mismatch")
	}
}

func TestGetRecentHistory_CorruptedMessage(t *testing.T) {
	a, _, _, cleanup := setup(t)
	defer cleanup()
	defer os.Remove(IdentityFile)

	_ = a.Register("123")
	_ = a.Login("123")
	_ = a.JoinTeam("team")

	_ = a.database.InsertMessage(context.Background(), &db.Message{
		ID:        uuid.New().String(),
		HLC:       "2026-04-25T00:00:00.000Z_0001_TEST",
		SenderID:  "test_node",
		Content:   []byte("this is not valid encrypted data"),
		CreatedAt: time.Now(),
	})

	hist, err := a.GetRecentHistory(10, 0)
	if err != nil {
		t.Fatalf("GetRecentHistory failed: %v", err)
	}
	if len(hist) == 0 {
		t.Fatal("expected at least 1 message")
	}
	if hist[0]["content"] != "[message corrupted]" {
		t.Errorf("expected corrupted placeholder, got %q", hist[0]["content"])
	}
}

func TestSendTeamChat_ReceiverID(t *testing.T) {
	a, _, _, cleanup := setup(t)
	defer cleanup()
	defer os.Remove(IdentityFile)

	_ = a.Register("123")
	_ = a.Login("123")
	_ = a.JoinTeam("team")
	_ = a.SendTeamChat("test message")

	msgs, err := a.database.GetRecentMessages(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("GetRecentMessages failed: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 message")
	}
	if msgs[0].ReceiverID != db.ReceiverIDGroupChat {
		t.Errorf("expected ReceiverID to be %q, got %q", db.ReceiverIDGroupChat, msgs[0].ReceiverID)
	}
}
