// Package telemetry reports one anonymous usage event per command run to the
// Reevit API (which forwards to product analytics server-side — no analytics
// keys ship in this binary).
//
// What is sent: command name, CLI version, OS/arch, detected stack and chosen
// targets (init only), success/failure, duration, org id when logged in, and
// a random per-install UUID. Never sent: paths, file contents, keys,
// hostnames, or anything derived from hardware identifiers.
//
// Opt out with DO_NOT_TRACK=1 or REEVIT_TELEMETRY=0. A one-time notice is
// printed on first use.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Reevit-Platform/cli/internal/config"
)

// tracked is the set of commands worth reporting — mirrors the backend's
// ingest allowlist. Help/completion/version runs are noise, not usage.
var tracked = map[string]struct{}{
	"login":    {},
	"init":     {},
	"doctor":   {},
	"listen":   {},
	"trigger":  {},
	"payments": {},
}

// Tracked reports whether a top-level command sends an event. The first-run
// notice is gated on it too: disclosing collection before `reevit --help`,
// which is never reported, is a warning about nothing.
func Tracked(command string) bool {
	_, ok := tracked[command]

	return ok
}

var (
	mu      sync.Mutex
	stack   string
	targets []string
)

// SetContext records init-specific detail (detected stack, chosen targets)
// for the event sent when the command finishes.
func SetContext(detectedStack string, chosenTargets []string) {
	mu.Lock()
	defer mu.Unlock()

	stack = detectedStack
	targets = chosenTargets
}

// Enabled reports whether telemetry may be sent. DO_NOT_TRACK is the
// cross-tool convention (consoledonottrack.com); REEVIT_TELEMETRY=0 is ours.
func Enabled() bool {
	if v := os.Getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("REEVIT_TELEMETRY"))) {
	case "0", "false", "off":
		return false
	}

	return true
}

// Report sends one event for a finished command run. Fire-and-forget with a
// short deadline: telemetry may never slow a command down noticeably or
// surface an error. Call after the command completes.
func Report(command, version string, success bool, duration time.Duration) {
	if !Enabled() {
		return
	}

	if !Tracked(command) {
		return
	}

	cfg, err := config.Load()
	if err != nil {
		return
	}

	machineID := ensureMachineID(&cfg)
	if machineID == "" {
		return
	}

	mu.Lock()
	body, err := json.Marshal(map[string]any{
		"command":     command,
		"version":     version,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"stack":       stack,
		"targets":     targets,
		"success":     success,
		"duration_ms": duration.Milliseconds(),
		"org_id":      cfg.OrgID,
		"machine_id":  machineID,
	})
	mu.Unlock()

	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/v1/cli/telemetry", bytes.NewReader(body))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 1500 * time.Millisecond}).Do(req)
	if err != nil {
		return
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ensureMachineID returns the anonymous per-install id, minting it on first
// use. Returns "" if the config can't be persisted, which means no tracking:
// an id that only exists in memory would produce a new "install" every run.
func ensureMachineID(cfg *config.Config) string {
	if cfg.TelemetryID != "" {
		return cfg.TelemetryID
	}

	id := uuid.NewString()

	// Persist only the id. cfg came from config.Load, which folds
	// REEVIT_API_KEY into what it returns — writing the whole struct back
	// would leak an env-only credential to disk, contradicting the notice
	// the user was just shown.
	if _, err := config.SaveTelemetryID(id); err != nil {
		return ""
	}

	cfg.TelemetryID = id

	return cfg.TelemetryID
}

// NoticeLines is the one-time first-run disclosure, unstyled. It lives here
// because this package decides what is collected; how it looks on a terminal
// is the caller's business.
var NoticeLines = [2]string{
	"reevit sends anonymous usage data (command, version, OS — never files, paths or keys).",
	"Opt out: REEVIT_TELEMETRY=0 or DO_NOT_TRACK=1 · docs.reevit.io/cli",
}

// EnsureNotice mints the anonymous per-install id and prints the one-time
// notice, before the command runs rather than after it. Report used to do
// this, which meant the disclosure landed underneath the output of the very
// run it was disclosing.
//
// It prints only when it actually mints an id, and only when that id reaches
// disk: an unpersisted id would show the notice again on the next run, and a
// notice shown to a user who opted out is noise about data nobody collected.
//
// note and dim decorate the two lines (glyph, colour) — internal/ui owns that
// vocabulary and this package must not import it, since ui is the lower
// layer. Either may be nil, which prints the line as-is.
func EnsureNotice(w io.Writer, note, dim func(string) string) {
	if w == nil || !Enabled() {
		return
	}

	cfg, err := config.Load()
	if err != nil || cfg.TelemetryID != "" {
		return
	}

	if ensureMachineID(&cfg) == "" {
		return
	}

	if note == nil {
		note = func(text string) string { return "- " + text }
	}

	if dim == nil {
		dim = func(text string) string { return text }
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, note(dim(NoticeLines[0])))
	fmt.Fprintln(w, "  "+dim(NoticeLines[1]))
	fmt.Fprintln(w)
}
