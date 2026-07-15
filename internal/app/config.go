package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type DaemonConfig struct {
	DataDir string `toml:"data_dir"`
	UDS     string `toml:"uds"`
	Port    int    `toml:"port"`
	Listen  string `toml:"listen"`

	Modes   DaemonModes   `toml:"modes"`
	Logging DaemonLogging `toml:"logging"`
}

type DaemonModes struct {
	Headless   bool `toml:"headless"`
	Debug      bool `toml:"debug"`
	Production bool `toml:"production"`
}

type DaemonLogging struct {
	Level string `toml:"level"`
	File  string `toml:"file"`
}

type P2PConfig struct {
	MDNSEnabled   *bool `toml:"mdns_enabled"`
	AutoReconnect *bool `toml:"auto_reconnect"`
}

type PeersConfig struct {
	Saved []string `toml:"saved"`
}

type ConfigFile struct {
	Daemon DaemonConfig `toml:"daemon"`
	P2P    P2PConfig    `toml:"p2p"`
	Peers  PeersConfig  `toml:"peers"`
}

type Config struct {
	UDS            string
	DataDir        string
	Port           int
	ListenAddr     string
	Headless       bool
	Debug          bool
	Production     bool
	MDNSEnabled    bool
	AutoReconnect  bool
	LogLevel       string
	LogFile        string
	SavedPeers     []string
}

func DefaultConfig() Config {
	return Config{
		UDS:           "/tmp/parade.sock",
		Port:          4327,
		ListenAddr:    "127.0.0.1",
		Headless:      false,
		Debug:         false,
		Production:    false,
		MDNSEnabled:   true,
		AutoReconnect: true,
		LogLevel:      "info",
		LogFile:       "",
	}
}

func configPaths() []string {
	var paths []string

	var configDir string
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			configDir = filepath.Join(appData, "parade")
		}
	default:
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
			configDir = filepath.Join(xdgConfig, "parade")
		} else if home, err := os.UserHomeDir(); err == nil {
			configDir = filepath.Join(home, ".config", "parade")
		}
	}
	if configDir != "" {
		paths = append(paths, filepath.Join(configDir, "config.toml"))
	}

	paths = append(paths, "{data-dir}/config.toml")
	return paths
}

func Load(path string) (*ConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg ConfigFile
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadFromStandardLocations(dataDirHint string) (*ConfigFile, error) {
	for _, pathTemplate := range configPaths() {
		var path string
		if pathTemplate == "{data-dir}/config.toml" {
			dd := os.Getenv("PARADE_DATA_DIR")
			if dd == "" {
				dd = dataDirHint
			}
			if dd == "" {
				continue
			}
			path = filepath.Join(dd, "config.toml")
		} else {
			path = pathTemplate
		}

		cfg, err := Load(path)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			return cfg, nil
		}
	}
	return nil, nil
}

func ApplyEnvOverrides(cfg *Config) {
	if v := os.Getenv("PARADE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("PARADE_UDS"); v != "" {
		cfg.UDS = v
	}
	if v := os.Getenv("PARADE_PORT"); v != "" {
		var port int
		if _, err := parsePositiveInt(v, &port); err == nil {
			cfg.Port = port
		}
	}
	if v := os.Getenv("PARADE_LISTEN"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("PARADE_HEADLESS"); v != "" {
		cfg.Headless = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("PARADE_DEBUG"); v != "" {
		cfg.Debug = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("PARADE_PRODUCTION"); v != "" {
		cfg.Production = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("PARADE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("PARADE_MDNS_ENABLED"); v != "" {
		cfg.MDNSEnabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("PARADE_AUTO_RECONNECT"); v != "" {
		cfg.AutoReconnect = strings.ToLower(v) == "true" || v == "1"
	}
}

func FromConfigFile(cfgFile *ConfigFile) Config {
	cfg := DefaultConfig()
	if cfgFile == nil {
		return cfg
	}

	d := cfgFile.Daemon
	if d.DataDir != "" {
		cfg.DataDir = d.DataDir
	}
	if d.UDS != "" {
		cfg.UDS = d.UDS
	}
	if d.Port != 0 {
		cfg.Port = d.Port
	}
	if d.Listen != "" {
		cfg.ListenAddr = d.Listen
	}

	if d.Modes.Headless {
		cfg.Headless = true
	}
	if d.Modes.Debug {
		cfg.Debug = true
	}
	if d.Modes.Production {
		cfg.Production = true
	}

	if d.Logging.Level != "" {
		cfg.LogLevel = d.Logging.Level
	}
	if d.Logging.File != "" {
		cfg.LogFile = d.Logging.File
	}

	if cfgFile.P2P.MDNSEnabled != nil {
		cfg.MDNSEnabled = *cfgFile.P2P.MDNSEnabled
	}
	if cfgFile.P2P.AutoReconnect != nil {
		cfg.AutoReconnect = *cfgFile.P2P.AutoReconnect
	}

	if cfgFile.Peers.Saved != nil {
		cfg.SavedPeers = cfgFile.Peers.Saved
	}

	return cfg
}

func MergeWithCLI(cfg Config, cli *DaemonCLIConfig) Config {
	if cli.UDS != "" {
		cfg.UDS = cli.UDS
	}
	if cli.DataDir != "" {
		cfg.DataDir = cli.DataDir
	}
	if cli.Port != 0 {
		cfg.Port = cli.Port
	}
	if cli.ListenAddr != "" {
		cfg.ListenAddr = cli.ListenAddr
	}
	if cli.HeadlessSet {
		cfg.Headless = cli.Headless
	}
	if cli.DebugSet {
		cfg.Debug = cli.Debug
	}
	if cli.ProductionSet {
		cfg.Production = cli.Production
	}
	if cli.MDNSEnabledSet {
		cfg.MDNSEnabled = cli.MDNSEnabled
	}
	return cfg
}

type DaemonCLIConfig struct {
	UDS           string
	DataDir       string
	Port          int
	ListenAddr    string
	Headless      bool
	HeadlessSet   bool
	Debug         bool
	DebugSet      bool
	Production    bool
	ProductionSet bool
	MDNSEnabled   bool
	MDNSEnabledSet bool
}

func parsePositiveInt(s string, result *int) (bool, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, nil
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return false, nil
	}
	*result = n
	return true, nil
}
