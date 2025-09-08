package config

// NetConfig contains networking tuning options.
type NetConfig struct {
    DialBackoffInitialMS int `mapstructure:"dial_backoff_initial_ms"`
    DialBackoffMaxMS     int `mapstructure:"dial_backoff_max_ms"`
    DialBackoffJitterMS  int `mapstructure:"dial_backoff_jitter_ms"`
}

