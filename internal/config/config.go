package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Security SecurityConfig `yaml:"security"`
	Billing  BillingConfig  `yaml:"billing"`
	Logging  LoggingConfig  `yaml:"logging"`
	User     UserConfig     `yaml:"user"`
}

type ServerConfig struct {
	Host             string `yaml:"host"`
	Port             int    `yaml:"port"`
	MaxBodySizeMB    int    `yaml:"max_body_size_mb"`
	ConcurrencyLimit int    `yaml:"concurrency_limit"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

type SecurityConfig struct {
	JWTSecret         string `yaml:"jwt_secret"`
	EncryptionKey     string `yaml:"encryption_key"`
	AdminUsername     string `yaml:"admin_username,omitempty"`
	AdminPasswordHash string `yaml:"admin_password_hash,omitempty"`
}

type BillingConfig struct {
}

type UserConfig struct {
	JWTExpiry      string `yaml:"jwt_expiry"`        // default "24h"
	MaxKeysPerUser int    `yaml:"max_keys_per_user"`  // default 5
}

type LoggingConfig struct {
	Level         string `yaml:"level"`
	RetentionDays int    `yaml:"retention_days"`
}

// Initialized returns true if an admin password has been set (setup completed).
func (c *Config) Initialized() bool {
	return c.Security.AdminPasswordHash != ""
}

// Load reads config from path. If the file doesn't exist, it auto-generates
// a minimal config with random secrets and writes it to path.
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Auto-generate config with random secrets
			if err := generateSecrets(&cfg.Security); err != nil {
				return nil, fmt.Errorf("generate secrets: %w", err)
			}
			if err := Save(cfg, path); err != nil {
				return nil, fmt.Errorf("write auto-generated config: %w", err)
			}
		} else {
			return nil, fmt.Errorf("read config: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// Auto-generate missing secrets
	if cfg.Security.JWTSecret == "" || cfg.Security.EncryptionKey == "" {
		if err := generateSecrets(&cfg.Security); err != nil {
			return nil, fmt.Errorf("generate secrets: %w", err)
		}
		if err := Save(cfg, path); err != nil {
			return nil, fmt.Errorf("write config with generated secrets: %w", err)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Guard: MaxBodySizeMB <= 0 rejects all POST requests, default to 100
	if cfg.Server.MaxBodySizeMB <= 0 {
		cfg.Server.MaxBodySizeMB = 100
	}

	return cfg, nil
}

// Save writes the config to path as YAML.
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:          8080,
			MaxBodySizeMB: 100,
		},
		Database: DatabaseConfig{
			Port:    5432,
			User:    "relay",
			DBName:  "cli_relay",
			SSLMode: "disable",
		},
		Logging: LoggingConfig{
			Level:         "info",
			RetentionDays: 180,
		},
		User: UserConfig{
			JWTExpiry:      "24h",
			MaxKeysPerUser: 5,
		},
	}
}

func generateSecrets(sec *SecurityConfig) error {
	if sec.JWTSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		sec.JWTSecret = base64.RawStdEncoding.EncodeToString(b)
	}
	if sec.EncryptionKey == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		sec.EncryptionKey = hex.EncodeToString(b)
	}
	return nil
}

func (c *Config) validate() error {
	if len(c.Security.EncryptionKey) != 64 {
		return fmt.Errorf("security.encryption_key must be 64 hex characters (32 bytes)")
	}
	return nil
}