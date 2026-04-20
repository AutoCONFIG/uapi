package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Database  DatabaseConfig            `yaml:"database"`
	Admin     AdminConfig               `yaml:"admin"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Refresh   RefreshConfig             `yaml:"refresh"`
	Log       LogConfig                 `yaml:"log"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Listen string `yaml:"listen"`
}

// DatabaseConfig configures the SQLite database.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// AdminConfig configures the admin authentication.
type AdminConfig struct {
	Password string `yaml:"password,omitempty"`
}

// ProviderConfig configures a single provider.
type ProviderConfig struct {
	Enabled             bool          `yaml:"enabled"`
	AuthMethod          string        `yaml:"auth_method"`
	Issuer              string        `yaml:"issuer,omitempty"`
	StoragePath         string        `yaml:"storage_path,omitempty"`
	ProactiveRefreshAge time.Duration `yaml:"proactive_refresh_age,omitempty"`
	RefreshBuffer       time.Duration `yaml:"refresh_buffer,omitempty"`
}

// RefreshConfig configures the token refresh scheduler.
type RefreshConfig struct {
	CheckInterval time.Duration `yaml:"check_interval"`
	MaxRetries    int           `yaml:"max_retries"`
	RetryBackoff  time.Duration `yaml:"retry_backoff"`
}

// LogConfig configures logging.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen: "127.0.0.1:9876",
		},
		Database: DatabaseConfig{
			Path: "cli-relay.db",
		},
		Providers: map[string]ProviderConfig{
			"codex": {
				Enabled:             true,
				AuthMethod:          "browser",
				Issuer:              "https://auth.openai.com",
				ProactiveRefreshAge: 192 * time.Hour, // 8 days
				RefreshBuffer:       5 * time.Minute,
			},
		},
		Refresh: RefreshConfig{
			CheckInterval: 1 * time.Minute,
			MaxRetries:    3,
			RetryBackoff:  30 * time.Second,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load reads config from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the config for errors.
func (c *Config) Validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	return nil
}
