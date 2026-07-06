package config

import (
	"os"
	"path/filepath"
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
