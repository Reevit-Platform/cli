package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSaveRoundtripWithEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	if _, err := Save(Config{APIKey: "rk_file", Mode: "test"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, _ := os.Stat(filepath.Join(dir, "config.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config perms = %v, want 0600", info.Mode().Perm())
	}

	cfg, err := Load()
	if err != nil || cfg.APIKey != "rk_file" || cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("load = %+v, %v", cfg, err)
	}

	// Env wins over file.
	t.Setenv("REEVIT_API_KEY", "rk_env")
	t.Setenv("REEVIT_MODE", "live")

	cfg, err = Load()
	if err != nil || cfg.APIKey != "rk_env" || cfg.Mode != "live" {
		t.Fatalf("env override = %+v, %v", cfg, err)
	}
}

func TestLoadRejectsBadMode(t *testing.T) {
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "rk")
	t.Setenv("REEVIT_MODE", "prod")

	if _, err := Load(); err == nil {
		t.Fatal("expected bad-mode error")
	}
}
func TestSaveTelemetryIDNeverPersistsEnvCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REEVIT_CONFIG", p)
	t.Setenv("REEVIT_API_KEY", "pfk_live_from_env.secret")
	t.Setenv("REEVIT_API_URL", "https://staging.example.com")
	t.Setenv("REEVIT_MODE", "live")

	if _, err := SaveTelemetryID("telemetry-uuid"); err != nil {
		t.Fatalf("SaveTelemetryID: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if strings.Contains(string(raw), "pfk_live_from_env") {
		t.Fatalf("env API key was written to disk: %s", raw)
	}

	if strings.Contains(string(raw), "staging.example.com") {
		t.Fatalf("env base URL was written to disk: %s", raw)
	}

	var onDisk Config
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if onDisk.TelemetryID != "telemetry-uuid" {
		t.Fatalf("telemetry id = %q, want %q", onDisk.TelemetryID, "telemetry-uuid")
	}

	if onDisk.APIKey != "" || onDisk.Mode != "" || onDisk.BaseURL != "" {
		t.Fatalf("expected only the telemetry id on disk, got %+v", onDisk)
	}
}

// The id must land alongside real saved credentials without clobbering them —
// the common case, where the user logged in first and telemetry runs later.
func TestSaveTelemetryIDPreservesExistingFileValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REEVIT_CONFIG", p)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	if _, err := Save(Config{APIKey: "pfk_test_saved.secret", Mode: "test", OrgID: "org_1"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	if _, err := SaveTelemetryID("telemetry-uuid"); err != nil {
		t.Fatalf("SaveTelemetryID: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.APIKey != "pfk_test_saved.secret" || cfg.OrgID != "org_1" {
		t.Fatalf("existing values clobbered: %+v", cfg)
	}

	if cfg.TelemetryID != "telemetry-uuid" {
		t.Fatalf("telemetry id = %q", cfg.TelemetryID)
	}
}

// LoadFile is what every writer must start from: it must not see the env
// overlay at all, or a one-off credential is persisted forever.
func TestLoadFileIgnoresTheEnvironment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REEVIT_CONFIG", p)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	if _, err := Save(Config{APIKey: "pfk_test_file.sec", Mode: "test"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	t.Setenv("REEVIT_API_KEY", "pfk_live_env.sec")
	t.Setenv("REEVIT_API_URL", "https://staging.example.com")

	cfg, err := LoadFile()
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if cfg.APIKey != "pfk_test_file.sec" || cfg.BaseURL != "" {
		t.Fatalf("LoadFile = %+v, want only the file's values and no default base URL", cfg)
	}
}

func TestModeFromKey(t *testing.T) {
	cases := []struct {
		key      string
		wantMode string
		wantOK   bool
	}{
		{"pfk_live_abc.sec", "live", true},
		{"pfk_test_abc.sec", "test", true},
		{"pfk_live", "", false},
		{"rk_legacy", "", false},
		{"", "", false},
	}

	for _, c := range cases {
		mode, ok := ModeFromKey(c.key)
		if mode != c.wantMode || ok != c.wantOK {
			t.Errorf("ModeFromKey(%q) = %q, %v; want %q, %v", c.key, mode, ok, c.wantMode, c.wantOK)
		}
	}
}

// The backend resolves an API key's mode from the key, so a REEVIT_MODE that
// contradicts it is a lie the CLI must refuse rather than print back.
func TestLoadRejectsModeThatContradictsTheKey(t *testing.T) {
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_live_abc.sec")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "test")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "determined by the key") {
		t.Fatalf("err = %v, want the mode-mismatch error", err)
	}
}

func TestLoadDerivesModeFromTheKey(t *testing.T) {
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_live_abc.sec")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	cfg, err := Load()
	if err != nil || cfg.Mode != "live" {
		t.Fatalf("Load = %+v, %v; want live mode", cfg, err)
	}
}

// A key with a non-standard prefix keeps the legacy env/file/default path, so
// self-hosted or older key shapes do not suddenly break.
func TestLoadKeepsLegacyModeForUnrecognisedKeys(t *testing.T) {
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "rk_legacy")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "live")

	cfg, err := Load()
	if err != nil || cfg.Mode != "live" {
		t.Fatalf("Load = %+v, %v; want live mode from the environment", cfg, err)
	}
}
