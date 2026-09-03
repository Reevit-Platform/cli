package cmd

import (
	"fmt"
	"io"
	"net/url"
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

		var rows []paymentRow
		if err := c.Do(cmd.Context(), api.Request{Path: "/payments", Query: query}, &rows); err != nil {
			return err
		}

		if len(rows) == 0 {
			fmt.Fprintf(noticeStream(cmd), "No payments in %s mode.\n", c.Mode())

			return nil
		}

		writePaymentsTable(cmd.OutOrStdout(), styleOf(cmd).out, rows, time.Now())

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
