package config

import (
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
}

type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	MaxBodySizeMB int    `yaml:"max_body_size_mb"`
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
	AdminUsername     string `yaml:"admin_username"`
	AdminPasswordHash string `yaml:"admin_password_hash"`
}

type BillingConfig struct {
	DefaultPlanID string `yaml:"default_plan_id"`
}

type LoggingConfig struct {
	Level         string `yaml:"level"`
	RetentionDays int    `yaml:"retention_days"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{
		Server: ServerConfig{
			Host:          "0.0.0.0",
			Port:          8080,
			MaxBodySizeMB: 50,
		},
		Database: DatabaseConfig{
			Host:    "localhost",
			Port:    5432,
			User:    "relay",
			DBName:  "cli_relay",
			SSLMode: "disable",
		},
		Logging: LoggingConfig{
			Level:         "info",
			RetentionDays: 180,
		},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Security.JWTSecret == "" {
		return fmt.Errorf("security.jwt_secret is required")
	}
	if c.Security.EncryptionKey == "" {
		return fmt.Errorf("security.encryption_key is required (32-byte hex)")
	}
	if len(c.Security.EncryptionKey) != 64 {
		return fmt.Errorf("security.encryption_key must be 64 hex characters (32 bytes)")
	}
	return nil
}
