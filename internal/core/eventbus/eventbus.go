package eventbus

import (
	"context"
//	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid" // 需要运行: go get github.com/google/uuid
)

// Event 封装了传递的事件实体
type Event struct {
	Topic     string
	Payload   interface{} // 任意类型的载荷，消费者需要按 topics.go 中的定义进行断言
	Timestamp time.Time
}

// EventHandler 是订阅者必须提供的回调函数签名
type EventHandler func(ctx context.Context, event Event)

// SubscriptionID 是订阅凭据，用于取消订阅
type SubscriptionID string

// EventBus 定义了总线的标准操作契约
type EventBus interface {
	Subscribe(topic string, handler EventHandler) SubscriptionID
	Unsubscribe(topic string, subID SubscriptionID)
	Publish(topic string, payload interface{})
}

// localEventBus 是内存事件总线的具体实现
type localEventBus struct {
	mu       sync.RWMutex
	handlers map[string]map[SubscriptionID]EventHandler
}

// New 初始化一个全局可用的事件总线实例
func New() EventBus {
	return &localEventBus{
		handlers: make(map[string]map[SubscriptionID]EventHandler),
	}
}

// Subscribe 注册一个事件监听器，返回唯一标识符
func (bus *localEventBus) Subscribe(topic string, handler EventHandler) SubscriptionID {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	if bus.handlers[topic] == nil {
		bus.handlers[topic] = make(map[SubscriptionID]EventHandler)
	}

	subID := SubscriptionID(uuid.New().String())
	bus.handlers[topic][subID] = handler

	return subID
}

// Unsubscribe 注销指定的事件监听器，防止内存泄漏
func (bus *localEventBus) Unsubscribe(topic string, subID SubscriptionID) {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	if _, ok := bus.handlers[topic]; ok {
		delete(bus.handlers[topic], subID)
		// 如果该主题下没有订阅者了，清理掉 Map 以释放空间
		if len(bus.handlers[topic]) == 0 {
			delete(bus.handlers, topic)
		}
	}
}

// Publish 发布事件（完全异步非阻塞，并且包含 Panic 恢复机制）
// ... 接上一段 Publish 方法实现

// Publish 发布事件（完全异步非阻塞，并且包含 Panic 恢复机制）
func (bus *localEventBus) Publish(topic string, payload interface{}) {
	bus.mu.RLock()
	// 获取该主题下的所有处理函数（拷贝一份引用，缩小锁范围）
	handlersMap, exists := bus.handlers[topic]
	if !exists || len(handlersMap) == 0 {
		bus.mu.RUnlock()
		return
	}

	// 准备事件实体
	event := Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	// 复制处理函数列表，避免在循环时发生死锁或由于取消订阅导致的竞争
	handlers := make([]EventHandler, 0, len(handlersMap))
	for _, h := range handlersMap {
		handlers = append(handlers, h)
	}
	bus.mu.RUnlock()

	// 异步派发事件
	for _, handler := range handlers {
		go func(h EventHandler) {
			// 核心防御逻辑：捕获订阅者内部可能出现的 Panic
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[EventBus] CRITICAL: Recovered from handler panic in topic [%s]: %v", topic, r)
				}
			}()

			// 设置 5 秒超时上下文，防止某个处理逻辑永久阻塞协程
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			h(ctx, event)
		}(handler)
	}
}
