package helperapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	appName        = "uapi-helper"
	keyringService = "uapi-helper"
)

type Config struct {
	ServerURL string `json:"server_url"`
	Email     string `json:"email"`
	Autostart bool   `json:"autostart"`
}

type Store struct {
	path string
}

func NewStore() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "uapi", "helper", "config.json")
	return &Store{path: path}, nil
}

func (s *Store) Load() (Config, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	cfg.ServerURL = strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	cfg.Email = strings.TrimSpace(cfg.Email)
	return cfg, nil
}

func (s *Store) Save(cfg Config) error {
	cfg.ServerURL = strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	cfg.Email = strings.TrimSpace(cfg.Email)
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0600)
}

func (s *Store) RefreshToken(cfg Config) (string, error) {
	return keyring.Get(keyringService, keyringUser(cfg))
}

func (s *Store) SaveRefreshToken(cfg Config, token string) error {
	return keyring.Set(keyringService, keyringUser(cfg), token)
}

func (s *Store) DeleteRefreshToken(cfg Config) {
	_ = keyring.Delete(keyringService, keyringUser(cfg))
}

func keyringUser(cfg Config) string {
	return strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/") + "|" + strings.TrimSpace(cfg.Email)
}
