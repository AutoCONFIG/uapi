package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Security SecurityConfig `yaml:"security"`
	Auth     AuthConfig     `yaml:"auth"`
	Gateway  GatewayConfig  `yaml:"gateway"`
	Billing  BillingConfig  `yaml:"billing"`
	Logging  LoggingConfig  `yaml:"logging"`
	User     UserConfig     `yaml:"user"`
	WS       WSServerConfig `yaml:"ws"`
}

type ServerConfig struct {
	Mode             string `yaml:"mode"`
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
	JWTSecret         string   `yaml:"jwt_secret"`
	EncryptionKey     string   `yaml:"encryption_key"`
	AdminUsername     string   `yaml:"admin_username,omitempty"`
	AdminPasswordHash string   `yaml:"admin_password_hash,omitempty"`
	TrustedProxies    []string `yaml:"trusted_proxies"` // IPs that are allowed to set X-Forwarded-For / X-Real-IP
}

type AuthConfig struct {
	AccessTokenExpiry  string `yaml:"access_token_expiry"`  // default "15m"
	RefreshTokenExpiry string `yaml:"refresh_token_expiry"` // default "720h"
}

type GatewayConfig struct {
	InternalSecret     string `yaml:"internal_secret"`
	CacheTTL           string `yaml:"cache_ttl"`
	GatewayID          string `yaml:"gateway_id"`
	RequireInternal    bool   `yaml:"require_internal"`
	ControlURL         string `yaml:"control_url"`
	RelayNodeID        string `yaml:"relay_node_id"`
	ConfigPullInterval string `yaml:"config_pull_interval"`
}

type BillingConfig struct {
}

type WSServerConfig struct {
	Host                     string   `yaml:"host"`
	Port                     int      `yaml:"port"`
	MaxMessageSizeMB         int      `yaml:"max_message_size_mb"`
	PoolIdleTimeoutSeconds   int      `yaml:"pool_idle_timeout_seconds"`
	PoolMaxConnLifetime      int      `yaml:"pool_max_conn_lifetime"`
	PoolMaxTotalConns        int      `yaml:"pool_max_total_conns"`
	PoolMaxIdlePerKey        int      `yaml:"pool_max_idle_per_key"`
	StreamIdleTimeoutSeconds int      `yaml:"stream_idle_timeout_seconds"`
	MaxConnections           int      `yaml:"max_connections"`
	AllowedOrigins           []string `yaml:"allowed_origins"`
}

type UserConfig struct {
	MaxKeysPerUser int `yaml:"max_keys_per_user"` // default 1
}

type LoggingConfig struct {
	Level                   string `yaml:"level"`
	RetentionDays           int    `yaml:"retention_days"`
	RedeemCodeRetentionDays int    `yaml:"redeem_code_retention_days"`
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
			if err := generateGatewaySecret(&cfg.Gateway); err != nil {
				return nil, fmt.Errorf("generate gateway secret: %w", err)
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
	if cfg.Security.JWTSecret == "" || cfg.Security.EncryptionKey == "" || cfg.Gateway.InternalSecret == "" {
		if err := generateSecrets(&cfg.Security); err != nil {
			return nil, fmt.Errorf("generate secrets: %w", err)
		}
		if err := generateGatewaySecret(&cfg.Gateway); err != nil {
			return nil, fmt.Errorf("generate gateway secret: %w", err)
		}
		if err := Save(cfg, path); err != nil {
			return nil, fmt.Errorf("write config with generated secrets: %w", err)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Guard: MaxBodySizeMB <= 0 rejects all POST requests, default to 256.
	if cfg.Server.MaxBodySizeMB <= 0 {
		cfg.Server.MaxBodySizeMB = 256
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
			Mode:          "all",
			Port:          8080,
			MaxBodySizeMB: 256,
		},
		Gateway: GatewayConfig{
			CacheTTL:           "5s",
			GatewayID:          "default",
			ConfigPullInterval: "5s",
		},
		WS: WSServerConfig{
			MaxMessageSizeMB: 256,
		},
		Database: DatabaseConfig{
			Port:    5432,
			User:    "uapi",
			DBName:  "uapi",
			SSLMode: "disable",
		},
		Logging: LoggingConfig{
			Level:                   "info",
			RetentionDays:           180,
			RedeemCodeRetentionDays: 180,
		},
		Auth: AuthConfig{
			AccessTokenExpiry:  "15m",
			RefreshTokenExpiry: "720h",
		},
		User: UserConfig{
			MaxKeysPerUser: 1,
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

func generateGatewaySecret(gw *GatewayConfig) error {
	if gw.InternalSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		gw.InternalSecret = base64.RawStdEncoding.EncodeToString(b)
	}
	return nil
}

func (c *Config) validate() error {
	switch c.Server.Mode {
	case "", "all":
		c.Server.Mode = "all"
	case "gateway", "relay":
	default:
		return fmt.Errorf("server.mode must be one of all, gateway, relay")
	}
	if len(c.Security.EncryptionKey) != 64 {
		return fmt.Errorf("security.encryption_key must be 64 hex characters (32 bytes)")
	}
	if _, err := hex.DecodeString(c.Security.EncryptionKey); err != nil {
		return fmt.Errorf("security.encryption_key must be valid hex")
	}
	if isWeakPlaceholderSecret(c.Security.EncryptionKey) {
		return fmt.Errorf("security.encryption_key must not be a placeholder or weak test value")
	}
	if len(strings.TrimSpace(c.Security.JWTSecret)) < 32 {
		return fmt.Errorf("security.jwt_secret must be at least 32 characters")
	}
	if isPlaceholderValue(c.Security.JWTSecret) || isWeakPlaceholderSecret(c.Security.JWTSecret) {
		return fmt.Errorf("security.jwt_secret must not be a placeholder or weak test value")
	}
	if c.Gateway.InternalSecret == "" {
		return fmt.Errorf("gateway.internal_secret must be set")
	}
	if len(strings.TrimSpace(c.Gateway.InternalSecret)) < 32 {
		return fmt.Errorf("gateway.internal_secret must be at least 32 characters")
	}
	if isPlaceholderValue(c.Gateway.InternalSecret) {
		return fmt.Errorf("gateway.internal_secret must not be a placeholder")
	}
	if isWeakPlaceholderSecret(c.Gateway.InternalSecret) {
		return fmt.Errorf("gateway.internal_secret must not be a placeholder or weak test value")
	}
	if c.Gateway.CacheTTL == "" {
		c.Gateway.CacheTTL = "5s"
	}
	if c.Gateway.GatewayID == "" {
		c.Gateway.GatewayID = "default"
	}
	if c.Gateway.ConfigPullInterval == "" {
		c.Gateway.ConfigPullInterval = "5s"
	}
	if c.Server.Mode == "relay" {
		if !c.Gateway.RequireInternal {
			return fmt.Errorf("gateway.require_internal must be true when server.mode is relay")
		}
		if c.Gateway.ControlURL == "" {
			return fmt.Errorf("gateway.control_url must be set when server.mode is relay")
		}
		if c.Gateway.RelayNodeID == "" {
			return fmt.Errorf("gateway.relay_node_id must be set when server.mode is relay")
		}
		if isPlaceholderValue(c.Gateway.RelayNodeID) {
			return fmt.Errorf("gateway.relay_node_id must not be a placeholder")
		}
		relayNodeID, err := uuid.Parse(c.Gateway.RelayNodeID)
		if err != nil || relayNodeID == uuid.Nil {
			return fmt.Errorf("gateway.relay_node_id must be a non-empty UUID when server.mode is relay")
		}
		if isRepeatedUUID(c.Gateway.RelayNodeID) {
			return fmt.Errorf("gateway.relay_node_id must not be a placeholder UUID")
		}
	}
	return nil
}

func isPlaceholderValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" ||
		strings.Contains(value, "change-me") ||
		strings.Contains(value, "replace-with") ||
		strings.Contains(value, "same-as") ||
		strings.Contains(value, "same-64") ||
		strings.Contains(value, "0123456789abcdef")
}

func isWeakPlaceholderSecret(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	allSame := true
	for _, r := range value {
		if r != rune(value[0]) {
			allSame = false
			break
		}
	}
	return allSame
}

func isRepeatedUUID(value string) bool {
	compact := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "")
	if compact == "" {
		return true
	}
	first := compact[0]
	for i := 1; i < len(compact); i++ {
		if compact[i] != first {
			return false
		}
	}
	return true
}
