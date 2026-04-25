package app_test

import (
	"context"
//	"encoding/json"
	"os"
	"testing"
	"time"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
)

// ---- Mock 组件 ----

type IntegrationMockNetwork struct {
	BroadcastCount int
	LastPayload    []byte
}

func (n *IntegrationMockNetwork) Start(p int) error { return nil }
func (n *IntegrationMockNetwork) Stop() error      { return nil }
func (n *IntegrationMockNetwork) BroadcastTeam(b []byte) error {
	n.BroadcastCount++
	n.LastPayload = b
	return nil
}
func (n *IntegrationMockNetwork) UnicastPrivate(t string, b []byte) error { return nil }

type IntegrationMockFile struct{}

func (f *IntegrationMockFile) GetVirtualTree(p string) (interface{}, error) { return nil, nil }
func (f *IntegrationMockFile) StartDownload(t, v, l string) error           { return nil }

type IntegrationMockUI struct {
	LastEvent string
	LastData  interface{}
}

func (u *IntegrationMockUI) Notify(name string, data interface{}) {
	u.LastEvent = name
	u.LastData = data
}

// ---- 核心集成测试 ----

func TestSystem_CompleteUserFlow(t *testing.T) {
	// 1. 环境准备
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

	// 2. 组装并启动系统
	application := app.NewApp(bus, cry, database, mockNet, mockFile, mockUI)
	application.Startup(context.Background())

	pwd := "safe_password_123"

	// --- 场景 A: 注册与登录 ---
	t.Run("Auth", func(t *testing.T) {
		if err := application.Register(pwd); err != nil {
			t.Fatalf("Register failed: %v", err)
		}
		if err := application.Login(pwd); err != nil {
			t.Fatalf("Login failed: %v", err)
		}
	})

	// --- 场景 B: 发送消息 ---
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

	// --- 场景 C: 异步接收消息 ---
	t.Run("Receive", func(t *testing.T) {
		remoteMsg := "Reply from Bob"
		// 使用当前时间 + 高计数器值，确保 HLC 排序在 Send 阶段的消息之后
		ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		bus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{
			HLC:      ts + "_9999_TEST",
			SenderID: "BOB_ID",
			Content:  []byte(remoteMsg),
		})

		time.Sleep(100 * time.Millisecond) // 等待总线分发

		if mockUI.LastEvent != "ui_new_message" {
			t.Error("UI notification missing")
		}
		
		hist, _ := application.GetRecentHistory(1, 0)
		if len(hist) == 0 || hist[0]["content"] != remoteMsg {
			t.Errorf("DB save failed or content mismatch")
		}
	})

	// --- 场景 D: 重启恢复测试 ---
	t.Run("Restart", func(t *testing.T) {
		// 1. 关闭旧连接
		database.Close()

		// 2. 模拟重启：创建全新实例
		newBus := eventbus.New()
		newCry := crypto.NewEngine()
		newDB, _ := db.NewSQLiteDB(dbFile)
		newUI := &IntegrationMockUI{}
		
		newApp := app.NewApp(newBus, newCry, newDB, mockNet, mockFile, newUI)
		newApp.Startup(context.Background())

		// 3. 登录并验证数据还原
		if err := newApp.Login(pwd); err != nil { // 修正：使用 Login 而非 LoadIdentity
			t.Fatalf("Login after restart failed: %v", err)
		}

		history, _ := newApp.GetRecentHistory(10, 0)
		if len(history) < 2 {
			t.Errorf("Data lost? Expected 2+ msgs, got %d", len(history))
		}
		
		t.Logf("Successfully restored %d messages from encrypted SQLite.", len(history))
	})
}
