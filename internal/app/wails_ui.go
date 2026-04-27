// internal/app/wails_ui.go
package app

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type WailsUI struct {
	ctx context.Context
}

func NewWailsUI() *WailsUI {
	return &WailsUI{}
}

func NewWailsUIWithContext(ctx context.Context) *WailsUI {
	return &WailsUI{ctx: ctx}
}

func (w *WailsUI) SetContext(ctx context.Context) {
	w.ctx = ctx
}

func (w *WailsUI) Notify(eventName string, data interface{}) {
	if w.ctx == nil {
		return
	}
	runtime.EventsEmit(w.ctx, eventName, data)
}
