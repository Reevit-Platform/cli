package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Reevit-Platform/cli/internal/config"
	"time"
)

func TestEnabledRespectsOptOuts(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")

	if !Enabled() {
		t.Fatal("telemetry must default to enabled")
	}

	t.Setenv("DO_NOT_TRACK", "1")

	if Enabled() {
		t.Fatal("DO_NOT_TRACK=1 must disable telemetry")
	}

	t.Setenv("DO_NOT_TRACK", "")

	for _, v := range []string{"0", "false", "off", "OFF"} {
		t.Setenv("REEVIT_TELEMETRY", v)

		if Enabled() {
			t.Fatalf("REEVIT_TELEMETRY=%s must disable telemetry", v)
		}
	}
}

func TestReportSendsAnEventPerRunWithAStableMachineID(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies [][]byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)

		mu.Lock()
		bodies = append(bodies, raw)
		mu.Unlock()

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_URL", server.URL)

	SetContext("nextjs", []string{"webhook", "checkout"})

	Report("init", "0.3.0", true, 42*time.Millisecond)

	if len(bodies) != 1 {
		t.Fatalf("events sent = %d, want 1", len(bodies))
	}

	var event map[string]any

	_ = json.Unmarshal(bodies[0], &event)

	if event["command"] != "init" || event["stack"] != "nextjs" || event["machine_id"] == "" {
		t.Errorf("event = %v", event)
	}

	// Second run: same machine id.
	Report("doctor", "0.3.0", false, time.Millisecond)

	if len(bodies) != 2 {
		t.Fatalf("events sent = %d, want 2", len(bodies))
	}

	var second map[string]any

	_ = json.Unmarshal(bodies[1], &second)

	if second["machine_id"] != event["machine_id"] {
		t.Error("machine id must be stable across runs")
	}

	if second["success"] != false {
		t.Error("failure must be reported as success=false")
	}
}

func TestReportSkipsUntrackedAndOptedOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be sent")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")

	// Untracked commands are never reported.
	Report("completion", "0.3.0", true, 0)
	Report("help", "0.3.0", true, 0)

	if Tracked("completion") || Tracked("help") {
		t.Error("completion and help must not be tracked")
	}

	// Opted out: tracked command, no event.
	t.Setenv("REEVIT_TELEMETRY", "0")

	Report("init", "0.3.0", true, 0)
}
func TestReportNeverWritesEnvAPIKeyToDisk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_API_KEY", "pfk_live_ci_only.supersecret")

	Report("init", "0.3.0", true, time.Millisecond)

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config should exist after minting an id: %v", err)
	}

	if strings.Contains(string(raw), "supersecret") ||
		strings.Contains(string(raw), "pfk_live_ci_only") {
		t.Fatalf("telemetry persisted the environment's API key: %s", raw)
	}

	var onDisk struct {
		APIKey      string `json:"api_key"`
		TelemetryID string `json:"telemetry_id"`
	}

	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if onDisk.APIKey != "" {
		t.Fatalf("api_key must be empty on disk, got %q", onDisk.APIKey)
	}

	if onDisk.TelemetryID == "" {
		t.Fatal("telemetry id should still have been minted and persisted")
	}
}

// The notice is a disclosure, so it has to arrive before the thing it
// discloses and it has to arrive exactly once.
func TestEnsureNoticePrintsOnceThenNeverAgain(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	var first bytes.Buffer

	EnsureNotice(&first, nil, nil)

	for _, want := range []string{NoticeLines[0], NoticeLines[1]} {
		if !strings.Contains(first.String(), want) {
			t.Errorf("first run did not disclose %q, got:\n%s", want, first.String())
		}
	}

	var second bytes.Buffer

	EnsureNotice(&second, nil, nil)

	if second.Len() != 0 {
		t.Errorf("the notice printed a second time:\n%s", second.String())
	}
}

// A user who opted out is told nothing, because nothing is collected. A
// notice here would be a warning about data that does not exist.
func TestEnsureNoticeSaysNothingWhenTelemetryIsOff(t *testing.T) {
	for _, off := range []struct{ name, key, value string }{
		{"REEVIT_TELEMETRY=0", "REEVIT_TELEMETRY", "0"},
		{"DO_NOT_TRACK=1", "DO_NOT_TRACK", "1"},
	} {
		t.Run(off.name, func(t *testing.T) {
			t.Setenv("DO_NOT_TRACK", "")
			t.Setenv("REEVIT_TELEMETRY", "")
			t.Setenv(off.key, off.value)
			t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))

			var out bytes.Buffer

			EnsureNotice(&out, nil, nil)

			if out.Len() != 0 {
				t.Errorf("opted out but still told about telemetry:\n%s", out.String())
			}
		})
	}
}

// Printing without persisting would re-disclose on every run, and Report
// would keep minting a fresh "install" each time.
//
// The failure has to happen in Save, not in Load: an unreadable config makes
// EnsureNotice bail one guard earlier, which would pass this test without
// ever exercising the guard it is about. So the directory exists and is
// readable, and only writing into it is refused.
func TestEnsureNoticeStaysSilentWhenTheIDCannotBePersisted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("REEVIT_API_KEY", "")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	t.Setenv("REEVIT_CONFIG", configPath)

	// Sanity: the config is loadable (absent is fine) before we lock the
	// directory, so a later silence cannot be blamed on Load.
	if _, err := config.Load(); err != nil {
		t.Fatalf("config must be loadable for this test to mean anything: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := config.SaveTelemetryID("probe"); err == nil {
		t.Fatal("the config directory is still writable; the mutation this test guards cannot be detected")
	}

	var out bytes.Buffer

	EnsureNotice(&out, nil, nil)

	if out.Len() != 0 {
		t.Errorf("disclosed an id that was never saved:\n%s", out.String())
	}
}

// The decorators are how the notice picks up the terminal's glyph set and
// colour; telemetry must apply them rather than hardcoding a marker.
func TestEnsureNoticeAppliesTheCallersDecorations(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	var out bytes.Buffer

	EnsureNotice(&out,
		func(text string) string { return "NOTE[" + text + "]" },
		func(text string) string { return "DIM(" + text + ")" },
	)

	want := "NOTE[DIM(" + NoticeLines[0] + ")]\n  DIM(" + NoticeLines[1] + ")\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("decorations not applied.\ngot:\n%q\nwant to contain:\n%q", out.String(), want)
	}
}
