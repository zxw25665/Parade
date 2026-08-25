package app_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	syncpkg "parade/internal/core/sync"
	"parade/internal/network"
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
func (n *IntegrationMockNetwork) BrowseRemoteDirectory(targetUUID, path string) ([]*network.BrowseEntry, error) {
	return nil, nil
}
func (n *IntegrationMockNetwork) SendMerkleRootRequest(targetUUID, convID string) ([]byte, error) {
	return make([]byte, 32), nil
}
func (n *IntegrationMockNetwork) SendBucketCompareRequest(targetUUID, convID string, level int, paths []string) ([]syncpkg.BucketInfo, error) {
	return nil, nil
}
func (n *IntegrationMockNetwork) SendFetchMessagesRequest(targetUUID, convID, bucketPath, sinceHLC string) ([]*db.Message, error) {
	return nil, nil
}
func (n *IntegrationMockNetwork) SendPushMessages(targetUUID, convID string, messages []*db.Message) error { return nil }
func (n *IntegrationMockNetwork) SetMerkleSyncHandler(handler syncpkg.MerkleSyncHandler) {}
func (n *IntegrationMockNetwork) ResolveUUID(uuid string) (string, error) { return "mock_resolved_pubkey", nil }

type IntegrationMockFile struct{}

func (f *IntegrationMockFile) GetVirtualTree(p string) (interface{}, error) { return nil, nil }
func (f *IntegrationMockFile) ShareDirectory(p string) error { return nil }
func (f *IntegrationMockFile) UnshareDirectory(p string) error { return nil }
func (f *IntegrationMockFile) GetDirectoryChildren(p string) (interface{}, error) { return nil, nil }
func (f *IntegrationMockFile) GetSharedRoots() []string { return nil }

type IntegrationMockUI struct {
	mu        sync.Mutex
	LastEvent string
	LastData  interface{}
}

func (u *IntegrationMockUI) Notify(name string, data interface{}) {
	u.mu.Lock()
	u.LastEvent = name
	u.LastData = data
	u.mu.Unlock()
}

func (u *IntegrationMockUI) GetLastEvent() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.LastEvent
}

// integrationFixture isolates one identity's files (DB, identity, team keys)
// inside a unique temp directory, so tests never mutate the shared CWD.
type integrationFixture struct {
	dir       string
	dbFile    string
	idFile    string
	teamsFile string
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	dir := t.TempDir()
	return &integrationFixture{
		dir:       dir,
		dbFile:    filepath.Join(dir, "parade.db"),
		idFile:    filepath.Join(dir, ".parade_identity"),
		teamsFile: filepath.Join(dir, ".parade_teams"),
	}
}

func newIntegrationApp(t *testing.T, fx *integrationFixture, bus eventbus.EventBus, cry crypto.Engine, net *IntegrationMockNetwork, ui *IntegrationMockUI) (*app.App, db.Database) {
	t.Helper()
	database, err := db.NewSQLiteDB(fx.dbFile)
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	application := app.NewApp(bus, cry, database, net, &IntegrationMockFile{}, ui, nil).
		WithIdentityPath(fx.idFile).
		WithTeamKeysPath(fx.teamsFile)
	application.Startup()
	return application, database
}

func TestSystem_CompleteUserFlow(t *testing.T) {
	fx := newIntegrationFixture(t)

	bus := eventbus.New()
	cry := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet := &IntegrationMockNetwork{}
	mockUI := &IntegrationMockUI{}
	application, database := newIntegrationApp(t, fx, bus, cry, mockNet, mockUI)

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

		if mockUI.GetLastEvent() != "ui_new_message" {
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
		newCry := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
		newUI := &IntegrationMockUI{}

		newApp, newDB := newIntegrationApp(t, fx, newBus, newCry, mockNet, newUI)
		defer newDB.Close()

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
	fx := newIntegrationFixture(t)

	pwd := "safe_password_123"
	secret := "team_reuse_test_secret"

	// Round 1: create identity, join team, record UUID
	bus1 := eventbus.New()
	cry1 := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet := &IntegrationMockNetwork{}
	mockUI := &IntegrationMockUI{}

	app1, db1 := newIntegrationApp(t, fx, bus1, cry1, mockNet, mockUI)

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
	cry2 := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet2 := &IntegrationMockNetwork{}
	mockUI2 := &IntegrationMockUI{}

	app2, db2 := newIntegrationApp(t, fx, bus2, cry2, mockNet2, mockUI2)

	if err := app2.Login(pwd); err != nil {
		t.Fatalf("Login failed: %v", err)
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
	fx := newIntegrationFixture(t)

	pwd := "safe_password_123"

	// Create identity, join team so .parade_teams exists in the fixture dir
	bus1 := eventbus.New()
	cry1 := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet := &IntegrationMockNetwork{}
	mockUI := &IntegrationMockUI{}

	app1, db1 := newIntegrationApp(t, fx, bus1, cry1, mockNet, mockUI)
	app1.Register(pwd)
	app1.Login(pwd)
	app1.JoinTeamWithName("Auto Team", "auto_secret")
	db1.Close()

	// New app: Login should trigger Start
	bus2 := eventbus.New()
	cry2 := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet2 := &IntegrationMockNetwork{}
	mockUI2 := &IntegrationMockUI{}

	app2, db2 := newIntegrationApp(t, fx, bus2, cry2, mockNet2, mockUI2)

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
	fx := newIntegrationFixture(t)

	pwd := "safe_password_123"
	bus := eventbus.New()
	cry := crypto.NewEngine(crypto.WithTeamKeysFile(fx.teamsFile))
	mockNet := &IntegrationMockNetwork{}
	mockUI := &IntegrationMockUI{}

	application, database := newIntegrationApp(t, fx, bus, cry, mockNet, mockUI)
	defer database.Close()

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

	tActive, _ := application.GetActiveTeam()
	histBefore, _ := application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
	// Retry with backoff if message not yet persisted (async write)
	for retry := 0; len(histBefore) == 0 && retry < 10; retry++ {
		time.Sleep(10 * time.Millisecond)
		histBefore, _ = application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
	}
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

		histAfter, _ := application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
		for retry := 0; len(histAfter) == 0 && retry < 10; retry++ {
			time.Sleep(10 * time.Millisecond)
			histAfter, _ = application.GetConversationMessages(app.DeriveTeamConvID(tActive), 10, 0)
		}
		countAfter := len(histAfter)
		t.Logf("Messages after self-publish: %d", countAfter)

		if countAfter != countBefore {
			t.Errorf("Self-published message was not deduplicated: before=%d after=%d", countBefore, countAfter)
		}
	}
}

// TestSystem_IsolatedDataDirs verifies that two apps with distinct data
// directories never share team keys or team-key files, even when both
// identities use the same password (identical master key).
func TestSystem_IsolatedDataDirs(t *testing.T) {
	fxA := newIntegrationFixture(t)
	fxB := newIntegrationFixture(t)

	pwd := "same_password_for_both"
	secret := "team_secret_of_A"

	// App A: register, join a team — team keys must persist into A's dir only.
	busA := eventbus.New()
	cryA := crypto.NewEngine(crypto.WithTeamKeysFile(fxA.teamsFile))
	appA, dbA := newIntegrationApp(t, fxA, busA, cryA, &IntegrationMockNetwork{}, &IntegrationMockUI{})
	defer dbA.Close()

	if err := appA.Register(pwd); err != nil {
		t.Fatalf("Register A failed: %v", err)
	}
	if err := appA.Login(pwd); err != nil {
		t.Fatalf("Login A failed: %v", err)
	}
	if _, err := appA.JoinTeamWithName("Team A", secret); err != nil {
		t.Fatalf("JoinTeamWithName A failed: %v", err)
	}

	if _, err := os.Stat(fxA.teamsFile); err != nil {
		t.Errorf("teams file missing in A's data dir: %v", err)
	}

	// App B: same password, different data dir — must not see A's team keys.
	busB := eventbus.New()
	cryB := crypto.NewEngine(crypto.WithTeamKeysFile(fxB.teamsFile))
	appB, dbB := newIntegrationApp(t, fxB, busB, cryB, &IntegrationMockNetwork{}, &IntegrationMockUI{})
	defer dbB.Close()

	if err := appB.Register(pwd); err != nil {
		t.Fatalf("Register B failed: %v", err)
	}
	if err := appB.Login(pwd); err != nil {
		t.Fatalf("Login B failed: %v", err)
	}

	teams, err := appB.ListTeams()
	if err != nil {
		t.Fatalf("ListTeams B failed: %v", err)
	}
	if len(teams) != 0 {
		t.Errorf("team keys leaked across data dirs: B sees %v", teams)
	}
	if _, err := os.Stat(fxB.teamsFile); !os.IsNotExist(err) {
		t.Errorf("B's data dir must not contain a teams file (stat err=%v)", err)
	}
}
