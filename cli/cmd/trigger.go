package cmd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
)

// Magic simulator amounts — mirrors the backend's stub-provider MagicOutcomes
// table (adapters/psp/stub/magic.go). Triggering an event means creating a
// REAL sandbox payment through the simulator, so the resulting outbound
// events flow through the production pipeline and match production schemas
// by construction.
var triggerAmounts = map[string]int64{
	"payment.succeeded":          4000,
	"payment.failed":             4001,
	"payment.insufficient_funds": 4002,
	"payment.timeout":            4003,
	"payment.provider_downtime":  4004,
}

var (
	triggerCurrency string
	triggerAmountOv int64
)

var triggerCmd = &cobra.Command{
	Use:   "trigger <event>",
	Short: "Fire a test event by driving the sandbox simulator",
	Long: `Creates a real test-mode payment against the simulator connection using the
documented magic amount for the requested outcome, so every downstream event
(webhooks, notifications, SSE) is produced by the production pipeline.

Supported: ` + strings.Join(triggerEventNames(), ", "),
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		event := strings.ToLower(strings.TrimSpace(args[0]))

		amount, ok := triggerAmounts[event]
		if !ok {
			return fmt.Errorf("unknown event %q — supported: %s", event, strings.Join(triggerEventNames(), ", "))
		}

		if triggerAmountOv > 0 {
			amount = triggerAmountOv
		}

		c, err := client()
		if err != nil {
			return err
		}

		if c.Mode() != "test" {
			return fmt.Errorf("trigger only runs in test mode (REEVIT_MODE=%s)", c.Mode())
		}

		paymentID, status, err := triggerSimulatorEvent(cmd.Context(), c, event, amount, triggerCurrency)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Triggered %s → payment %s (status: %s)\n", event, paymentID, status)

		return nil
	},
}

// triggerSimulatorEvent creates a real sandbox payment through the simulator
// with the outcome's magic amount, exercising the production routing path.
// Shared by `reevit trigger` and `reevit doctor --e2e`.
func triggerSimulatorEvent(ctx context.Context, c *api.Client, event string, amount int64, currency string) (id, status string, err error) {
	if _, err := ensureSimulatorConnection(ctx, c); err != nil {
		return "", "", err
	}

	var payment struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}

	err = c.Do(ctx, api.Request{
		Method:     "POST",
		Path:       "/payments/intents",
		Idempotent: true,
		// Intents always route — the simulator connection ensured above is
		// what the router picks.
		Body: map[string]any{
			"amount":      amount,
			"currency":    strings.ToUpper(currency),
			"method":      "mobile_money",
			"country":     "GH",
			"description": "reevit trigger " + event,
			"metadata":    map[string]any{"created_via": "reevit-cli", "trigger": event},
		},
	}, &payment)
	if err != nil {
		return "", "", err
	}

	return payment.ID, payment.Status, nil
}

// ensureSimulatorConnection finds or creates the sandbox simulator connection.
func ensureSimulatorConnection(ctx context.Context, c *api.Client) (string, error) {
	var listed struct {
		Connections []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Status   string `json:"status"`
		} `json:"connections"`
	}

	if err := c.Do(ctx, api.Request{Path: "/connections", Query: url.Values{"provider": {"stub"}}}, &listed); err != nil {
		return "", err
	}

	for _, conn := range listed.Connections {
		if conn.Provider == "stub" {
			return conn.ID, nil
		}
	}

	var created struct {
		ID string `json:"id"`
	}

	err := c.Do(ctx, api.Request{
		Method:     "POST",
		Path:       "/connections",
		Idempotent: true,
		// The API rejects unknown fields; connections are identified by
		// provider + labels (no name field).
		Body: map[string]any{
			"provider":    "stub",
			"mode":        "sandbox",
			"credentials": map[string]any{"simulator": true},
			"labels":      []string{"simulator"},
		},
	}, &created)
	if err != nil {
		return "", fmt.Errorf("create simulator connection: %w", err)
	}

	return created.ID, nil
}

func triggerEventNames() []string {
	names := make([]string, 0, len(triggerAmounts))
	for name := range triggerAmounts {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func init() {
	triggerCmd.Flags().StringVar(&triggerCurrency, "currency", "GHS", "payment currency")
	triggerCmd.Flags().Int64Var(&triggerAmountOv, "amount", 0, "override the magic amount (advanced)")
	rootCmd.AddCommand(triggerCmd)
}
