package app_test

import (
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
	StartCalled    bool
}

func (n *IntegrationMockNetwork) Start(p int) error {
	n.StartCalled = true
	return nil
}
func (n *IntegrationMockNetwork) Stop() error { return nil }
func (n *IntegrationMockNetwork) BroadcastTeam(b []byte) error {
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
func (n *IntegrationMockNetwork) SendConvSyncRequest(targetUUID, convID, sinceHLC string) error { return nil }
func (n *IntegrationMockNetwork) SendConvSyncResponse(targetUUID, convID string, messagesJSON []byte) error { return nil }
func (n *IntegrationMockNetwork) SavePeers() error { return nil }
func (n *IntegrationMockNetwork) PeersWithStatus() []network.PeerStatus { return nil }
func (n *IntegrationMockNetwork) BrowseRemoteDirectory(targetUUID, path string) ([]*pb.BrowseEntry, error) {
	return nil, nil
}
func (n *IntegrationMockNetwork) ResolveUUID(uuid string) (string, error) { return "mock_resolved_pubkey", nil }

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
	application.Startup()

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
		teamActive, _ := application.GetActiveTeam()
		bus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{
			HLC:            ts + "_9999_TEST",
			SenderID:       "BOB_ID",
			Content:        []byte(remoteMsg),
			TeamID:         teamActive,
			ConversationID: app.DeriveTeamConvID(teamActive),
		})

		time.Sleep(100 * time.Millisecond)

		if mockUI.LastEvent != "ui_new_message" {
			t.Error("UI notification missing")
		}

		hist, _ := application.GetConversationMessages(app.DeriveTeamConvID(teamActive), 1, 0)
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
		newApp.Startup()

		if err := newApp.Login(pwd); err != nil {
			t.Fatalf("Login after restart failed: %v", err)
		}

		tActive, _ := newApp.GetActiveTeam()
		convID := app.DeriveTeamConvID(tActive)
		history, _ := newApp.GetConversationMessages(convID, 10, 0)
		if len(history) != 2 {
			t.Errorf("Data lost or duplicate? Expected 2 msgs, got %d", len(history))
		}

		t.Logf("Successfully restored %d messages from encrypted SQLite.", len(history))
	})
}

// TestSystem_JoinTeamReusesUUID verifies that re-joining with the same team secret
// reuses the existing team UUID instead of generating a new one (Issue 1 fix).
func TestSystem_JoinTeamReusesUUID(t *testing.T) {
	dbFile := "./integration_test.db"
	idFile := "./.parade_identity"
	teamsFile := "./.parade_teams"
	_ = os.Remove(dbFile)
	_ = os.Remove(idFile)
	_ = os.Remove(teamsFile)
	defer func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(idFile)
		_ = os.Remove(teamsFile)
	}()

	pwd := "safe_password_123"
	secret := "team_reuse_test_secret"

	// Round 1: create identity, join team, record UUID
	bus1 := eventbus.New()
	cry1 := crypto.NewEngine()
	db1, err := db.NewSQLiteDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	mockNet := &IntegrationMockNetwork{}
	mockFile := &IntegrationMockFile{}
	mockUI := &IntegrationMockUI{}

	app1 := app.NewApp(bus1, cry1, db1, mockNet, mockFile, mockUI, nil)
	app1.Startup()

	if err := app1.Register(pwd); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := app1.Login(pwd); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	teamID1, err := app1.JoinTeamWithName("Test Team", secret)
	if err != nil {
		t.Fatalf("First JoinTeamWithName failed: %v", err)
	}
	t.Logf("First join: teamID=%s", teamID1)
	db1.Close()

	// Round 2: new app on same DB, login, join with same secret — should get same UUID
	bus2 := eventbus.New()
	cry2 := crypto.NewEngine()
	db2, err := db.NewSQLiteDB(dbFile)
	if err != nil {
		t.Fatalf("Failed to reopen DB: %v", err)
	}
	mockNet2 := &IntegrationMockNetwork{}
	mockUI2 := &IntegrationMockUI{}

	app2 := app.NewApp(bus2, cry2, db2, mockNet2, mockFile, mockUI2, nil)
	app2.Startup()

	if err := app2.Login(pwd); err != nil {
		t.Fatalf("Login after restart failed: %v", err)
	}
	teamID2, err := app2.JoinTeamWithName("", secret)
	if err != nil {
		t.Fatalf("Second JoinTeamWithName failed: %v", err)
	}
	t.Logf("Second join: teamID=%s", teamID2)
	db2.Close()

	if teamID1 != teamID2 {
		t.Errorf("UUID mismatch after re-join: first=%s second=%s", teamID1, teamID2)
	}
}

// TestSystem_LoginAutoStartsNetwork verifies that Login starts the network
// engine when restored team keys exist (Issue 2 fix).
func TestSystem_LoginAutoStartsNetwork(t *testing.T) {
	dbFile := "./integration_test.db"
	idFile := "./.parade_identity"
	teamsFile := "./.parade_teams"
	_ = os.Remove(dbFile)
	_ = os.Remove(idFile)
	_ = os.Remove(teamsFile)
	defer func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(idFile)
		_ = os.Remove(teamsFile)
	}()

	pwd := "safe_password_123"

	// Create identity, join team so .parade_teams exists
	bus1 := eventbus.New()
	cry1 := crypto.NewEngine()
	db1, _ := db.NewSQLiteDB(dbFile)
	mockNet := &IntegrationMockNetwork{}
	mockFile := &IntegrationMockFile{}
	mockUI := &IntegrationMockUI{}

	app1 := app.NewApp(bus1, cry1, db1, mockNet, mockFile, mockUI, nil)
	app1.Startup()
	app1.Register(pwd)
	app1.Login(pwd)
	app1.JoinTeamWithName("Auto Team", "auto_secret")
	db1.Close()

	// New app: Login should trigger Start
	bus2 := eventbus.New()
	cry2 := crypto.NewEngine()
	db2, _ := db.NewSQLiteDB(dbFile)
	mockNet2 := &IntegrationMockNetwork{}
	mockUI2 := &IntegrationMockUI{}

	app2 := app.NewApp(bus2, cry2, db2, mockNet2, mockFile, mockUI2, nil)
	app2.Startup()

	if err := app2.Login(pwd); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	db2.Close()

	if !mockNet2.StartCalled {
		t.Error("Login did not auto-start network when teams exist")
	}
}

// TestSystem_NoDuplicateOnSelfSender verifies that messages from self
// published on the event bus are not persisted a second time (Issue 4 fix).
func TestSystem_NoDuplicateOnSelfSender(t *testing.T) {
	dbFile := "./integration_test.db"
	idFile := "./.parade_identity"
	teamsFile := "./.parade_teams"
	_ = os.Remove(dbFile)
	_ = os.Remove(idFile)
	_ = os.Remove(teamsFile)
	defer func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(idFile)
		_ = os.Remove(teamsFile)
	}()

	pwd := "safe_password_123"
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
	application.Startup()

	if err := application.Register(pwd); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := application.Login(pwd); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	_ = application.JoinTeam("dedup_test")

	// Send one message via SendTeamChat (persisted by sendMessageWith)
	if err := application.SendTeamChat("First message"); err != nil {
		t.Fatalf("SendTeamChat failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	tActive, _ := application.GetActiveTeam()
	histBefore, _ := application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
	countBefore := len(histBefore)
	t.Logf("Messages before self-publish: %d", countBefore)

	// Simulate self-publish: use sender ID from history to match the self-guard check
	if countBefore > 0 {
		senderID, ok := histBefore[0]["sender"].(string)
		if !ok {
			t.Fatal("Could not get sender from history")
		}

		// Publish a message where SenderID matches our pubkey (simulating self-publish)
		ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		bus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{
			HLC:      ts + "_0001_SELF",
			SenderID: senderID,
			Content:  []byte("This should be deduped"),
			TeamID:   tActive,
		})

		time.Sleep(100 * time.Millisecond)

		histAfter, _ := application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
		countAfter := len(histAfter)
		t.Logf("Messages after self-publish: %d", countAfter)

		if countAfter != countBefore {
			t.Errorf("Self-published message was not deduplicated: before=%d after=%d", countBefore, countAfter)
		}
	}
}
