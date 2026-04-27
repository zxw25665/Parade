package eventbus

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event 封装了传递的事件实体
type Event struct {
	Topic     string
	Payload   interface{}
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

	// PublishSync 同步发布，在当前 goroutine 中顺序执行所有 handler。
	// 主要用于测试和关键路径，消除 time.Sleep 竞态。
	PublishSync(topic string, payload interface{})

	// RegisteredTopics 返回当前有活跃订阅者的 topic 列表（调试诊断用）。
	RegisteredTopics() []string
}

// localEventBus 是内存事件总线的具体实现。
// 每个 topic 独立运行一个 goroutine，保证同一 topic 内事件 FIFO 有序投递。
type localEventBus struct {
	mu   sync.RWMutex
	done bool

	handlers map[string]map[SubscriptionID]EventHandler

	// per-topic goroutine 管理
	topicChs  map[string]chan Event
	cancelFns map[string]context.CancelFunc
	wg        sync.WaitGroup

	handlerTimeout time.Duration
}

// New 初始化一个全局可用的事件总线实例。默认 handler 超时 5 秒。
func New() EventBus {
	return &localEventBus{
		handlers:       make(map[string]map[SubscriptionID]EventHandler),
		topicChs:       make(map[string]chan Event),
		cancelFns:      make(map[string]context.CancelFunc),
		handlerTimeout: 5 * time.Second,
	}
}

// NewWithTimeout 创建一个可自定义 handler 超时的事件总线。
func NewWithTimeout(timeout time.Duration) EventBus {
	bus := New().(*localEventBus)
	bus.handlerTimeout = timeout
	return bus
}

// Subscribe 注册一个事件监听器，返回唯一标识符。
// 首次订阅某 topic 时会自动启动该 topic 的后台分发 goroutine。
func (bus *localEventBus) Subscribe(topic string, handler EventHandler) SubscriptionID {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	if bus.done {
		return ""
	}

	if bus.handlers[topic] == nil {
		bus.handlers[topic] = make(map[SubscriptionID]EventHandler)
	}

	subID := SubscriptionID(uuid.New().String())
	bus.handlers[topic][subID] = handler

	bus.ensureTopicLoopLocked(topic)

	return subID
}

// Unsubscribe 注销指定的事件监听器。当 topic 下再无订阅者时，
// 自动取消该 topic 的后台分发 goroutine 并回收资源。
// 注意：channel 不会在此关闭，由 goroutine 通过 ctx.Done() 退出后自然 GC。
func (bus *localEventBus) Unsubscribe(topic string, subID SubscriptionID) {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	if _, ok := bus.handlers[topic]; !ok {
		return
	}

	delete(bus.handlers[topic], subID)

	if len(bus.handlers[topic]) == 0 {
		delete(bus.handlers, topic)
		if cancel, ok := bus.cancelFns[topic]; ok {
			cancel()
			delete(bus.cancelFns, topic)
		}
		delete(bus.topicChs, topic)
	}
}

// ensureTopicLoopLocked 在 topic 首次被订阅时启动后台 goroutine。
// 必须在 bus.mu 写锁下调用。
func (bus *localEventBus) ensureTopicLoopLocked(topic string) {
	if _, ok := bus.topicChs[topic]; ok {
		return
	}

	ch := make(chan Event, 256)
	ctx, cancel := context.WithCancel(context.Background())
	bus.topicChs[topic] = ch
	bus.cancelFns[topic] = cancel

	bus.wg.Add(1)
	go bus.runTopicLoop(ctx, topic, ch)
}

// runTopicLoop 是每个 topic 的后台分发循环。
// 从 channel 中 FIFO 取出事件，顺序分发给所有订阅者。
func (bus *localEventBus) runTopicLoop(ctx context.Context, topic string, ch <-chan Event) {
	defer bus.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			bus.dispatch(ctx, topic, ev)
		}
	}
}

// dispatch 将一条事件按订阅顺序分发给 topic 下的所有 handler。
// handler 间的 panic 被隔离，不会影响其他 handler 或导致进程退出。
func (bus *localEventBus) dispatch(ctx context.Context, topic string, ev Event) {
	bus.mu.RLock()
	handlersMap := bus.handlers[topic]
	handlers := make([]EventHandler, 0, len(handlersMap))
	for _, h := range handlersMap {
		handlers = append(handlers, h)
	}
	bus.mu.RUnlock()

	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[EventBus] CRITICAL: Recovered from handler panic in topic [%s]: %v", topic, r)
				}
			}()
			hCtx, cancel := context.WithTimeout(ctx, bus.handlerTimeout)
			defer cancel()
			h(hCtx, ev)
		}()
	}
}

// Publish 发布事件到指定 topic（异步，非阻塞）。
// 如果 topic 无订阅者，日志警告但不崩溃。
// 如果 channel 已满，丢弃事件并日志警告。
func (bus *localEventBus) Publish(topic string, payload interface{}) {
	ev := Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	bus.mu.RLock()
	ch, ok := bus.topicChs[topic]
	bus.mu.RUnlock()

	if !ok {
		log.Printf("[EventBus] WARN: publishing to topic [%s] with zero subscribers (possible typo?)", topic)
		return
	}

	select {
	case ch <- ev:
	default:
		log.Printf("[EventBus] WARN: topic [%s] channel full (cap=%d), dropping event", topic, cap(ch))
	}
}

// PublishSync 同步发布，在当前 goroutine 中直接执行所有 handler，不经过 channel。
// 无超时限制。用于测试和必须同步完成的场景。
func (bus *localEventBus) PublishSync(topic string, payload interface{}) {
	bus.mu.RLock()
	handlersMap, exists := bus.handlers[topic]
	if !exists || len(handlersMap) == 0 {
		bus.mu.RUnlock()
		return
	}
	handlers := make([]EventHandler, 0, len(handlersMap))
	for _, h := range handlersMap {
		handlers = append(handlers, h)
	}
	bus.mu.RUnlock()

	ev := Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[EventBus] CRITICAL: Recovered from handler panic in sync publish [%s]: %v", topic, r)
				}
			}()
			h(context.Background(), ev)
		}()
	}
}

// RegisteredTopics 返回当前有活跃订阅者的 topic 列表（字典序排序）。
func (bus *localEventBus) RegisteredTopics() []string {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	topics := make([]string, 0, len(bus.handlers))
	for t := range bus.handlers {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return topics
}
