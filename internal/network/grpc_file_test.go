package network

import (
	"context"
	"testing"
	"time"

	"parade/internal/core/eventbus"
)

func TestFilePlaneRequestAndLastRequest(t *testing.T) {
	bus := eventbus.New()
	plane := NewFilePlane(bus)

	progressCh := make(chan eventbus.FileProgressPayload, 1)
	bus.Subscribe(eventbus.TopicFileProgress, func(_ context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.FileProgressPayload)
		if ok {
			progressCh <- payload
		}
	})

	err := plane.RequestFileChunk("peer-a", "task-1", 2048)
	if err != nil {
		t.Fatalf("RequestFileChunk failed: %v", err)
	}

	req, ok := plane.LastRequest("task-1")
	if !ok {
		t.Fatal("expected request to exist")
	}
	if req.PeerID != "peer-a" || req.Offset != 2048 {
		t.Fatalf("unexpected request state: %+v", req)
	}

	select {
	case payload := <-progressCh:
		if payload.TaskID != "task-1" || payload.Transferred != 2048 {
			t.Fatalf("unexpected progress payload: %+v", payload)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive file progress event")
	}
}

func TestFilePlaneMarkCompleted(t *testing.T) {
	bus := eventbus.New()
	plane := NewFilePlane(bus)

	completedCh := make(chan string, 1)
	bus.Subscribe(eventbus.TopicFileCompleted, func(_ context.Context, ev eventbus.Event) {
		taskID, ok := ev.Payload.(string)
		if ok {
			completedCh <- taskID
		}
	})

	plane.MarkCompleted("task-done")

	select {
	case taskID := <-completedCh:
		if taskID != "task-done" {
			t.Fatalf("unexpected completed task id: %s", taskID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive file completed event")
	}
}

func TestFilePlaneRequestValidation(t *testing.T) {
	bus := eventbus.New()
	plane := NewFilePlane(bus)

	if err := plane.RequestFileChunk("", "task", 0); err == nil {
		t.Fatal("expected error for empty peerID")
	}
	if err := plane.RequestFileChunk("peer", "", 0); err == nil {
		t.Fatal("expected error for empty taskID")
	}
	if err := plane.RequestFileChunk("peer", "task", -1); err == nil {
		t.Fatal("expected error for negative offset")
	}
}
