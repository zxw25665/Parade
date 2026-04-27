package app

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type WailsUI struct {
	mu  sync.Mutex
	ctx context.Context
}

func NewWailsUI() *WailsUI {
	return &WailsUI{}
}

func NewWailsUIWithContext(ctx context.Context) *WailsUI {
	return &WailsUI{ctx: ctx}
}

func (w *WailsUI) SetContext(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ctx = ctx
}

func (w *WailsUI) Notify(eventName string, data interface{}) {
	w.mu.Lock()
	ctx := w.ctx
	w.mu.Unlock()
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, eventName, data)
}
