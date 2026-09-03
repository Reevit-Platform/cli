package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// TestAPIURLIsAcceptedAsBaseURL reproduces the failure that sent two Round 3
// test runs to the live API. The config file says api_url — the spelling the
// environment override (REEVIT_API_URL) and the client's own connection error
// both teach — and encoding/json used to drop it, leaving BaseURL empty for
// Load to fill with the production default.
func TestAPIURLIsAcceptedAsBaseURL(t *testing.T) {
	const stub = "http://127.0.0.1:9"

	// Vacuity guard: if the default ever stopped being the production API,
	// this test would pass while proving nothing.
	if DefaultBaseURL == stub || !strings.HasPrefix(DefaultBaseURL, "https://") {
		t.Fatalf("DefaultBaseURL = %q — this test assumes it is the remote production URL", DefaultBaseURL)
	}

	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	write(t, dir, `{"api_key":"pfk_test_x.sec","api_url":"`+stub+`"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.BaseURL == DefaultBaseURL {
		t.Fatalf("a config file asking for %s resolved to the production API %s — "+
			"an ignored key is indistinguishable from an absent one, so the typo "+
			"silently sends real traffic upstream", stub, DefaultBaseURL)
	}

	if cfg.BaseURL != stub {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, stub)
	}

	// The alias is a spelling of a real setting, not an unknown key.
	for _, k := range cfg.UnknownKeys {
		if k == "api_url" {
			t.Fatalf("api_url was accepted and then also reported as unrecognised: %v", cfg.UnknownKeys)
		}
	}
}

func TestExplicitBaseURLWinsOverTheAlias(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	write(t, dir, `{"base_url":"http://real","api_url":"http://alias"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.BaseURL != "http://real" {
		t.Fatalf("BaseURL = %q — the canonical key must win over the alias, "+
			"otherwise a stale api_url silently overrides the value the user meant", cfg.BaseURL)
	}
}

func TestEnvStillWinsOverTheAlias(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_MODE", "")
	t.Setenv("REEVIT_API_URL", "http://from-env")

	write(t, dir, `{"api_url":"http://from-file"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.BaseURL != "http://from-env" {
		t.Fatalf("BaseURL = %q, want the env override — accepting the alias must not "+
			"promote a file value above REEVIT_API_URL", cfg.BaseURL)
	}
}

func TestUnknownConfigKeysAreReportedAndSorted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	write(t, dir, `{"api_key":"pfk_test_x.sec","zeta":1,"apikey":"x","base_url":"http://s"}`)

	cfg, err := LoadFile()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got := strings.Join(cfg.UnknownKeys, ",")
	if got != "apikey,zeta" {
		t.Fatalf("UnknownKeys = %q, want %q — every key that is not a setting must be "+
			"reported, in a stable order (map iteration is random and this reaches a terminal)", got, "apikey,zeta")
	}
}

func TestAGoodConfigReportsNoUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REEVIT_CONFIG", filepath.Join(dir, "config.json"))

	write(t, dir, `{"api_key":"pfk_test_x.sec","base_url":"http://s","mode":"test",`+
		`"org_id":"o","org_name":"n","telemetry_id":"t"}`)

	cfg, err := LoadFile()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.UnknownKeys) != 0 {
		t.Fatalf("a config using only real settings reported %v as unrecognised — "+
			"false warnings train users to ignore the real one", cfg.UnknownKeys)
	}
}

// TestKnownKeysCoversEveryConfigField stops knownKeys drifting from the struct.
// Adding a field to Config without listing it here would make every config
// written by the new CLI warn about its own key.
func TestKnownKeysCoversEveryConfigField(t *testing.T) {
	typ := reflect.TypeOf(Config{})

	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")

		if name == "" || name == "-" {
			continue
		}

		if !knownKeys[name] {
			t.Errorf("Config field %s has json tag %q but knownKeys does not list it — "+
				"the CLI would warn about a key it wrote itself", typ.Field(i).Name, name)
		}
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
