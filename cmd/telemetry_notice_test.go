package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

// onDiskTelemetryID reads the id straight out of the config file rather than
// through config.Load, so the test observes what a later run would observe.
func onDiskTelemetryID(t *testing.T, configPath string) string {
	t.Helper()

	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}

		t.Fatalf("read %s: %v", configPath, err)
	}

	var onDisk struct {
		TelemetryID string `json:"telemetry_id"`
	}

	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse %s: %v", configPath, err)
	}

	return onDisk.TelemetryID
}

// The invariant: an anonymous install id is never minted without the notice
// being shown on the same run.
//
// Plan 031 asked for the notice to be skipped under --json. That is a
// silent-tracking hole, not a courtesy. telemetry.Report mints and persists
// the id through its own call to ensureMachineID, so suppressing the notice
// suppresses the disclosure and not the collection — and because the notice
// is keyed on the id being empty, the user is never told on any later run
// either. --json is a promise about stdout; this writes to stderr, so there
// was nothing to protect in the first place.
//
// If 032 reintroduces the guard, this test fails.
func TestAnIDIsNeverMintedWithoutTheNoticeBeingShown(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")

	// Stand in for the --json flag 032 adds to `payments list`. It lives on
	// a temporary sibling because pflag cannot un-register a flag, and
	// paymentsListCmd is a package-level var shared with every other test.
	// The parent is still `payments`, so this runs the same
	// PersistentPreRunE path with the same tracked top-level name.
	var asJSON bool

	probe := &cobra.Command{Use: "list-json", RunE: paymentsListCmd.RunE}
	probe.Flags().BoolVar(&asJSON, "json", false, "machine-readable output on stdout")

	paymentsCmd.AddCommand(probe)
	t.Cleanup(func() { paymentsCmd.RemoveCommand(probe) })

	stdout, stderr := runForNotice(t, config, "payments", "list-json", "--json")

	// runForNotice goes through ExecuteWith, which is only half of what a
	// real run does: Execute follows it with telemetry.Report, and Report
	// mints and persists the machine id through its own ensureMachineID
	// call, whatever the notice did or did not print. Reproducing that
	// second half is the whole point — without it the test can only see the
	// id EnsureNotice itself minted, and would go quiet exactly when the
	// disclosure is suppressed.
	telemetry.Report("payments", Version, true, time.Millisecond)

	minted := onDiskTelemetryID(t, config)
	disclosed := strings.Contains(stderr, telemetry.NoticeLines[0])

	// Without this the invariant would hold vacuously on a run that never
	// minted anything.
	if minted == "" {
		t.Fatalf("no id was minted, so this run cannot test the invariant.\nstderr:\n%s", stderr)
	}

	if !disclosed {
		t.Fatalf("machine id %q was minted and persisted to disk without the user being told. "+
			"That is silent tracking: telemetry.Report will send events under this id, and because "+
			"the notice only prints while the id is empty, no later run will disclose it either.\n"+
			"stderr was:\n%q", minted, stderr)
	}

	// --json governs stdout. The notice is stderr, so a machine reading
	// stdout is unaffected by it either way.
	if strings.Contains(stdout, "usage data") {
		t.Errorf("the notice leaked into stdout, which --json makes machine-readable:\n%s", stdout)
	}

	if !strings.Contains(stdout, "pmt_1") {
		t.Fatalf("the command produced no output of its own:\n%s", stdout)
	}
}
