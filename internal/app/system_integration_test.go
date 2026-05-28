package app_test

import (
	"context"
	"os"
	"testing"
	"time"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/network"
	pb "parade/internal/network/pb"
)

type IntegrationMockNetwork struct {
	BroadcastCount int
	LastPayload    []byte
}

func (n *IntegrationMockNetwork) Start(p int) error { return nil }
func (n *IntegrationMockNetwork) Stop() error { return nil }
func (n *IntegrationMockNetwork) BroadcastTeam(b []byte) error {
	n.BroadcastCount++
	n.LastPayload = b
	return nil
}
func (n *IntegrationMockNetwork) BroadcastChannel(channelID string, b []byte) error {
	n.BroadcastCount++
	n.LastPayload = b
	return nil
}
func (n *IntegrationMockNetwork) UnicastPrivate(t string, b []byte) error { return nil }
func (n *IntegrationMockNetwork) Peers() []map[string]string { return nil }
func (n *IntegrationMockNetwork) StartDownload(t, v, l string) error { return nil }
func (n *IntegrationMockNetwork) ConnectToPeer(ip string) (*network.PeerConnectResult, error) {
	return &network.PeerConnectResult{
		IP:     ip,
		PubKey: "mock_key",
		Phase1: network.PhaseResult{Success: true, Label: "正常"},
	}, nil
}
func (n *IntegrationMockNetwork) OnForeground() {}
func (n *IntegrationMockNetwork) BrowseRemoteDirectory(targetPubKey, path string) ([]*pb.BrowseEntry, error) {
	return nil, nil
}

type IntegrationMockFile struct{}

func (f *IntegrationMockFile) GetVirtualTree(p string) (interface{}, error) { return nil, nil }
func (f *IntegrationMockFile) ShareDirectory(p string) error { return nil }
func (f *IntegrationMockFile) UnshareDirectory(p string) error { return nil }
func (f *IntegrationMockFile) GetDirectoryChildren(p string) (interface{}, error) { return nil, nil }
func (f *IntegrationMockFile) GetSharedRoots() []string { return nil }

type IntegrationMockUI struct {
	LastEvent string
	LastData  interface{}
}

func (u *IntegrationMockUI) Notify(name string, data interface{}) {
	u.LastEvent = name
	u.LastData = data
}

func TestSystem_CompleteUserFlow(t *testing.T) {
	dbFile := "./integration_test.db"
	idFile := "./.parade_identity"
	_ = os.Remove(dbFile)
	_ = os.Remove(idFile)
	defer func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(idFile)
	}()

	bus := eventbus.New()
	cry := crypto.NewEngine()
	database, err := db.NewSQLiteDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}

	mockNet := &IntegrationMockNetwork{}
	mockFile := &IntegrationMockFile{}
	mockUI := &IntegrationMockUI{}

	application := app.NewApp(bus, cry, database, mockNet, mockFile, mockUI, nil)
	application.Startup(context.Background())

	pwd := "safe_password_123"

	t.Run("Auth", func(t *testing.T) {
		if err := application.Register(pwd); err != nil {
			t.Fatalf("Register failed: %v", err)
		}
		if err := application.Login(pwd); err != nil {
			t.Fatalf("Login failed: %v", err)
		}
	})

	t.Run("Send", func(t *testing.T) {
		_ = application.JoinTeam("team_123")
		msgText := "Hello Integrated World"
		if err := application.SendTeamChat(msgText); err != nil {
			t.Fatalf("Send failed: %v", err)
		}
		if mockNet.BroadcastCount != 1 {
			t.Errorf("Expected 1 broadcast, got %d", mockNet.BroadcastCount)
		}
	})

	t.Run("Receive", func(t *testing.T) {
		remoteMsg := "Reply from Bob"
		ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		bus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{
			HLC:      ts + "_9999_TEST",
			SenderID: "BOB_ID",
			Content:  []byte(remoteMsg),
			TeamID:   application.GetActiveTeam(),
		})

		time.Sleep(100 * time.Millisecond)

		if mockUI.LastEvent != "ui_new_message" {
			t.Error("UI notification missing")
		}

		hist, _ := application.GetRecentHistory(1, 0)
		if len(hist) == 0 || hist[0]["content"] != remoteMsg {
			t.Errorf("DB save failed or content mismatch")
		}
	})

	t.Run("Restart", func(t *testing.T) {
		database.Close()

		newBus := eventbus.New()
		newCry := crypto.NewEngine()
		newDB, _ := db.NewSQLiteDB(dbFile)
		newUI := &IntegrationMockUI{}

		newApp := app.NewApp(newBus, newCry, newDB, mockNet, mockFile, newUI, nil)
		newApp.Startup(context.Background())

		if err := newApp.Login(pwd); err != nil {
			t.Fatalf("Login after restart failed: %v", err)
		}

		history, _ := newApp.GetRecentHistory(10, 0)
		if len(history) < 2 {
			t.Errorf("Data lost? Expected 2+ msgs, got %d", len(history))
		}

		t.Logf("Successfully restored %d messages from encrypted SQLite.", len(history))
	})
}
