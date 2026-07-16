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

type Config struct {
	DataDir     string
	Port        int
	ListenAddr  string
	Headless    bool
	Debug       bool
	Production  bool
	MDNSEnabled bool
}

func Run(args []string) {
	cfg := loadConfig(args)

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

	// Create IPC server object early — needed for the ui Frontend that gets
	// passed to NewApp below. The actual listener is started AFTER all engines
	// are initialized and JSON-RPC methods are registered.
	var ui app.Frontend
	var ipcSrv app.IPCServer

	if !cfg.Headless {
		ipcSrv = app.NewIPCServer()
		ui = app.NewStdioFrontend(ipcSrv.Hub())
	} else {
		log.Println("[daemon] headless mode — no IPC")
		ui = &app.NullUI{}
	}

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

	identityPath := filepath.Join(dataDir, ".parade_identity")
	appInstance := app.NewApp(eventBus, cry, database, netEngine, fileEngine, ui, logBroker).
		WithIdentityPath(identityPath).
		WithMDNSEnabled(cfg.MDNSEnabled).
		WithNetworkPort(cfg.Port).
		WithNetworkListenAddr(cfg.ListenAddr)
	_ = appInstance.LoadPeersConfig(dataDir)
	appInstance.Startup()

	if ipcSrv != nil {
		app.RegisterMethods(appInstance)
	}

	// Start IPC listener now that all methods are registered.
	// Clients connecting from this point forward will always find
	// the method table fully populated.
	if ipcSrv != nil {
		if err := ipcSrv.Start(); err != nil {
			log.Fatalf("failed to start IPC listener: %v", err)
		}
		defer ipcSrv.Stop()
		log.Printf("[daemon] IPC started on stdio")
	}

	log.Printf("[daemon] Parade %s started (pid=%d, data=%s, p2p=%s:%d, headless=%v, debug=%v, production=%v, mdns=%v)",
		appVersion, os.Getpid(), dataDir, cfg.ListenAddr, cfg.Port, cfg.Headless, cfg.Debug, cfg.Production, cfg.MDNSEnabled)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("[daemon] received signal %v, shutting down...", sig)

	appInstance.Shutdown()
	log.Println("[daemon] Parade stopped")
}

func loadConfig(args []string) Config {
	cliCfg, err := parseFlagsWithTracking(args)
	if err != nil {
		os.Exit(1)
	}

	cfgFile, err := app.LoadFromStandardLocations("")
	if err != nil {
		log.Fatalf("failed to load config file: %v", err)
	}

	cfg := app.FromConfigFile(cfgFile)
	app.ApplyEnvOverrides(&cfg)
	cfg = app.MergeWithCLI(cfg, cliCfg)

	finalCfg := Config{
		DataDir:     cfg.DataDir,
		Port:        cfg.Port,
		ListenAddr:  cfg.ListenAddr,
		Headless:    cfg.Headless,
		Debug:       cfg.Debug,
		Production:  cfg.Production,
		MDNSEnabled: cfg.MDNSEnabled,
	}

	validateConfig(finalCfg)
	return finalCfg
}

func parseFlagsWithTracking(args []string) (*app.DaemonCLIConfig, error) {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)

	cliCfg := &app.DaemonCLIConfig{}

	fs.StringVar(&cliCfg.DataDir, "data-dir", "", "Data directory (default: current directory)")
	fs.IntVar(&cliCfg.Port, "port", 0, "P2P listen port")
	fs.StringVar(&cliCfg.ListenAddr, "listen", "", "P2P listen address")
	fs.BoolVar(&cliCfg.Headless, "headless", false, "Run without IPC (automation/testing)")
	fs.BoolVar(&cliCfg.Debug, "debug", false, "Debug mode: allow multi-instance, custom listen interface")
	fs.BoolVar(&cliCfg.Production, "production", false, "Production mode: enforce security constraints")
	fs.BoolVar(&cliCfg.MDNSEnabled, "mdns", false, "Enable mDNS peer discovery (default: enabled)")
	fs.BoolVar(&cliCfg.MDNSEnabled, "no-mdns", false, "Disable mDNS peer discovery")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cliCfg.HeadlessSet = wasFlagSet(args, "headless")
	cliCfg.DebugSet = wasFlagSet(args, "debug")
	cliCfg.ProductionSet = wasFlagSet(args, "production")
	cliCfg.MDNSEnabledSet = wasFlagSet(args, "mdns") || wasFlagSet(args, "no-mdns")

	if wasFlagSet(args, "no-mdns") {
		cliCfg.MDNSEnabled = false
	}

	return cliCfg, nil
}

func wasFlagSet(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--"+name || arg == "-"+string(name[0]) {
			return true
		}
		if len(arg) > 2 && arg[:2] == "--"+name+"=" {
			return true
		}
	}
	return false
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

}

// appVersion is set at build time or defaults.
var appVersion = "v0.2.0-libp2p"
