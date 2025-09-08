// Package config provides YAML-based configuration loading for ttmesh.
package config

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/spf13/viper"
)

// Config is the root application configuration.
type Config struct {
    // AppName optional logical name of the node/application
    AppName string `mapstructure:"app_name"`

    // DataDir base directory for persistent data
    DataDir string `mapstructure:"data_dir"`

    // NodeID is the local canonical node/peer identifier used in envelope routing
    NodeID string `mapstructure:"node_id"`

    // Log holds logging configuration
    Log LogConfig `mapstructure:"log"`

    // Transports list to configure multiple inbound/outbound links
    Transports []TransportConfig `mapstructure:"transports"`

    // Identity controls node cryptographic identity used in handshake.
    Identity IdentityConfig `mapstructure:"identity"`

    // Net holds network/bootstrap options
    Net NetConfig `mapstructure:"net"`
}

// LogConfig defines logger settings.
type LogConfig struct {
    // Level: debug, info, warn, error
    Level string `mapstructure:"level"`
    // Format: console or json
    Format string `mapstructure:"format"`
    // Outputs: list of outputs: stdout, stderr, or file paths
    Outputs []string `mapstructure:"outputs"`

    // Rotation controls file rotation when writing to files
    Rotation RotationConfig `mapstructure:"rotation"`
    // Development toggles development-friendly logging options
    Development bool `mapstructure:"development"`
}

// RotationConfig controls log file rotation for file outputs.
type RotationConfig struct {
    Enable     bool `mapstructure:"enable"`
    Filename   string `mapstructure:"filename"`
    MaxSizeMB  int  `mapstructure:"max_size_mb"`
    MaxBackups int  `mapstructure:"max_backups"`
    MaxAgeDays int  `mapstructure:"max_age_days"`
    Compress   bool `mapstructure:"compress"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
    return &Config{
        AppName: "ttmesh-node",
        DataDir: "./data",
        NodeID:  "node-1",
        Log: LogConfig{
            Level:       "info",
            Format:      "console",
            Outputs:     []string{"stdout"},
            Development: true,
            Rotation: RotationConfig{
                Enable:     false,
                Filename:   "logs/ttmesh.log",
                MaxSizeMB:  50,
                MaxBackups: 3,
                MaxAgeDays: 28,
                Compress:   true,
            },
        },
        Transports: []TransportConfig{
            {
                Kind:   "udp",
                Listen: []string{":7777"},
            },
        },
        Identity: IdentityConfig{Alg: "ed25519"},
        Net:      NetConfig{DialBackoffInitialMS: 500, DialBackoffMaxMS: 30000, DialBackoffJitterMS: 100},
    }
}

// Load reads configuration from the provided path (if non-empty),
// otherwise it searches common locations and supports environment overrides.
// Environment variables use the prefix TTMESH and `.`/`-` are replaced with `_`.
// Example: TTMESH_LOG_LEVEL=debug
func Load(path string) (*Config, error) {
    cfg := Default()

    v := viper.New()
    v.SetConfigType("yaml")
    v.SetEnvPrefix("TTMESH")
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
    v.AutomaticEnv()

    // seed defaults for viper so env-only configs work
    v.SetDefault("app_name", cfg.AppName)
    v.SetDefault("data_dir", cfg.DataDir)
    v.SetDefault("node_id", cfg.NodeID)
    v.SetDefault("log.level", cfg.Log.Level)
    v.SetDefault("log.format", cfg.Log.Format)
    v.SetDefault("log.outputs", cfg.Log.Outputs)
    v.SetDefault("log.development", cfg.Log.Development)
    v.SetDefault("log.rotation.enable", cfg.Log.Rotation.Enable)
    v.SetDefault("log.rotation.filename", cfg.Log.Rotation.Filename)
    v.SetDefault("log.rotation.max_size_mb", cfg.Log.Rotation.MaxSizeMB)
    v.SetDefault("log.rotation.max_backups", cfg.Log.Rotation.MaxBackups)
    v.SetDefault("log.rotation.max_age_days", cfg.Log.Rotation.MaxAgeDays)
    v.SetDefault("log.rotation.compress", cfg.Log.Rotation.Compress)
    // Transports default
    v.SetDefault("transports", cfg.Transports)
    // Identity defaults
    v.SetDefault("identity.alg", cfg.Identity.Alg)
    v.SetDefault("identity.private_key", cfg.Identity.PrivateKey)
    v.SetDefault("identity.private_key_file", cfg.Identity.PrivateKeyFile)
    // Net defaults
    v.SetDefault("net.dial_backoff_initial_ms", cfg.Net.DialBackoffInitialMS)
    v.SetDefault("net.dial_backoff_max_ms", cfg.Net.DialBackoffMaxMS)
    v.SetDefault("net.dial_backoff_jitter_ms", cfg.Net.DialBackoffJitterMS)

    // Choose config file
    if path == "" {
        // Allow override via env var
        if envPath := os.Getenv("TTMESH_CONFIG"); envPath != "" {
            path = envPath
        }
    }

    if path != "" {
        v.SetConfigFile(path)
    } else {
        // Search common locations with base name `ttmesh`
        v.SetConfigName("ttmesh")
        v.AddConfigPath(".")
        v.AddConfigPath("./configs")
        if home, err := os.UserHomeDir(); err == nil {
            v.AddConfigPath(filepath.Join(home, ".ttmesh"))
        }
    }

    // Read config file if present; if not found, continue with defaults/env
    if err := v.ReadInConfig(); err != nil {
        var viperConfigFileNotFound viper.ConfigFileNotFoundError
        if !errors.As(err, &viperConfigFileNotFound) {
            return nil, fmt.Errorf("read config: %w", err)
        }
    }

    if err := v.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("decode config: %w", err)
    }

    if err := cfg.validate(); err != nil {
        return nil, err
    }
    return cfg, nil
}

func (c *Config) validate() error {
    lvl := strings.ToLower(strings.TrimSpace(c.Log.Level))
    switch lvl {
    case "debug", "info", "warn", "warning", "error":
        // ok
    default:
        return fmt.Errorf("invalid log.level: %q", c.Log.Level)
    }

    if c.Log.Format == "" {
        c.Log.Format = "console"
    }
    if len(c.Log.Outputs) == 0 {
        c.Log.Outputs = []string{"stdout"}
    }
    if strings.TrimSpace(c.NodeID) == "" {
        c.NodeID = "node-1"
    }
    // basic validation of transports
    for i := range c.Transports {
        c.Transports[i].Kind = strings.ToLower(strings.TrimSpace(c.Transports[i].Kind))
        // nothing else mandatory; listen/dial can be empty
    }
    return nil
}

// MustLoad is a convenience that panics on error.
func MustLoad(path string) *Config {
    cfg, err := Load(path)
    if err != nil {
        panic(err)
    }
    return cfg
}
