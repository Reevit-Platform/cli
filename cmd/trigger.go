package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/scaffold"
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

// triggerEventOrder is the order the events are offered in, which is the
// order a person meets them: the happy path first, then the failure modes in
// descending likelihood. Ranging over triggerAmounts would order them by map
// iteration; sorting them would order them alphabetically, which puts
// `payment.failed` ahead of `payment.succeeded`. Neither is a reading order.
var triggerEventOrder = []string{
	"payment.succeeded",
	"payment.failed",
	"payment.insufficient_funds",
	"payment.timeout",
	"payment.provider_downtime",
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

Supported events, with the magic amount each one uses:
` + triggerSupportedList(),
	Example: `  reevit trigger payment.succeeded
  reevit trigger payment.timeout
  reevit trigger payment.failed --currency NGN`,
	Args: exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		event := strings.ToLower(strings.TrimSpace(args[0]))

		amount, ok := triggerAmounts[event]
		if !ok {
			return fmt.Errorf("unknown event %q — supported: %s", event, strings.Join(triggerEventNames(), ", "))
		}

		sty := styleOf(cmd)
		notice := noticeStream(cmd)

		if triggerAmountOv > 0 {
			// An override silently discards the outcome the user asked for:
			// the simulator branches on the amount, so 4000 is what makes
			// `payment.succeeded` succeed. Say so before it looks broken.
			//
			// Straight to stderr, not through notice: --quiet trims the
			// narration, and "the outcome you asked for will not happen" is
			// not narration.
			if !isMagicAmount(triggerAmountOv) {
				fmt.Fprintln(cmd.ErrOrStderr(), sty.err.Warning(fmt.Sprintf(
					"%d is not a magic amount — this will be an ordinary sandbox payment",
					triggerAmountOv)))
			}

			amount = triggerAmountOv
		}

		c, err := client()
		if err != nil {
			return err
		}

		if c.Mode() != "test" {
			return fmt.Errorf("trigger only runs in test mode (REEVIT_MODE=%s)", c.Mode())
		}

		// `trigger` creates a real payment through a real connection. Naming
		// the simulator is what stops "did this just charge someone?".
		//
		// The line is unconditional: api.BootstrapResult reports no "created"
		// flag, so the CLI cannot tell a fresh simulator from an existing one
		// without an API change that is out of this plan's scope.
		fmt.Fprintln(notice, sty.err.Step("Using the sandbox simulator"))

		paymentID, status, err := triggerSimulatorEvent(cmd.Context(), c, event, amount, triggerCurrency)
		if err != nil {
			return err
		}

		fmt.Fprintln(notice, sty.err.Success("Triggered "+event))
		fmt.Fprintln(notice, "  "+sty.err.Note(fmt.Sprintf(
			"payment %s created through the sandbox simulator (status: %s)", paymentID, status)))
		fmt.Fprintln(notice, "  "+sty.err.Note("the outcome resolves asynchronously; watch it land with:"))
		fmt.Fprintln(notice, sty.err.Command("reevit listen --forward-to "+localWebhookURL()))
		fmt.Fprintln(notice, sty.err.Command("reevit payments list"))

		if jsonOut, _ := outputMode(cmd); jsonOut {
			return emitJSON(cmd, triggerDocument{
				Schema:    schemaTrigger,
				Event:     event,
				PaymentID: paymentID,
				Status:    status,
				Amount:    amount,
				Currency:  strings.ToUpper(triggerCurrency),
			})
		}

		// stdout carries the id and nothing else, so `id=$(reevit trigger …)`
		// is a working idiom rather than a string to parse.
		fmt.Fprintln(cmd.OutOrStdout(), paymentID)

		return nil
	},
}

// triggerDocument is `reevit trigger --json`.
//
// `amount` is the amount actually sent, not the one the event maps to: with
// --amount the two differ, and the simulator branches on what was sent. A
// script reconciling this against the dashboard needs the number that was
// charged, and the human path only warns about the difference in prose.
//
// `status` is the intent's status at creation — almost always "pending",
// because the outcome resolves asynchronously. It is here so a consumer does
// not read the absence of a field as "succeeded".
type triggerDocument struct {
	Schema    string `json:"schema"`
	Event     string `json:"event"`
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// isMagicAmount reports whether an amount still drives a simulator outcome.
// Every magic value is one of the documented ones; anything else produces an
// ordinary payment whose result has nothing to do with the requested event.
func isMagicAmount(amount int64) bool {
	for _, magic := range triggerAmounts {
		if magic == amount {
			return true
		}
	}

	return false
}

// localWebhookURL builds the `--forward-to` the user should actually run.
// A generic placeholder makes the suggestion un-pasteable, and the scaffolded
// handler's route is already known whenever init has run here.
func localWebhookURL() string {
	root, err := os.Getwd()
	if err != nil {
		return "http://localhost:3000/api/webhooks/reevit"
	}

	project := scaffold.Detect(root)

	_, path := scaffold.WebhookHandler(project)
	if path == "" {
		path = "/api/webhooks/reevit"
	}

	port := scaffold.DefaultPort(project)
	if port == 0 {
		port = 3000
	}

	return fmt.Sprintf("http://localhost:%d%s", port, path)
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
			"customer_id": "reevit_cli_trigger_" + uuid.NewString(),
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
	projectID := "rvproj_cli_trigger"
	projectName := "reevit-cli-trigger"
	if root, err := os.Getwd(); err == nil {
		project := scaffold.Detect(root)
		if manifest, readErr := scaffold.ReadManifest(project); readErr == nil && manifest.ProjectID != "" {
			projectID = manifest.ProjectID
			projectName = filepath.Base(root)
		}
	}

	result, err := c.BootstrapProject(ctx, api.BootstrapRequest{
		ProjectID: projectID, ProjectName: projectName,
	})
	if err != nil {
		return "", fmt.Errorf("prepare sandbox simulator: %w", err)
	}
	if !result.Simulator.Ready || result.Simulator.ConnectionID == "" {
		return "", fmt.Errorf("prepare sandbox simulator: platform did not report a ready simulator")
	}
	return result.Simulator.ConnectionID, nil
}

func triggerEventNames() []string {
	return append([]string(nil), triggerEventOrder...)
}

// triggerSupportedList renders one event per line with its magic amount, so
// `--amount` stops looking like a free parameter.
func triggerSupportedList() string {
	var b strings.Builder
	for _, name := range triggerEventOrder {
		fmt.Fprintf(&b, "  %-28s %d\n", name, triggerAmounts[name])
	}

	return strings.TrimRight(b.String(), "\n")
}

func init() {
	triggerCmd.Flags().StringVar(&triggerCurrency, "currency", "GHS", "payment currency")
	triggerCmd.Flags().Int64Var(&triggerAmountOv, "amount", 0, "override the magic amount (advanced)")
	rootCmd.AddCommand(triggerCmd)
}
