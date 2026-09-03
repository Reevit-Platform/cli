package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/ui"
)

// A remedy is data, not prose. It renders on its own line, in the style the
// CLI uses everywhere else for "type this", so a run with five findings reads
// as five things to do rather than five sentences to parse.
func TestRemedyRendersAsItsOwnCommandLine(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{}
	res.failr(&out, "reevit init", "project manifest is missing")

	got := out.String()

	if want := "  x project manifest is missing\n    > reevit init\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	if res.failures != 1 {
		t.Fatalf("failures = %d, want 1", res.failures)
	}
}

// fail/warn are failr/warnr with no remedy, and must not leave a dangling
// arrow behind — most of doctor's 52 findings have no single command that
// clears them.
func TestFindingsWithoutARemedyPrintOneLine(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{}
	res.fail(&out, "no project detected here")
	res.warn(&out, "platform bootstrap check skipped")

	got := out.String()

	if strings.Contains(got, ">") {
		t.Fatalf("output = %q, want no remedy arrow", got)
	}

	if res.failures != 1 || res.warnings != 1 {
		t.Fatalf("failures = %d warnings = %d, want 1 and 1", res.failures, res.warnings)
	}
}

// The remedy must be styled as a command, not folded into the message, or the
// split gains nothing over the em-dash it replaced.
func TestRemedyIsAccentedSeparatelyFromTheMessage(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{sty: ui.New(ui.Options{
		Env: []string{"FORCE_COLOR=1", "LANG=en_US.UTF-8"},
	})}
	res.warnr(&out, "reevit init", "REEVIT_ORG_ID is not set")

	got := out.String()

	if !strings.Contains(got, "\x1b[36mreevit init\x1b[0m") {
		t.Fatalf("output = %q, want the remedy accented", got)
	}

	if strings.Contains(got, "\x1b[36mREEVIT_ORG_ID") {
		t.Fatalf("output = %q, want the message left unaccented", got)
	}
}

// The summary answers two questions — what did it find, and what do I do now —
// and the counts have to include warnings, because --strict and CI fail on
// them and the old line never mentioned they existed.
func TestDoctorSummaryReportsBothTallies(t *testing.T) {
	cases := []struct {
		failures, warnings int
		want               []string
		deny               []string
	}{
		{0, 0, []string{"Everything checks out."}, []string{"problem", "warning"}},
		{0, 2, []string{"Setup looks good (2 warnings)."}, []string{"problem"}},
		{0, 1, []string{"Setup looks good (1 warning)."}, nil},
		{1, 0, []string{"1 problem.", "Fix the x items above and run `reevit doctor` again."}, []string{"warning"}},
		{2, 3, []string{"2 problems, 3 warnings."}, nil},
		{1, 1, []string{"1 problem, 1 warning."}, nil},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%df%dw", tc.failures, tc.warnings), func(t *testing.T) {
			var out bytes.Buffer

			printDoctorSummary(&out, &doctorResult{failures: tc.failures, warnings: tc.warnings})

			got := out.String()

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("summary = %q, want it to contain %q", got, want)
				}
			}

			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("summary = %q, want it not to mention %q", got, deny)
				}
			}
		})
	}
}

// The wrapped transport error names a request the user never typed and repeats
// a URL doctor printed one line earlier. Only the tail is news, and printing
// the whole thing pushed the actual cause off the right edge of an 80-column
// terminal.
func TestShortDialCauseKeepsOnlyTheCause(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "api client wrapping plus net/http wrapping",
			err: errors.New(`request GET /payments: Get "http://127.0.0.1:1/v1/payments": ` +
				`dial tcp 127.0.0.1:1: connect: connection refused`),
			want: "dial tcp 127.0.0.1:1: connect: connection refused",
		},
		{
			name: "url containing a colon-space is not cut early",
			err: errors.New(`request GET /payments: Get "http://host/v1/payments?q=a:%20b": ` +
				"context deadline exceeded"),
			want: "context deadline exceeded",
		},
		{
			// Not every failure comes from the transport: a decode error
			// carries the api.Client wrapper and no net/http prefix, so the
			// two cuts are not interchangeable.
			name: "api client wrapping alone",
			err:  errors.New("request GET /payments: decode response: unexpected EOF"),
			want: "decode response: unexpected EOF",
		},
		{
			name: "an unwrapped error is left alone",
			err:  errors.New("tls: bad certificate"),
			want: "tls: bad certificate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortDialCause(tc.err); got != tc.want {
				t.Fatalf("shortDialCause = %q, want %q", got, tc.want)
			}
		})
	}
}
