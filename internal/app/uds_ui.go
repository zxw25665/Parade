package app

import (
	"encoding/json"
	"log"
	"sync"
)

type UDSFrontend struct {
	hub *UDSHub
	mu  sync.Mutex
}

func NewUDSFrontend(hub *UDSHub) *UDSFrontend {
	return &UDSFrontend{hub: hub}
}

func (u *UDSFrontend) Notify(eventName string, data interface{}) {
	u.mu.Lock()
	hub := u.hub
	u.mu.Unlock()

	if hub == nil {
		return
	}

	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "event",
		"params": map[string]interface{}{
			"event": eventName,
			"data":  data,
		},
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[uds_ui] failed to marshal event %s: %v", eventName, err)
		return
	}

	hub.Broadcast(payload)
}

type NullUI struct{}

func (n *NullUI) Notify(eventName string, data interface{}) {}
