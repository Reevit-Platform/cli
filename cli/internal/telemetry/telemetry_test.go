package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

func TestReportSendsEventAndNoticesOnce(t *testing.T) {
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

	var notice bytes.Buffer

	Report("init", "0.3.0", true, 42*time.Millisecond, &notice)

	if !strings.Contains(notice.String(), "REEVIT_TELEMETRY=0") {
		t.Error("first run must print the opt-out notice")
	}

	if len(bodies) != 1 {
		t.Fatalf("events sent = %d, want 1", len(bodies))
	}

	var event map[string]any

	_ = json.Unmarshal(bodies[0], &event)

	if event["command"] != "init" || event["stack"] != "nextjs" || event["machine_id"] == "" {
		t.Errorf("event = %v", event)
	}

	// Second run: same machine id, no second notice.
	var secondNotice bytes.Buffer

	Report("doctor", "0.3.0", false, time.Millisecond, &secondNotice)

	if secondNotice.Len() != 0 {
		t.Error("notice must only print once per install")
	}

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
	Report("completion", "0.3.0", true, 0, io.Discard)
	Report("help", "0.3.0", true, 0, io.Discard)

	// Opted out: tracked command, no event, and NO notice either.
	t.Setenv("REEVIT_TELEMETRY", "0")

	var notice bytes.Buffer

	Report("init", "0.3.0", true, 0, &notice)

	if notice.Len() != 0 {
		t.Error("opted-out runs must not print the notice")
	}
}
