package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEventBus_FullFlow(t *testing.T) {
	bus := New()

	// 定义计数器，验证是否所有订阅者都收到了事件
	var wg sync.WaitGroup
	wg.Add(2)

	// 1. 模拟逻辑层订阅：负责入库
	bus.Subscribe(TopicMsgReceived, func(ctx context.Context, event Event) {
		defer wg.Done()
		payload := event.Payload.(MsgReceivedPayload) // 类型断言
		if payload.SenderID != "node_a" {
			t.Errorf("Expected sender node_a, got %s", payload.SenderID)
		}
		t.Logf("[Logic] Received message: %s", string(payload.Content))
	})

	// 2. 模拟 App 桥接层订阅：负责推给前端
	bus.Subscribe(TopicMsgReceived, func(ctx context.Context, event Event) {
		defer wg.Done()
		t.Log("[Bridge] Pushing message to UI via Wails...")
	})

	// 3. 模拟网络层发布事件
	mockMsg := MsgReceivedPayload{
		HLC:      "20260413_001",
		SenderID: "node_a",
		Content:  []byte("Hello Parade!"),
	}
	
	bus.Publish(TopicMsgReceived, mockMsg)

	// 等待异步处理完成
	wg.Wait()
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := New()
	callCount := 0
	
	handler := func(ctx context.Context, event Event) {
		callCount++
	}

	subID := bus.Subscribe("test:topic", handler)
	
	// 第一次发布，应该触发
	bus.Publish("test:topic", nil)
	time.Sleep(10 * time.Millisecond)

	// 取消订阅
	bus.Unsubscribe("test:topic", subID)

	// 第二次发布，不应该触发
	bus.Publish("test:topic", nil)
	time.Sleep(10 * time.Millisecond)

	if callCount != 1 {
		t.Errorf("Expected call count 1, got %d", callCount)
	}
}

func TestEventBus_RecoverPanic(t *testing.T) {
	bus := New()
	
	// 订阅一个会崩溃的函数
	bus.Subscribe("panic:topic", func(ctx context.Context, event Event) {
		panic("intentional crash")
	})

	// 发布事件，观察是否导致主测试进程崩溃
	bus.Publish("panic:topic", "data")
	
	// 给一点点处理时间
	time.Sleep(50 * time.Millisecond)
	t.Log("Main process survived the subscriber panic.")
}
