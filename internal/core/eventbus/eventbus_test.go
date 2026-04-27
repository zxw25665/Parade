package eventbus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEventBus_FullFlow 验证：订阅 → 异步发布 → 所有 handler 收到事件
func TestEventBus_FullFlow(t *testing.T) {
	bus := New()

	var wg sync.WaitGroup
	wg.Add(2)

	bus.Subscribe(TopicMsgReceived, func(ctx context.Context, event Event) {
		defer wg.Done()
		payload := event.Payload.(MsgReceivedPayload)
		if payload.SenderID != "node_a" {
			t.Errorf("Expected sender node_a, got %s", payload.SenderID)
		}
	})

	bus.Subscribe(TopicMsgReceived, func(ctx context.Context, event Event) {
		defer wg.Done()
	})

	bus.Publish(TopicMsgReceived, MsgReceivedPayload{
		HLC:      "20260413_001",
		SenderID: "node_a",
		Content:  []byte("Hello Parade!"),
	})

	ok := waitTimeout(&wg, time.Second)
	if !ok {
		t.Fatal("handlers were not invoked within timeout")
	}
}

func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestEventBus_PublishSync 验证：同步发布，handler 在当前 goroutine 中立即执行
func TestEventBus_PublishSync(t *testing.T) {
	bus := New()

	called := false
	bus.Subscribe("test:sync", func(ctx context.Context, event Event) {
		called = true
	})

	bus.PublishSync("test:sync", "payload")

	if !called {
		t.Error("PublishSync handler should have been called synchronously")
	}
}

// TestEventBus_Unsubscribe 验证：取消订阅后 handler 不再被调用
func TestEventBus_Unsubscribe(t *testing.T) {
	bus := New()
	var callCount int32

	handler := func(ctx context.Context, event Event) {
		atomic.AddInt32(&callCount, 1)
	}

	subID := bus.Subscribe("test:topic", handler)

	bus.PublishSync("test:topic", nil)
	if c := atomic.LoadInt32(&callCount); c != 1 {
		t.Fatalf("expected call count 1, got %d", c)
	}

	bus.Unsubscribe("test:topic", subID)

	bus.PublishSync("test:topic", nil)
	if c := atomic.LoadInt32(&callCount); c != 1 {
		t.Fatalf("expected call count 1 after unsubscribe, got %d", c)
	}
}

// TestEventBus_RecoverPanic 验证：handler 的 panic 不会导致进程崩溃
func TestEventBus_RecoverPanic(t *testing.T) {
	bus := New()

	bus.Subscribe("panic:topic", func(ctx context.Context, event Event) {
		panic("intentional crash")
	})

	bus.Publish("panic:topic", "data")
	bus.PublishSync("panic:topic", "data")

	t.Log("Process survived subscriber panics.")
}

// TestEventBus_TopicOrdering 验证：同一 topic 内事件按发布顺序投递
func TestEventBus_TopicOrdering(t *testing.T) {
	bus := New()

	var received []int
	var mu sync.Mutex

	bus.Subscribe("test:ordered", func(ctx context.Context, event Event) {
		mu.Lock()
		received = append(received, event.Payload.(int))
		mu.Unlock()
	})

	for i := 0; i < 100; i++ {
		bus.PublishSync("test:ordered", i)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 100 {
		t.Fatalf("expected 100 events, got %d", len(received))
	}
	for i, v := range received {
		if v != i {
			t.Fatalf("order violation at index %d: expected %d, got %d", i, i, v)
		}
	}
}

// TestEventBus_RegisteredTopics 验证：RegisteredTopics 返回有订阅者的 topic 列表
func TestEventBus_RegisteredTopics(t *testing.T) {
	bus := New()

	topics := bus.RegisteredTopics()
	if len(topics) != 0 {
		t.Fatalf("expected empty topics, got %v", topics)
	}

	h := func(ctx context.Context, event Event) {}
	bus.Subscribe("topic:a", h)
	bus.Subscribe("topic:b", h)

	topics = bus.RegisteredTopics()
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics, got %v", topics)
	}
	if topics[0] != "topic:a" || topics[1] != "topic:b" {
		t.Fatalf("expected sorted [topic:a topic:b], got %v", topics)
	}
}

// TestEventBus_TopicNoSubscriber 验证：向无订阅者的 topic publish 不崩溃
func TestEventBus_TopicNoSubscriber(t *testing.T) {
	bus := New()

	bus.Publish("nonexistent:topic", "data")
	bus.PublishSync("nonexistent:topic", "data")
}

// TestEventBus_ConcurrentPublishDifferentTopics 验证：不同 topic 可安全并发发布
func TestEventBus_ConcurrentPublishDifferentTopics(t *testing.T) {
	bus := New()

	var wg sync.WaitGroup
	countA := new(atomic.Int32)
	countB := new(atomic.Int32)

	bus.Subscribe("topic:a", func(ctx context.Context, event Event) {
		countA.Add(1)
	})
	bus.Subscribe("topic:b", func(ctx context.Context, event Event) {
		countB.Add(1)
	})

	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			bus.Publish("topic:a", nil)
		}()
		go func() {
			defer wg.Done()
			bus.Publish("topic:b", nil)
		}()
	}

	wg.Wait()

	if ok := waitForCount(countA, 100, time.Second); !ok {
		t.Fatalf("topic:a expected 100 events, got %d", countA.Load())
	}
	if ok := waitForCount(countB, 100, time.Second); !ok {
		t.Fatalf("topic:b expected 100 events, got %d", countB.Load())
	}
}

// TestEventBus_UnsubscribeCleansUpGoroutine 验证：取消最后一个订阅停止 goroutine
func TestEventBus_UnsubscribeCleansUpGoroutine(t *testing.T) {
	bus := New().(*localEventBus)

	h := func(ctx context.Context, event Event) {}
	sub1 := bus.Subscribe("test:gc", h)

	time.Sleep(10 * time.Millisecond)

	bus.mu.RLock()
	_, hasCh := bus.topicChs["test:gc"]
	_, hasCancel := bus.cancelFns["test:gc"]
	bus.mu.RUnlock()

	if !hasCh {
		t.Fatal("topic channel should exist after subscribe")
	}
	if !hasCancel {
		t.Fatal("cancel function should exist after subscribe")
	}

	bus.Unsubscribe("test:gc", sub1)

	time.Sleep(10 * time.Millisecond)

	bus.mu.RLock()
	_, hasCh = bus.topicChs["test:gc"]
	_, hasCancel = bus.cancelFns["test:gc"]
	_, hasHandlers := bus.handlers["test:gc"]
	bus.mu.RUnlock()

	if hasCh {
		t.Fatal("topic channel should be cleaned after last unsubscribe")
	}
	if hasCancel {
		t.Fatal("cancel function should be cleaned after last unsubscribe")
	}
	if hasHandlers {
		t.Fatal("handlers map should be cleaned after last unsubscribe")
	}
}

func waitForCount(c *atomic.Int32, target int32, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.Load() >= target {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
