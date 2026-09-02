package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/telemetry"
)

// runForNotice runs one command against a stub API with telemetry left ON,
// sharing a config path across calls so the second run sees the id the first
// one minted.
func runForNotice(t *testing.T, configPath string, args ...string) (stdout, stderr string) {
	t.Helper()

	resetFlags()
	t.Cleanup(resetFlags)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/payments" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"no route"}`))

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"pmt_1","status":"succeeded","amount":12500,` +
			`"currency":"GHS","provider":"paystack","method":"card",` +
			`"created_at":"2026-01-02T10:04:05Z"}]`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_API_KEY", "pfk_test_notice.sec")
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_MODE", "")
	t.Setenv("REEVIT_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	t.Setenv("TZ", "UTC")

	var out, errOut bytes.Buffer

	if err := ExecuteWith(context.Background(), args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("%v: %v\nstderr:\n%s", args, err, errOut.String())
	}

	return out.String(), errOut.String()
}

// The disclosure has to come before the run it discloses, and it has to leave
// stdout alone — a notice in the middle of piped data corrupts it.
func TestFirstRunNoticeOpensStderrAndNeverTouchesStdout(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")

	stdout, stderr := runForNotice(t, config, "payments", "list")

	if !strings.HasPrefix(stderr, "\n"+"- "+telemetry.NoticeLines[0]) {
		t.Fatalf("the notice is not the first thing on stderr:\n%q", stderr)
	}

	if strings.Contains(stdout, "usage data") {
		t.Errorf("the notice leaked into stdout:\n%s", stdout)
	}

	if !strings.Contains(stdout, "pmt_1") {
		t.Fatalf("the command did not produce its own output:\n%s", stdout)
	}
}

// Once per install, not once per run.
func TestFirstRunNoticeIsNotRepeatedOnTheNextRun(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")

	if _, first := runForNotice(t, config, "payments", "list"); !strings.Contains(first, "usage data") {
		t.Fatalf("first run did not disclose telemetry:\n%s", first)
	}

	_, second := runForNotice(t, config, "payments", "list")
	if strings.Contains(second, "usage data") {
		t.Errorf("the notice printed again on the second run:\n%s", second)
	}
}

// Help, completion and version are not reported, so there is nothing to
// disclose before them. Announcing collection that never happens trains
// people to ignore the notice that matters.
func TestNoNoticeBeforeCommandsThatAreNeverReported(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"completion", "bash"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.json")

			stdout, stderr := runForNotice(t, config, args...)

			if strings.Contains(stderr, "usage data") || strings.Contains(stdout, "usage data") {
				t.Errorf("%v disclosed telemetry it never sends:\nstderr:\n%s", args, stderr)
			}
		})
	}
}
