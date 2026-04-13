// internal/app/wails_ui.go
package app

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type WailsUI struct {
	ctx context.Context
}

func NewWailsUI(ctx context.Context) *WailsUI {
	return &WailsUI{ctx: ctx}
}

func (w *WailsUI) Notify(eventName string, data interface{}) {
	runtime.EventsEmit(w.ctx, eventName, data)
}
