// Package config stores the CLI's credentials and defaults in
// ~/.config/reevit/config.json (0600). Environment variables override the
// file so CI and one-off invocations never need to write to disk.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
	Mode    string `json:"mode,omitempty"`
	// OrgID is recorded by the browser login flow; SDK constructors need it
	// alongside the key, so `reevit init` writes it into project env files.
	OrgID string `json:"org_id,omitempty"`
	// TelemetryID is a random per-install UUID for anonymous usage events —
	// minted on first use alongside the one-time notice, never derived from
	// hardware or user identifiers. See internal/telemetry.
	TelemetryID string `json:"telemetry_id,omitempty"`
}

const (
	DefaultBaseURL = "https://api.reevit.io"
	DefaultMode    = "test"
)

func path() (string, error) {
	if custom := os.Getenv("REEVIT_CONFIG"); custom != "" {
		return custom, nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}

	return filepath.Join(dir, "reevit", "config.json"), nil
}

// Load returns the effective config: file values overridden by env vars.
// A missing file is fine as long as REEVIT_API_KEY is set.
func Load() (Config, error) {
	var cfg Config

	p, err := path()
	if err != nil {
		return cfg, err
	}

	if raw, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", p, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("read %s: %w", p, err)
	}

	if v := strings.TrimSpace(os.Getenv("REEVIT_API_KEY")); v != "" {
		cfg.APIKey = v
	}

	if v := strings.TrimSpace(os.Getenv("REEVIT_API_URL")); v != "" {
		cfg.BaseURL = v
	}

	if v := strings.TrimSpace(os.Getenv("REEVIT_MODE")); v != "" {
		cfg.Mode = v
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}

	if cfg.Mode != "test" && cfg.Mode != "live" {
		return cfg, fmt.Errorf(`mode must be "test" or "live", got %q`, cfg.Mode)
	}

	return cfg, nil
}

// Save writes the config file with owner-only permissions.
func Save(cfg Config) (string, error) {
	p, err := path()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}

	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}

	return p, nil
}
