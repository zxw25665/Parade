package network

import (
	"encoding/json"
	"errors"
	"sync"

	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
)

// Envelope 对应设计文档中的信封结构。
type Envelope struct {
	SenderID  string `json:"sender_id"`
	Payload   []byte `json:"payload"`
	Signature string `json:"signature"`
}

// Engine 是网络控制面的联调实现，满足 app.NetworkEngine。
type Engine struct {
	mu      sync.RWMutex
	started bool

	bus    eventbus.EventBus
	crypto crypto.Engine

	discovery *Discovery
	filePlane *FilePlane
}

func NewEngine(bus eventbus.EventBus, cry crypto.Engine) *Engine {
	return &Engine{
		bus:       bus,
		crypto:    cry,
		discovery: NewDiscovery(bus),
		filePlane: NewFilePlane(bus),
	}
}

func (e *Engine) Start(_ int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.started = true
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.started = false
	return nil
}

// BroadcastTeam 用于处理队伍广播消息（联调模式：本地解密并回灌事件总线）。
func (e *Engine) BroadcastTeam(payload []byte) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}

	plain, err := e.crypto.DecryptTeam(payload)
	if err != nil {
		return err
	}

	var msg eventbus.MsgReceivedPayload
	if err := json.Unmarshal(plain, &msg); err != nil {
		return err
	}

	e.bus.Publish(eventbus.TopicMsgReceived, msg)
	return nil
}

// UnicastPrivate 用于定点私聊（联调模式：按队伍密钥解包并发布消息事件）。
func (e *Engine) UnicastPrivate(targetPubKey string, payload []byte) error {
	if targetPubKey == "" {
		return errors.New("target public key is required")
	}

	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}

	plain, err := e.crypto.DecryptTeam(payload)
	if err != nil {
		return err
	}

	var msg eventbus.MsgReceivedPayload
	if err := json.Unmarshal(plain, &msg); err != nil {
		return err
	}

	e.bus.Publish(eventbus.TopicMsgReceived, msg)
	return nil
}

func (e *Engine) Discovery() *Discovery {
	return e.discovery
}

func (e *Engine) FilePlane() *FilePlane {
	return e.filePlane
}
