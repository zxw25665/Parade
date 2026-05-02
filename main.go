package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/file"
	"parade/internal/network"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	eventBus := eventbus.New()
	cry := crypto.NewEngine()

	dbPath := "./.parade_data.db"
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	fileEngine := file.NewEngine().
		WithDatabase(database).
		WithEventBus(eventBus)
	defer fileEngine.Close()

	netEngine := network.NewEngine(eventBus, cry)
	netEngine.AttachFileEngine(fileEngine)
	defer netEngine.Stop()

	wailsUI := app.NewWailsUI()

	application := app.NewApp(eventBus, cry, database, netEngine, fileEngine, wailsUI)

	err = wails.Run(&options.App{
		Title:  "Parade (游行)",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			wailsUI.SetContext(ctx)
			application.Startup(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			application.Shutdown()
		},
		Bind: []interface{}{
			application,
		},
	})

	if err != nil {
		log.Fatalf("Wails application error: %v", err)
	}
}
