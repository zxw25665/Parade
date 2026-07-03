package daemon

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"parade/internal/app"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	"parade/internal/file"
	"parade/internal/network"
)

// Config holds the parsed daemon configuration.
type Config struct {
	UDS        string
	DataDir    string
	Port       int
	ListenAddr string
	Headless   bool
	Debug      bool
	Production bool
}

func Run(args []string) {
	cfg := parseFlags(args)
	validateConfig(cfg)

	// Resolve data directory
	dataDir := cfg.DataDir
	if dataDir == "" {
		var err error
		dataDir, err = os.Getwd()
		if err != nil {
			log.Fatalf("failed to get working directory: %v", err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("failed to create data directory %s: %v", dataDir, err)
	}

	// Single-instance lock (production mode or non-debug mode)
	if !cfg.Debug {
		if err := lockInstance(dataDir); err != nil {
			log.Fatalf("another instance is already running in %s: %v", dataDir, err)
		}
		defer unlockInstance(dataDir)
	}

	// Initialize engines
	eventBus := eventbus.New()
	cry := crypto.NewEngine()

	dbPath := filepath.Join(dataDir, ".parade_data.db")
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	logPath := filepath.Join(dataDir, ".parade.log")
	logBroker, err := logger.NewLogBroker(logPath, 5000)
	if err != nil {
		log.Fatalf("failed to create log broker: %v", err)
	}
	defer logBroker.Close()

	fileEngine := file.NewEngine().
		WithDatabase(database).
		WithEventBus(eventBus).
		WithLogger(logBroker)
	if err := fileEngine.LoadSharedDirectories(); err != nil {
		fmt.Printf("warning: failed to load shared directories: %v\n", err)
	}
	defer fileEngine.Close()

	netEngine := network.NewLibp2pEngine(eventBus, cry, logBroker)
	netEngine.AttachFileEngine(fileEngine)
	defer netEngine.Stop()

	// Create UDS frontend (or nil for headless mode)
	var ui app.Frontend
	var udsSrv *app.UDSServer
	if !cfg.Headless {
		udsSrv = app.NewUDSServer(cfg.UDS)
		ui = app.NewUDSFrontend(udsSrv.Hub())
	} else {
		log.Println("[daemon] headless mode — no UDS listener")
		ui = &app.NullUI{}
	}

	identityPath := filepath.Join(dataDir, ".parade_identity")
	appInstance := app.NewApp(eventBus, cry, database, netEngine, fileEngine, ui, logBroker).
		WithIdentityPath(identityPath)
	appInstance.Startup()

	// Start UDS listener (after app is initialized)
	if udsSrv != nil {
		app.RegisterMethods(appInstance)
		if err := udsSrv.Start(); err != nil {
			log.Fatalf("failed to start UDS listener: %v", err)
		}
		defer udsSrv.Stop()
	}

	log.Printf("[daemon] Parade %s started (pid=%d, data=%s, p2p=%s:%d, uds=%s, headless=%v, debug=%v, production=%v)",
		appVersion, os.Getpid(), dataDir, cfg.ListenAddr, cfg.Port, cfg.UDS, cfg.Headless, cfg.Debug, cfg.Production)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	sig := <-sigCh
	log.Printf("[daemon] received signal %v, shutting down...", sig)

	appInstance.Shutdown()
	log.Println("[daemon] Parade stopped")
}

func parseFlags(args []string) Config {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)

	var cfg Config
	fs.StringVar(&cfg.UDS, "uds", "/tmp/parade.sock", "Unix domain socket path")
	fs.StringVar(&cfg.DataDir, "data-dir", "", "Data directory (default: current directory)")
	fs.IntVar(&cfg.Port, "port", 4327, "P2P listen port")
	fs.StringVar(&cfg.ListenAddr, "listen", "127.0.0.1", "P2P listen address")
	fs.BoolVar(&cfg.Headless, "headless", false, "Run without UDS listener")
	fs.BoolVar(&cfg.Debug, "debug", false, "Debug mode: allow multi-instance, custom listen interface")
	fs.BoolVar(&cfg.Production, "production", false, "Production mode: enforce security constraints")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	return cfg
}

func validateConfig(cfg Config) {
	if cfg.Production {
		// Force loopback-only for P2P in production
		if cfg.ListenAddr != "127.0.0.1" && cfg.ListenAddr != "localhost" {
			log.Fatalf("production mode: P2P listen address must be 127.0.0.1, got %s", cfg.ListenAddr)
		}
		// Force single-instance (lockfile) — enforced in Run()
		log.Println("[daemon] PRODUCTION MODE — security constraints enforced")
	}

	if cfg.Debug {
		log.Println("[daemon] ⚠️  DEBUG MODE — not for production use")
	}

	if cfg.Headless && cfg.UDS != "" {
		// UDS path is irrelevant in headless mode, but don't error
	}
}

// appVersion is set at build time or defaults.
var appVersion = "v0.2.0-libp2p"
