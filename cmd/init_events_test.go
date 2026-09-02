package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/setup"
	"github.com/Reevit-Platform/cli/internal/ui"
)

// Every "running" line printSetupEvent emits has to be answered by a
// "complete" line. `setup.Apply` can sit inside a dependency install for a
// minute; a `→ Installing dependencies (pnpm)…` that is never closed off is
// indistinguishable from a hang, which is the single most common reason a
// developer kills `reevit init` half-way and leaves the project inconsistent.
func TestPrintSetupEventAnswersEveryRunningLine(t *testing.T) {
	cases := []struct {
		name  string
		event setup.Event
		want  []string
		// deny catches the events that must stay silent — printing a line for
		// every stage would bury the three that matter in bookkeeping.
		silent bool
	}{
		{
			name:  "bootstrap running",
			event: setup.Event{Stage: "bootstrap", Status: "running", Detail: "configuring Reevit test mode"},
			want:  []string{"> Configuring Reevit test mode…"},
		},
		{
			name:  "bootstrap complete",
			event: setup.Event{Stage: "bootstrap", Status: "complete", Detail: "test project ready"},
			want:  []string{"ok Reevit test mode configured"},
		},
		{
			name:  "install running names the installer only",
			event: setup.Event{Stage: "install", Status: "running", Detail: "pnpm add @reevit/node"},
			want:  []string{"> Installing dependencies (pnpm)…"},
		},
		{
			name: "install complete answers it and keeps the log path",
			event: setup.Event{
				Stage: "install", Status: "complete",
				Detail: "pnpm add @reevit/node", LogPath: "/tmp/install.log",
			},
			want: []string{"ok Dependencies installed (pnpm)", "- log retained at /tmp/install.log"},
		},
		{
			name:   "install failure stays silent because Apply returns the error",
			event:  setup.Event{Stage: "install", Status: "failed", Detail: "pnpm add @reevit/node"},
			silent: true,
		},
		{
			name:  "verify running",
			event: setup.Event{Stage: "verify", Status: "running", Detail: "checking project credentials against the sandbox"},
			want:  []string{"> Verifying project credentials against the sandbox…"},
		},
		{
			name:  "verify complete",
			event: setup.Event{Stage: "verify", Status: "complete", Detail: "sandbox verification passed"},
			want:  []string{"ok Project credentials verified against the sandbox"},
		},
		{
			name:   "manifest bookkeeping stays silent",
			event:  setup.Event{Stage: "manifest", Status: "complete", Detail: "pending setup recorded"},
			silent: true,
		},
		{
			name:   "write_env bookkeeping stays silent",
			event:  setup.Event{Stage: "write_env", Status: "complete", Detail: "project credentials safely stored"},
			silent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			// The zero Styler is the ASCII, no-colour rendering, so the
			// assertions read as the text a plain terminal shows.
			printSetupEvent(&out, ui.Styler{}, tc.event)

			got := out.String()

			if tc.silent {
				if got != "" {
					t.Fatalf("output = %q, want nothing for %s/%s", got, tc.event.Stage, tc.event.Status)
				}

				return
			}

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("output = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

// The complete line has to name the same installer as the running line, or the
// pair does not read as a pair. Both derive it from the event Detail, which is
// the whole argv.
func TestInstallerNameIsTheToolNotTheArgv(t *testing.T) {
	cases := map[string]string{
		"pnpm add @reevit/node":           "pnpm",
		"npm install --save @reevit/node": "npm",
		"bun":                             "bun",
		"":                                "",
	}

	for detail, want := range cases {
		if got := installerName(detail); got != want {
			t.Errorf("installerName(%q) = %q, want %q", detail, got, want)
		}
	}
}
