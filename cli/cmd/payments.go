package cmd

import (
	"fmt"
	"net/url"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
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
}

var paymentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent payments (current mode)",
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
			fmt.Fprintf(cmd.OutOrStdout(), "No payments in %s mode.\n", c.Mode())

			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tAMOUNT\tPROVIDER\tMETHOD\tCREATED")

		for _, p := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s %.2f\t%s\t%s\t%s\n",
				p.ID, p.Status, p.Currency, float64(p.Amount)/100,
				p.Provider, p.Method, p.CreatedAt.Local().Format("2006-01-02 15:04"))
		}

		return w.Flush()
	},
}

func init() {
	paymentsListCmd.Flags().StringVar(&paymentsStatus, "status", "", "filter by status (succeeded, failed, pending, ...)")
	paymentsListCmd.Flags().IntVar(&paymentsLimit, "limit", 20, "max rows")
	paymentsCmd.AddCommand(paymentsListCmd)
	rootCmd.AddCommand(paymentsCmd)
}
