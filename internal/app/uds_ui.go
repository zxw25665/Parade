package app

import (
	"encoding/json"
	"log"
	"sync"
)

type StdioFrontend struct {
	hub IPCClientHub
	mu  sync.Mutex
}

func NewStdioFrontend(hub IPCClientHub) *StdioFrontend {
	return &StdioFrontend{hub: hub}
}

func (s *StdioFrontend) Notify(eventName string, data any) {
	s.mu.Lock()
	hub := s.hub
	s.mu.Unlock()

	if hub == nil {
		return
	}

	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  "event",
		"params": map[string]any{
			"event": eventName,
			"data":  data,
		},
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[stdio] failed to marshal event %s: %v", eventName, err)
		return
	}

	hub.Broadcast(payload)
}

type NullUI struct{}

func (n *NullUI) Notify(eventName string, data any) {}
