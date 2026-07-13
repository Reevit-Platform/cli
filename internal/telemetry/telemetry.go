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
func Report(command, version string, success bool, duration time.Duration, notice io.Writer) {
	if !Enabled() {
		return
	}

	if _, ok := tracked[command]; !ok {
		return
	}

	cfg, err := config.Load()
	if err != nil {
		return
	}

	machineID := ensureMachineID(&cfg, notice)
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

// ensureMachineID returns the anonymous per-install id, minting it (and
// printing the one-time notice) on first use. Returns "" if the config can't
// be persisted — no notice means no tracking.
func ensureMachineID(cfg *config.Config, notice io.Writer) string {
	if cfg.TelemetryID != "" {
		return cfg.TelemetryID
	}

	cfg.TelemetryID = uuid.NewString()

	if notice != nil {
		fmt.Fprintln(notice, "\nreevit collects anonymous usage data (command, version, OS — never file")
		fmt.Fprintln(notice, "contents, paths, or keys) to improve the CLI. Opt out any time with")
		fmt.Fprintln(notice, "REEVIT_TELEMETRY=0 or DO_NOT_TRACK=1. Docs: https://docs.reevit.io/cli")
	}

	if _, err := config.Save(*cfg); err != nil {
		return ""
	}

	return cfg.TelemetryID
}
