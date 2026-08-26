package db

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 测试配置
// setupTest 初始化测试数据库并返回清理函数
func setupTest(t testing.TB) (Database, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_parade_stress.db")

	db, err := NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}

	return db, func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close test database: %v", err)
		}
	}
}

// 1. 验证 HLC 时序排序与差量拉取
func TestMessage_HLCSortingAndSync(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	// 模拟乱序到达的消息
	// HLC 格式: UTC_Counter_NodeID
	msgs := []*Message{
		{ID: "1", HLC: "2026-04-13T10:00:01Z_0001_A", Content: []byte("A")},
		{ID: "3", HLC: "2026-04-13T10:00:03Z_0001_A", Content: []byte("C")},
		{ID: "2", HLC: "2026-04-13T10:00:02Z_0001_B", Content: []byte("B")},
	}

	for _, m := range msgs {
		if err := db.InsertMessage(ctx, m); err != nil {
			t.Errorf("Insert failed: %v", err)
		}
	}

	// 从 T1 之后开始同步，预期应该按顺序拿到 T2, T3
	results, err := db.GetMessagesSinceHLC(ctx, "2026-04-13T10:00:01Z_0001_A", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(results))
	}

	if results[0].ID != "2" || results[1].ID != "3" {
		t.Error("HLC sorting failed: messages are not in lexicographical order")
	}
}

// 2. 验证事务原子性 (Rollback)
func TestTransaction_AtomicRollback(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	// 模拟事务执行，中间发生错误
	err := db.RunInTx(ctx, func(tx DBTx) error {
		_ = tx.InsertMessageTx(ctx, &Message{ID: "tx_1", HLC: "H1", Content: []byte("data")})

		// 模拟某种业务逻辑失败
		return fmt.Errorf("simulated business error")
	})

	if err == nil {
		t.Error("Transaction should have returned error")
	}

	// 验证数据库里是否为空（即使插入了 tx_1 也应该被回滚）
	msgs, _ := db.GetRecentMessages(ctx, 10, 0)
	if len(msgs) != 0 {
		t.Error("Transaction failed to rollback: data persisted after error")
	}
}

// 3. 极速写压力测试：模拟文件分块传输更新
func TestFileLog_HighFrequencyUpsert(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	taskID := "file_hash_123"
	totalSize := int64(1024 * 1024 * 100) // 100MB

	// 模拟 100 次分块更新（每次 1MB）
	for i := int64(1); i <= 100; i++ {
		log := &FileLog{
			TaskID:      taskID,
			FilePath:    "/movies/inception.mp4",
			TotalSize:   totalSize,
			Transferred: i * 1024 * 1024,
			Status:      0, // 传输中
			UpdatedAt:   time.Now(),
		}
		if err := db.UpsertFileLog(ctx, log); err != nil {
			t.Fatalf("Upsert failed at %d: %v", i, err)
		}
	}

	// 验证最终状态
	finalLog, _ := db.GetFileLog(ctx, taskID)
	if finalLog.Transferred != 100*1024*1024 {
		t.Errorf("Upsert logic failed: expected 100MB transferred, got %d", finalLog.Transferred)
	}
}

// 4. 终极测试：混合并发对抗
// 模拟场景：
// - 10 个线程在不停地收发聊天消息
// - 10 个线程在同步更新文件下载进度
// - 5 个线程在疯狂查询历史记录
func TestConcurrency_HeavyMixedLoad(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	var wg sync.WaitGroup
	duration := 2 * time.Second // 压测持续时间
	stop := make(chan struct{})

	// A. 模拟聊天消息写入
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(nodeIdx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = db.InsertMessage(ctx, &Message{
						ID:       fmt.Sprintf("msg_%d_%d", nodeIdx, rand.Intn(1000000)),
						HLC:      fmt.Sprintf("%d_HLC", time.Now().UnixNano()),
						Content:  []byte("stress_test_payload"),
						SenderID: "test_node",
					})
					time.Sleep(time.Millisecond * 2) // 模拟高频发言
				}
			}
		}(i)
	}

	// B. 模拟文件下载进度更新 (同一个文件的竞争更新)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(taskIdx int) {
			defer wg.Done()
			tid := fmt.Sprintf("task_%d", taskIdx)
			for {
				select {
				case <-stop:
					return
				default:
					_ = db.UpsertFileLog(ctx, &FileLog{
						TaskID:      tid,
						Transferred: rand.Int63n(1000),
						UpdatedAt:   time.Now(),
					})
					time.Sleep(time.Millisecond * 5)
				}
			}
		}(i)
	}

	// C. 模拟前端查询
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = db.GetRecentMessages(ctx, 20, 0)
					time.Sleep(time.Millisecond * 10)
				}
			}
		}()
	}

	// 运行一段时间后停止
	time.Sleep(duration)
	close(stop)
	wg.Wait()
	t.Log("Concurrency test completed without deadlocks or crashes.")
}

// 5. 性能基准测试：批量插入
func BenchmarkBatchInsert(b *testing.B) {
	db, cleanup := setupTest(nil)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.RunInTx(ctx, func(tx DBTx) error {
			for j := 0; j < 100; j++ {
				_ = tx.InsertMessageTx(ctx, &Message{
					ID:      fmt.Sprintf("%d_%d", i, j),
					HLC:     fmt.Sprintf("%d", time.Now().UnixNano()),
					Content: []byte("perf_test"),
				})
			}
			return nil
		})
	}
}

func TestConversation_UpsertCreate(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	conv := &Conversation{
		ID:          "conv-1",
		TeamID:      "team-1",
		Type:        "private",
		DisplayName: "alice_bob",
		PeerPubkey:  "pubkey-bob",
		MyPubkey:    "pubkey-alice",
		LastHLC:     "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("UpsertConversation failed: %v", err)
	}

	got, err := db.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatalf("GetConversation failed: %v", err)
	}
	if got.ID != "conv-1" || got.Type != "private" {
		t.Errorf("unexpected conversation: %+v", got)
	}
}

func TestConversation_UpsertUpdate(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now()
	conv := &Conversation{ID: "conv-2", TeamID: "t1", Type: "team", DisplayName: "old", LastHLC: "", CreatedAt: now, UpdatedAt: now}
	db.UpsertConversation(ctx, conv)

	updated := &Conversation{ID: "conv-2", TeamID: "t1", Type: "team", DisplayName: "new", LastHLC: "hlc-123", UpdatedAt: time.Now()}
	db.UpsertConversation(ctx, updated)

	got, _ := db.GetConversation(ctx, "conv-2")
	if got.DisplayName != "new" {
		t.Errorf("display_name not updated: %s", got.DisplayName)
	}
	if got.LastHLC != "hlc-123" {
		t.Errorf("last_hlc not updated: %s", got.LastHLC)
	}
}

func TestConversation_ListByTeam(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	db.UpsertConversation(ctx, &Conversation{ID: "c1", TeamID: "team-a", Type: "team", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	db.UpsertConversation(ctx, &Conversation{ID: "c2", TeamID: "team-a", Type: "private", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	db.UpsertConversation(ctx, &Conversation{ID: "c3", TeamID: "team-b", Type: "team", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	convs, err := db.ListConversations(ctx, "team-a")
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if len(convs) != 2 {
		t.Errorf("expected 2 convs for team-a, got %d", len(convs))
	}
}

func TestConversation_GetMessages(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		db.InsertMessage(ctx, &Message{
			ID:             fmt.Sprintf("m%d", i),
			HLC:            fmt.Sprintf("hlc-%d", i),
			ConversationID: "conv-x",
			Content:        []byte("hello"),
		})
	}

	msgs, err := db.GetConversationMessages(ctx, "conv-x", 10, 0)
	if err != nil {
		t.Fatalf("GetConversationMessages failed: %v", err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(msgs))
	}
}

func TestConversation_GetMessagesSinceHLC(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		db.InsertMessage(ctx, &Message{
			ID:             fmt.Sprintf("m%d", i),
			HLC:            fmt.Sprintf("hlc-%d", i),
			ConversationID: "conv-y",
			Content:        []byte("hello"),
		})
	}

	msgs, err := db.GetConversationMessagesSinceHLC(ctx, "conv-y", "hlc-2", 10)
	if err != nil {
		t.Fatalf("GetConversationMessagesSinceHLC failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages after hlc-2, got %d", len(msgs))
	}
}

func TestConversation_UpdateLastHLC(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	db.UpsertConversation(ctx, &Conversation{ID: "conv-z", TeamID: "t1", Type: "team", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	db.UpdateConversationLastHLC(ctx, "conv-z", "hlc-latest")

	got, _ := db.GetConversation(ctx, "conv-z")
	if got.LastHLC != "hlc-latest" {
		t.Errorf("last_hlc not updated: %s", got.LastHLC)
	}
}

func TestConversation_InsertMessageWithConvID(t *testing.T) {
	db, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	db.UpsertConversation(ctx, &Conversation{ID: "cv-msg", TeamID: "t1", Type: "team", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	db.InsertMessage(ctx, &Message{
		ID:             "msg-with-conv",
		HLC:            "hlc-msg",
		ConversationID: "cv-msg",
		Content:        []byte("with conv"),
	})

	msgs, err := db.GetConversationMessages(ctx, "cv-msg", 10, 0)
	if err != nil {
		t.Fatalf("GetConversationMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ConversationID != "cv-msg" {
		t.Errorf("conversation_id not stored correctly")
	}
}
