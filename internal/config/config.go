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
	OrgID   string `json:"org_id,omitempty"`
	OrgName string `json:"org_name,omitempty"`
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

// LoadFile returns only what is on disk — no env overlay, no defaults. A
// missing file yields the zero Config. Anything that writes the file back
// must start here rather than from Load, or it persists env-supplied values.
func LoadFile() (Config, error) {
	var cfg Config

	p, err := path()
	if err != nil {
		return cfg, err
	}

	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}

	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", p, err)
	}

	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", p, err)
	}

	return cfg, nil
}

// Load returns the effective config: file values overridden by env vars.
// A missing file is fine as long as REEVIT_API_KEY is set.
func Load() (Config, error) {
	cfg, err := LoadFile()
	if err != nil {
		return cfg, err
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

	// The key decides the mode. The backend resolves an API-key principal's
	// mode from the key itself and ignores X-Reevit-Mode (it is only read for
	// session principals), so a file or env value that disagrees is a lie the
	// CLI would otherwise print back at the user.
	if keyMode, ok := ModeFromKey(cfg.APIKey); ok {
		if cfg.Mode != "" && cfg.Mode != keyMode {
			return cfg, fmt.Errorf(
				"REEVIT_MODE is %q but the API key is a %s key — mode is determined by the key; unset REEVIT_MODE or use a matching key",
				cfg.Mode, keyMode)
		}

		cfg.Mode = keyMode
	}

	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}

	if cfg.Mode != "test" && cfg.Mode != "live" {
		return cfg, fmt.Errorf(`mode must be "test" or "live", got %q`, cfg.Mode)
	}

	return cfg, nil
}

// ModeFromKey reports the mode a Reevit API key belongs to, mirroring the rule
// the backend and the MCP server already use: pfk_live_… is live, pfk_test_…
// is test. Keys with any other shape return ok=false so non-standard or
// legacy keys keep whatever the file or environment says.
func ModeFromKey(key string) (string, bool) {
	switch {
	case strings.HasPrefix(key, "pfk_live_"):
		return "live", true
	case strings.HasPrefix(key, "pfk_test_"):
		return "test", true
	default:
		return "", false
	}
}

// SaveTelemetryID persists just the anonymous install id, leaving every other
// field as it is on disk.
//
// It deliberately re-reads the file instead of taking a Config from the
// caller: Load overlays REEVIT_API_KEY / REEVIT_API_URL / REEVIT_MODE onto
// what it returns, so saving a Load result would write an env-supplied
// credential to disk. A CI-only or shell-local key must stay ephemeral, and
// minting an analytics id is no reason to make it permanent.
func SaveTelemetryID(id string) (string, error) {
	cfg, err := LoadFile()
	if err != nil {
		return "", err
	}

	cfg.TelemetryID = id

	return Save(cfg)
}

// Save writes the config file with owner-only permissions.
//
// The Config must come from LoadFile or from an explicit assignment (as the
// login flows do) — never straight from Load, whose env overlay would be
// persisted. See SaveTelemetryID.
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
