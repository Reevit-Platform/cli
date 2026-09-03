package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/ui"
)

type paymentRow struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Method    string    `json:"method"`
	Status    string    `json:"status"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
}

// paymentsListDocument is the `payments list --json` contract. `data` is
// always an array, empty rather than null, so a consumer can pipe it into
// `.data[]` without a null guard.
type paymentsListDocument struct {
	Schema string       `json:"schema"`
	Mode   string       `json:"mode"`
	Data   []paymentRow `json:"data"`
	// Pagination is passed through verbatim, and only when the response
	// carried one. Re-modelling it would drop any field the backend adds,
	// and synthesising it from --limit would invent a `total` that is worse
	// than an absent one.
	Pagination json.RawMessage `json:"pagination,omitempty"`
}

// paymentsPage decodes a list response without caring which of the backend's
// list conventions it met. Four coexist today:
//
//	[…]                                        /payments — what this command hits
//	{"data":[…],"pagination":{…}}              /api-keys
//	{"success":true,"data":{…},"pagination":…} /kyc, /admin
//	{"connections":[…],"pagination":{…}}       /connections
//
// The order below mirrors sdks/go/helpers.go: bare array, then the legacy flat
// key, then `data` as an array, then `data.<key>`. The legacy key is tried
// before `data` so today's responses resolve early and this stays a provable
// no-op against the server as it is.
type paymentsPage struct {
	rows       []paymentRow
	pagination json.RawMessage
}

func (p *paymentsPage) UnmarshalJSON(raw []byte) error {
	if bare := bytes.TrimLeft(raw, " \t\r\n"); len(bare) > 0 && bare[0] == '[' {
		return json.Unmarshal(raw, &p.rows)
	}

	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return err
	}

	p.pagination = wrapped["pagination"]

	if legacy, ok := wrapped["payments"]; ok {
		if err := json.Unmarshal(legacy, &p.rows); err == nil {
			return nil
		}
	}

	data, ok := wrapped["data"]
	if !ok {
		return fmt.Errorf("no payments array in the response (keys: %s)", strings.Join(sortedKeys(wrapped), ", "))
	}

	if err := json.Unmarshal(data, &p.rows); err == nil {
		return nil
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal(data, &nested); err != nil {
		return fmt.Errorf("no payments array in the response (\"data\" is neither an array nor an object)")
	}

	inner, ok := nested["payments"]
	if !ok {
		return fmt.Errorf("no payments array in the response (data keys: %s)", strings.Join(sortedKeys(nested), ", "))
	}

	return json.Unmarshal(inner, &p.rows)
}

// sortedKeys names what the response DID contain, so a shape this decoder has
// never met reports itself instead of surfacing as an empty list.
func sortedKeys(m map[string]json.RawMessage) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

var (
	paymentsStatus string
	paymentsLimit  int
)

var paymentsCmd = &cobra.Command{
	Use:   "payments",
	Short: "Inspect payments",
	Long:  `Reads payments in the current mode. Live keys read live payments.`,
}

var paymentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent payments (current mode)",
	Long: `Lists the most recent payments in the current mode, newest first.

Shows the id, outcome, amount, provider, method, and how long ago it was
created. Filter with --status and shorten the list with --limit.`,
	Example: `  reevit payments list
  reevit payments list --status failed --limit 5`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := client()
		if err != nil {
			return err
		}

		query := url.Values{"limit": {strconv.Itoa(paymentsLimit)}}
		if paymentsStatus != "" {
			query.Set("status", paymentsStatus)
		}

		var page paymentsPage
		if err := c.Do(cmd.Context(), api.Request{Path: "/payments", Query: query}, &page); err != nil {
			return err
		}

		if jsonOut, _ := outputMode(cmd); jsonOut {
			rows := page.rows
			if rows == nil {
				rows = []paymentRow{}
			}

			return emitJSON(cmd, paymentsListDocument{
				Schema:     schemaPaymentsList,
				Mode:       c.Mode(),
				Data:       rows,
				Pagination: page.pagination,
			})
		}

		if len(page.rows) == 0 {
			fmt.Fprintf(noticeStream(cmd), "No payments in %s mode.\n", c.Mode())

			return nil
		}

		writePaymentsTable(cmd.OutOrStdout(), styleOf(cmd).out, page.rows, time.Now())

		return nil
	},
}

// paymentColumn describes one column's shape. Alignment and colour are
// properties of the column, not of the loop that prints it.
type paymentColumn struct {
	title string
	// right aligns the cell against the column's right edge. Money is read
	// by comparing digit positions, which only works when they line up.
	right bool
	// paint colours the cell's own value; nil leaves it plain.
	paint func(ui.Styler, string) string
}

var paymentColumns = []paymentColumn{
	{title: "Id"},
	{title: "Status", paint: paintPaymentStatus},
	{title: "Amount", right: true},
	{title: "Provider"},
	{title: "Method"},
	{title: "Created"},
}

// writePaymentsTable lays the table out by hand rather than with tabwriter.
// tabwriter measures cells in bytes, so every SGR sequence in a coloured cell
// counts as visible width and the column it lives in drifts right by the size
// of the escape. Measuring the plain text and padding outside the styling is
// what keeps a coloured table aligned.
func writePaymentsTable(out io.Writer, sty ui.Styler, rows []paymentRow, now time.Time) {
	cells := make([][]string, 0, len(rows))

	for _, p := range rows {
		cells = append(cells, []string{
			p.ID,
			p.Status,
			fmt.Sprintf("%s %.2f", p.Currency, float64(p.Amount)/100),
			p.Provider,
			p.Method,
			relativeTime(p.CreatedAt, now),
		})
	}

	widths := make([]int, len(paymentColumns))

	for i, column := range paymentColumns {
		widths[i] = utf8.RuneCountInString(column.title)
	}

	for _, row := range cells {
		for i, value := range row {
			if n := utf8.RuneCountInString(value); n > widths[i] {
				widths[i] = n
			}
		}
	}

	header := make([]string, len(paymentColumns))

	for i, column := range paymentColumns {
		header[i] = sty.Dim(column.title)
	}

	fmt.Fprintln(out, joinPaymentRow(header, plainRowOf(paymentColumns), widths))

	for _, row := range cells {
		painted := make([]string, len(row))

		for i, value := range row {
			painted[i] = value
			if paint := paymentColumns[i].paint; paint != nil {
				painted[i] = paint(sty, value)
			}
		}

		fmt.Fprintln(out, joinPaymentRow(painted, row, widths))
	}
}

// plainRowOf returns the header's unstyled text, which is what its widths must
// be measured from.
func plainRowOf(columns []paymentColumn) []string {
	plain := make([]string, len(columns))
	for i, column := range columns {
		plain[i] = column.title
	}

	return plain
}

// joinPaymentRow pads using the plain widths and writes the styled values, so
// an escape sequence never counts as a printed column.
func joinPaymentRow(styled, plain []string, widths []int) string {
	var b strings.Builder

	for i, value := range styled {
		last := i == len(styled)-1
		gap := widths[i] - utf8.RuneCountInString(plain[i])

		if paymentColumns[i].right {
			b.WriteString(strings.Repeat(" ", gap))
			b.WriteString(value)
		} else {
			b.WriteString(value)

			// Trailing whitespace on the last column is invisible noise that
			// shows up in diffs and in `| cat -A`.
			if !last {
				b.WriteString(strings.Repeat(" ", gap))
			}
		}

		if !last {
			b.WriteString("  ")
		}
	}

	return b.String()
}

// paintPaymentStatus gives the three statuses a developer scans for their own
// colour. Anything else stays default rather than being guessed at.
func paintPaymentStatus(sty ui.Styler, status string) string {
	switch status {
	case "succeeded":
		return sty.SuccessText(status)
	case "failed":
		return sty.FailureText(status)
	case "pending":
		return sty.WarningText(status)
	default:
		return status
	}
}

// relativeTime answers "how fresh is this?", which is the only question a
// payment list's timestamp is ever asked. An absolute timestamp makes the
// reader do the subtraction; past about two days the date is the better
// answer and the arithmetic stops being useful.
func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}

	switch age := now.Sub(t); {
	case age < 0:
		// A clock skew between the API and this machine is not news the user
		// can act on; show the timestamp rather than "in 3m".
		return t.Local().Format("2006-01-02")
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	case age < 48*time.Hour:
		return "yesterday"
	default:
		return t.Local().Format("2006-01-02")
	}
}

func init() {
	paymentsListCmd.Flags().StringVar(&paymentsStatus, "status", "", "filter by status (succeeded, failed, pending, ...)")
	paymentsListCmd.Flags().IntVar(&paymentsLimit, "limit", 20, "max rows")
	paymentsCmd.AddCommand(paymentsListCmd)
	rootCmd.AddCommand(paymentsCmd)
}
