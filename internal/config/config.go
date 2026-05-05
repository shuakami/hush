// Package config loads and validates Hush configuration from environment
// variables, config files, and CLI flags.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ServerConfig describes how to start a Hush server.
type ServerConfig struct {
	Listen   string        // e.g. "127.0.0.1:8443"
	DataDir  string        // SQLite + state lives here
	KEKKind  string        // "env" | "file"
	KEKValue string        // env var name or file path (depending on KEKKind)
	BootKey  string        // initial admin API key (only used on first boot)
	Timeout  time.Duration // default request timeout
}

// ClientConfig describes how a CLI / SDK reaches a Hush server.
type ClientConfig struct {
	Endpoint string // e.g. "http://127.0.0.1:8443"
	APIKey   string
	Timeout  time.Duration
}

// LoadServer pulls server config from env. Defaults are friendly enough that
// running `hush server` with no args works on a fresh box.
func LoadServer() (*ServerConfig, error) {
	cfg := &ServerConfig{
		Listen:  envOr("HUSH_LISTEN", "127.0.0.1:8443"),
		DataDir: envOr("HUSH_DATA_DIR", defaultDataDir()),
		KEKKind: envOr("HUSH_KEK_KIND", "env"),
		Timeout: 60 * time.Second,
	}

	switch cfg.KEKKind {
	case "env":
		cfg.KEKValue = envOr("HUSH_KEK_ENV", "HUSH_MASTER_KEY")
	case "file":
		cfg.KEKValue = envOr("HUSH_KEK_FILE", filepath.Join(cfg.DataDir, "master.key"))
	default:
		return nil, fmt.Errorf("unknown HUSH_KEK_KIND %q (want env|file)", cfg.KEKKind)
	}

	cfg.BootKey = strings.TrimSpace(os.Getenv("HUSH_BOOTSTRAP_API_KEY"))

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return cfg, nil
}

// LoadClient resolves an endpoint + API key for CLI / SDK usage. Lookup order:
//  1. explicit args (caller fills before calling Validate)
//  2. env: HUSH_ENDPOINT / HUSH_API_KEY
//  3. ~/.hush/config (TOML-lite, super simple)
func LoadClient() *ClientConfig {
	cfg := &ClientConfig{
		Endpoint: os.Getenv("HUSH_ENDPOINT"),
		APIKey:   os.Getenv("HUSH_API_KEY"),
		Timeout:  60 * time.Second,
	}
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		_ = readUserConfig(cfg)
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://127.0.0.1:8443"
	}
	return cfg
}

// Validate ensures the client config is usable. APIKey is allowed to be empty
// for commands that don't need it (e.g. `hush login`).
func (c *ClientConfig) Validate(needsAuth bool) error {
	if c.Endpoint == "" {
		return errors.New("endpoint not set; run `hush login` or set HUSH_ENDPOINT")
	}
	if needsAuth && c.APIKey == "" {
		return errors.New("api key not set; run `hush login` or set HUSH_API_KEY")
	}
	return nil
}

// SaveClient writes endpoint + api key to ~/.hush/config.
func SaveClient(cfg *ClientConfig) error {
	dir, err := userConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config")
	body := fmt.Sprintf("endpoint = %q\napi_key = %q\n", cfg.Endpoint, cfg.APIKey)
	return os.WriteFile(path, []byte(body), 0o600)
}

func readUserConfig(cfg *ClientConfig) error {
	dir, err := userConfigDir()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"`)
		switch k {
		case "endpoint":
			if cfg.Endpoint == "" {
				cfg.Endpoint = v
			}
		case "api_key":
			if cfg.APIKey == "" {
				cfg.APIKey = v
			}
		}
	}
	return nil
}

func userConfigDir() (string, error) {
	if v := os.Getenv("HUSH_CONFIG_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hush"), nil
}

func defaultDataDir() string {
	if v := os.Getenv("HUSH_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "./hush-data"
	}
	return filepath.Join(home, ".hush", "data")
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
