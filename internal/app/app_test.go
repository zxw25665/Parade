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

// ---- Mock 对象实现 ----

type MockNetwork struct {
	LastPayload []byte
}
func (m *MockNetwork) Start(p int) error { return nil }
func (m *MockNetwork) Stop() error      { return nil }
func (m *MockNetwork) BroadcastTeam(b []byte) error { m.LastPayload = b; return nil }
func (m *MockNetwork) UnicastPrivate(t string, b []byte) error { return nil }

type MockFile struct{}
func (m *MockFile) GetVirtualTree(p string) (interface{}, error) { return nil, nil }
func (m *MockFile) StartDownload(t, v, l string) error { return nil }

type MockUI struct {
	EventName string
	Payload   interface{}
}
func (m *MockUI) Notify(name string, data interface{}) {
	m.EventName = name
	m.Payload = data
}

// ---- 测试逻辑 ----

func setup(t *testing.T) (*App, *MockNetwork, *MockUI, func()) {
	dbP, idP := "./test.db", "./test.id"
	_ = os.Remove(dbP); _ = os.Remove(idP)

	eb := eventbus.New()
	cr := crypto.NewEngine()
	d, _ := db.NewSQLiteDB(dbP)
	net := &MockNetwork{}
	file := &MockFile{}
	ui := &MockUI{}

	app := NewApp(eb, cr, d, net, file, ui)
	app.Startup(context.Background())

	return app, net, ui, func() {
		d.Close(); os.Remove(dbP); os.Remove(idP)
	}
}

func TestApp_FullFlow(t *testing.T) {
	a, net, ui, cleanup := setup(t)
	defer cleanup()

	// 1. 认证
	_ = a.Register("123")
	_ = a.Login("123")
	_ = a.JoinTeam("team")

	// 2. 测试发送
	txt := "Hello World"
	_ = a.SendTeamChat(txt)

	// 校验库
	hist, _ := a.GetRecentHistory(1, 0)
	if hist[0]["content"] != txt {
		t.Errorf("DB content mismatch")
	}

	// 校验加密发网
	dec, _ := a.crypto.DecryptTeam(net.LastPayload)
	var netPayload eventbus.MsgReceivedPayload
	_ = json.Unmarshal(dec, &netPayload)
	if string(netPayload.Content) != txt {
		t.Errorf("Network payload mismatch")
	}

	// 3. 测试接收 (模拟网络层抛出事件)
	incoming := eventbus.MsgReceivedPayload{
		HLC: "2026-04-13T12:00:00.000Z_0001_REMOTE",
		SenderID: "remote_node",
		Content: []byte("Incoming Message"),
	}
	a.evBus.Publish(eventbus.TopicMsgReceived, incoming)

	// 等待异步处理
	time.Sleep(100 * time.Millisecond)

	// 校验 UI 通知
	if ui.EventName != "ui_new_message" {
		t.Errorf("UI not notified")
	}
	uiData := ui.Payload.(map[string]interface{})
	if uiData["content"] != "Incoming Message" {
		t.Errorf("UI content mismatch")
	}
}

// TestGetRecentHistory_CorruptedMessage 验证：消息解密失败时返回 "[message corrupted]" 占位符而非空内容
func TestGetRecentHistory_CorruptedMessage(t *testing.T) {
	a, _, _, cleanup := setup(t)
	defer cleanup()
	defer os.Remove(IdentityFile)

	_ = a.Register("123")
	_ = a.Login("123")
	_ = a.JoinTeam("team")

	// 向 DB 中插入一条内容为垃圾数据的消息（模拟磁盘损坏或篡改）
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

	// 第一条（最新）应该标记为损坏
	if hist[0]["content"] != "[message corrupted]" {
		t.Errorf("expected corrupted placeholder, got %q", hist[0]["content"])
	}
}

// TestSendTeamChat_ReceiverID 验证：群聊消息的 ReceiverID 为空字符串（ReceiverIDGroupChat）
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
