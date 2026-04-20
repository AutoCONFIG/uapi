package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/provider"

	_ "modernc.org/sqlite"
)

// DB wraps an SQLite database for cli-relay persistence.
type DB struct {
	db *sql.DB
	mu sync.Mutex
}

// Open creates or opens a SQLite database at the given path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return d, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS tokens (
			provider    TEXT PRIMARY KEY,
			data        TEXT NOT NULL,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS admin (
			id          INTEGER PRIMARY KEY CHECK (id = 1),
			password    TEXT NOT NULL,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS settings (
			key         TEXT PRIMARY KEY,
			value       TEXT NOT NULL,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// --- TokenStore implementation ---

// Load reads the stored tokens for a provider.
func (d *DB) Load(_ context.Context, providerName string) (*provider.TokenSet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var data string
	err := d.db.QueryRow("SELECT data FROM tokens WHERE provider = ?", providerName).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query token: %w", err)
	}

	var ts provider.TokenSet
	if err := json.Unmarshal([]byte(data), &ts); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}
	return &ts, nil
}

// Save persists tokens for a provider.
func (d *DB) Save(_ context.Context, providerName string, tokens *provider.TokenSet) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	data, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}

	_, err = d.db.Exec(
		"INSERT OR REPLACE INTO tokens (provider, data, updated_at) VALUES (?, ?, ?)",
		providerName, string(data), time.Now().UTC(),
	)
	return err
}

// Delete removes stored tokens for a provider.
func (d *DB) Delete(_ context.Context, providerName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec("DELETE FROM tokens WHERE provider = ?", providerName)
	return err
}

// ListProviders returns all provider names that have stored tokens.
func (d *DB) ListProviders() ([]string, error) {
	rows, err := d.db.Query("SELECT provider FROM tokens")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// --- Admin password ---

// SetAdminPassword sets the admin password hash.
func (d *DB) SetAdminPassword(hash string) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO admin (id, password, updated_at) VALUES (1, ?, ?)",
		hash, time.Now().UTC(),
	)
	return err
}

// GetAdminPassword returns the stored admin password hash, or empty string if none.
func (d *DB) GetAdminPassword() (string, error) {
	var hash string
	err := d.db.QueryRow("SELECT password FROM admin WHERE id = 1").Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

// HasAdmin returns true if an admin password has been set.
func (d *DB) HasAdmin() (bool, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM admin WHERE id = 1").Scan(&count)
	return count > 0, err
}

// --- Settings ---

// GetSetting returns a setting value by key.
func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting sets a setting value.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
		key, value, time.Now().UTC(),
	)
	return err
}
