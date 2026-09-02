package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Reevit-Platform/cli/internal/ui"
)

// An absolute timestamp makes the reader subtract. "How fresh is this?" is the
// only question a payment list's Created column is ever asked, and past about
// two days the arithmetic stops being the useful answer.
func TestRelativeTimeAnswersHowFreshNotWhen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "seconds", at: now.Add(-30 * time.Second), want: "just now"},
		{name: "minutes", at: now.Add(-2 * time.Minute), want: "2m ago"},
		{name: "just under an hour", at: now.Add(-59 * time.Minute), want: "59m ago"},
		{name: "hours", at: now.Add(-3 * time.Hour), want: "3h ago"},
		{name: "just under a day", at: now.Add(-23 * time.Hour), want: "23h ago"},
		{name: "yesterday", at: now.Add(-30 * time.Hour), want: "yesterday"},
		{name: "older than that is a date", at: now.Add(-72 * time.Hour), want: "2026-08-30"},
		// A clock skew between the API and this machine is not news the user
		// can act on, and "in 3m" reads as a bug in the CLI.
		{name: "the future is a date, never a countdown", at: now.Add(time.Hour), want: "2026-09-02"},
		{name: "a missing timestamp is not the year 1", at: time.Time{}, want: "—"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := relativeTime(test.at, now); got != test.want {
				t.Errorf("relativeTime = %q, want %q", got, test.want)
			}
		})
	}
}

func paymentsFixture() []paymentRow {
	at := time.Date(2026, 1, 2, 9, 30, 0, 0, time.UTC)

	return []paymentRow{
		{ID: "pmt_1", Provider: "paystack", Method: "card", Status: "succeeded",
			Amount: 12500, Currency: "GHS", CreatedAt: at},
		{ID: "pmt_2", Provider: "hubtel", Method: "mobile_money", Status: "failed",
			Amount: 900, Currency: "GHS", CreatedAt: at},
		{ID: "pmt_3", Provider: "hubtel", Method: "mobile_money", Status: "pending",
			Amount: 25, Currency: "GHS", CreatedAt: at},
	}
}

// tabwriter measures cells in bytes, so an SGR sequence counts as visible
// width and the column it lives in drifts right by the size of the escape.
// This is the regression that forced the hand-rolled layout.
func TestPaymentsTableKeepsItsColumnsUnderColour(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	coloured := ui.New(ui.Options{Env: []string{"FORCE_COLOR=1", "LANG=en_US.UTF-8"}})

	var plainOut, colourOut bytes.Buffer

	writePaymentsTable(&plainOut, ui.Styler{}, paymentsFixture(), now)
	writePaymentsTable(&colourOut, coloured, paymentsFixture(), now)

	if !coloured.Color() {
		t.Fatal("the coloured styler is not coloured; the assertion below would be vacuous")
	}

	if strings.Contains(plainOut.String(), "\x1b") {
		t.Errorf("the plain table carries escapes:\n%q", plainOut.String())
	}

	if colourOut.String() == plainOut.String() {
		t.Fatalf("colour changed nothing:\n%q", colourOut.String())
	}

	if stripped := goldenSGRRe.ReplaceAllString(colourOut.String(), ""); stripped != plainOut.String() {
		t.Errorf("colour moved a column:\nplain:\n%s\nstripped:\n%s", plainOut.String(), stripped)
	}
}

// The three statuses a developer scans for get the glyph vocabulary's colours
// without the glyph; anything else stays default rather than being guessed at.
func TestPaymentStatusColoursOnlyTheKnownOutcomes(t *testing.T) {
	t.Parallel()

	sty := ui.New(ui.Options{Env: []string{"FORCE_COLOR=1", "LANG=en_US.UTF-8"}})

	for _, test := range []struct {
		status string
		want   string
	}{
		{status: "succeeded", want: "\x1b[32msucceeded\x1b[0m"},
		{status: "failed", want: "\x1b[31mfailed\x1b[0m"},
		{status: "pending", want: "\x1b[33mpending\x1b[0m"},
		{status: "refunded", want: "refunded"},
		{status: "processing", want: "processing"},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()

			if got := paintPaymentStatus(sty, test.status); got != test.want {
				t.Errorf("paintPaymentStatus(%q) = %q, want %q", test.status, got, test.want)
			}
		})
	}
}

// Money is read by comparing digit positions: 9.00 under 125.00 with the
// decimal points out of line is a column that has to be re-read.
func TestPaymentsTableRightAlignsTheAmount(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	writePaymentsTable(&out, ui.Styler{}, paymentsFixture(),
		time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q, want a header and three rows", lines)
	}

	columns := make([]int, 0, 3)

	for _, line := range lines[1:] {
		at := strings.Index(line, ".")
		if at < 0 {
			t.Fatalf("line = %q, want a decimal amount", line)
		}

		columns = append(columns, at)
	}

	for _, at := range columns[1:] {
		if at != columns[0] {
			t.Fatalf("decimal points at %v:\n%s", columns, out.String())
		}
	}

	// The header is a label, not data, and dimming it is what lets the eye
	// fall straight to the rows.
	if !strings.HasPrefix(lines[0], "Id     Status") {
		t.Errorf("header = %q, want title case", lines[0])
	}

	// Trailing whitespace is invisible noise in diffs and in `| cat -A`.
	for _, line := range lines {
		if strings.TrimRight(line, " ") != line {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}
