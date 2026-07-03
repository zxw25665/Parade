package main

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	"parade/internal/file"
	"parade/internal/network"
)

//go:embed all:deprecated/frontend/dist
var assets embed.FS

const AppVersion = "v0.2.0-libp2p"

var appInstance *app.App

func main() {
	log.Printf("[Parade] %s starting", AppVersion)
	eventBus := eventbus.New()
	cry := crypto.NewEngine()

	dbPath := "./.parade_data.db"
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	logBroker, err := logger.NewLogBroker("./.parade.log", 5000)
	if err != nil {
		log.Fatalf("Failed to create log broker: %v", err)
	}
	defer logBroker.Close()

	fileEngine := file.NewEngine().
		WithDatabase(database).
		WithEventBus(eventBus).
		WithLogger(logBroker)
	if err := fileEngine.LoadSharedDirectories(); err != nil {
		fmt.Printf("failed to load shared directories: %v\n", err)
	}
	defer fileEngine.Close()

	netEngine := network.NewLibp2pEngine(eventBus, cry, logBroker)
	netEngine.AttachFileEngine(fileEngine)
	defer netEngine.Stop()

	wailsUI := app.NewWailsUI()

	appInstance = app.NewApp(eventBus, cry, database, netEngine, fileEngine, wailsUI, logBroker).
		WithIdentityPath("./.parade_identity")

	err = wails.Run(&options.App{
		Title:  "Parade " + AppVersion,
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			wailsUI.SetContext(ctx)
			appInstance.Startup()
		},
		OnShutdown: func(ctx context.Context) {
			appInstance.Shutdown()
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.parade.app-7f3a9c2e",
		},
		Bind: []interface{}{
			appInstance,
		},
	})

	if err != nil {
		log.Fatalf("Wails application error: %v", err)
	}
}