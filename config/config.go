package config

import (
	"encoding/json"
	"os"
	"time"
)

type Config struct {
	Gateway GatewayConfig `json:"gateway"`
	Routes  []RouteConfig `json:"routes"`
	Buffer  BufferConfig  `json:"buffer"`
	Metrics MetricsConfig `json:"metrics"`
	Memory  MemoryConfig  `json:"memory"`
}

type GatewayConfig struct {
	Listen            string          `json:"listen"`
	MaxConnections    int             `json:"maxConnections"`
	ConnectionTimeout time.Duration   `json:"connectionTimeout"`
	HeartbeatInterval time.Duration   `json:"heartbeatInterval"`
	MaxMessageSize    int64           `json:"maxMessageSize"`
	ReadBufferSize    int             `json:"readBufferSize"`
	WriteBufferSize   int             `json:"writeBufferSize"`
	RateLimit         RateLimitConfig `json:"rateLimit"`
	Security          SecurityConfig  `json:"security"`
	History           HistoryConfig   `json:"history"`
}

type HistoryConfig struct {
	Enabled      bool `json:"enabled"`
	MaxPerClient int  `json:"maxPerClient"`
	MaxMsgSize   int  `json:"maxMsgSizeKB"`
	CleanupHours int  `json:"cleanupHours"`
}

type SecurityConfig struct {
	AllowedOrigins      []string `json:"allowedOrigins"`
	EnableCompression   bool     `json:"enableCompression"`
	MaxControlFrameSize int      `json:"maxControlFrameSize"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerSecond int  `json:"requestsPerSecond"`
	BurstSize         int  `json:"burstSize"`
}

type RouteConfig struct {
	ClientIdPrefix  string `json:"clientIdPrefix"`
	OpenclawGateway string `json:"openclawGateway"`
}

type BufferConfig struct {
	Size        int    `json:"size"`
	PersistFile string `json:"persistFile"`
}

type MetricsConfig struct {
	Enabled     bool          `json:"enabled"`
	LogInterval time.Duration `json:"logInterval"`
}

type MemoryConfig struct {
	MaxMemoryMB   int           `json:"maxMemoryMB"`
	GCInterval    time.Duration `json:"gcInterval"`
	HighWatermark float64       `json:"highWatermark"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.Gateway.ConnectionTimeout == 0 {
		cfg.Gateway.ConnectionTimeout = 90 * time.Second
	}
	if cfg.Gateway.HeartbeatInterval == 0 {
		cfg.Gateway.HeartbeatInterval = 15 * time.Second
	}
	if cfg.Gateway.MaxMessageSize == 0 {
		cfg.Gateway.MaxMessageSize = 50 * 1024 * 1024 // 50MB
	}
	if cfg.Gateway.ReadBufferSize == 0 {
		cfg.Gateway.ReadBufferSize = 64 * 1024 // 64KB
	}
	if cfg.Gateway.WriteBufferSize == 0 {
		cfg.Gateway.WriteBufferSize = 64 * 1024 // 64KB
	}
	if cfg.Gateway.RateLimit.RequestsPerSecond == 0 {
		cfg.Gateway.RateLimit.RequestsPerSecond = 1000
	}
	if cfg.Gateway.RateLimit.BurstSize == 0 {
		cfg.Gateway.RateLimit.BurstSize = 2000
	}
	if cfg.Gateway.Security.MaxControlFrameSize == 0 {
		cfg.Gateway.Security.MaxControlFrameSize = 125
	}
	if cfg.Gateway.History.MaxPerClient == 0 {
		cfg.Gateway.History.MaxPerClient = 500
	}
	if cfg.Gateway.History.MaxMsgSize == 0 {
		cfg.Gateway.History.MaxMsgSize = 64
	}
	if cfg.Gateway.History.CleanupHours == 0 {
		cfg.Gateway.History.CleanupHours = 24
	}
	if cfg.Buffer.Size == 0 {
		cfg.Buffer.Size = 1000000
	}
	if cfg.Metrics.LogInterval == 0 {
		cfg.Metrics.LogInterval = 10 * time.Second
	}
	if cfg.Memory.MaxMemoryMB == 0 {
		cfg.Memory.MaxMemoryMB = 2048 // 2GB default
	}
	if cfg.Memory.GCInterval == 0 {
		cfg.Memory.GCInterval = 30 * time.Second
	}
	if cfg.Memory.HighWatermark == 0 {
		cfg.Memory.HighWatermark = 0.8
	}

	return &cfg, nil
}
