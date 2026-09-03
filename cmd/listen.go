package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

var (
	listenForwardTo string
	listenSecret    string
)

// healthyStreamAge is how long a stream must have run before a drop counts as
// a fresh incident rather than a continuing outage. A package var so tests can
// shorten it.
var healthyStreamAge = 30 * time.Second

// listenGapNotice is printed on every reconnect. The backend discards
// Last-Event-ID, so events emitted while the CLI was disconnected are gone —
// saying so beats letting the user assume the gap was replayed.
const listenGapNotice = "  events emitted while disconnected are not replayed — resend them from the dashboard"

var listenCmd = &cobra.Command{
	Use:   "listen --forward-to <url>",
	Short: "Stream test-mode events to a local endpoint with valid signatures",
	Long: `Subscribes to your account's live test-mode event stream and forwards each
event to your local endpoint, signed exactly like production webhooks
(X-Reevit-Signature: sha256=<hex HMAC-SHA256 of the raw body>), so your
verification code runs unchanged.

The signing secret comes from --signing-secret, then the current project's
REEVIT_WEBHOOK_SECRET, then your webhook configuration. Only the final
fallback is ephemeral.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if listenForwardTo == "" {
			return fmt.Errorf("--forward-to is required, e.g. --forward-to http://localhost:3000/webhooks")
		}

		c, err := client()
		if err != nil {
			return err
		}

		if c.Mode() != "test" {
			return fmt.Errorf("listen only runs in test mode (REEVIT_MODE=%s)", c.Mode())
		}

		root, _ := os.Getwd()

		secret, err := resolveListenSecret(cmd, c, root)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Forwarding test-mode events to %s\n", listenForwardTo)

		forwarder := newEventForwarder(listenForwardTo, secret, cmd, &http.Client{Timeout: 15 * time.Second})

		// Reconnect with backoff: local dev streams drop on hot reloads.
		backoff := time.Second

		for {
			connectedAt := time.Now()

			err := c.Stream(cmd.Context(), "/events/stream?mode=test", forwarder.handle)
			if cmd.Context().Err() != nil {
				return ExitError{Code: 130, Err: context.Canceled}
			}

			// A stream that ran for a while was healthy; the drop is a new
			// incident, not a continuation of an earlier outage. Without this
			// the backoff ratchets to 30s and stays there for the session.
			if time.Since(connectedAt) > healthyStreamAge {
				backoff = time.Second
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "stream dropped (%v) — reconnecting in %s\n", err, backoff)
			fmt.Fprintln(cmd.ErrOrStderr(), listenGapNotice)

			select {
			case <-cmd.Context().Done():
				return ExitError{Code: 130, Err: context.Canceled}
			case <-time.After(backoff):
			}

			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	},
}

func resolveListenSecret(cmd *cobra.Command, c *api.Client, root string) (string, error) {
	if listenSecret != "" {
		return listenSecret, nil
	}
	if root != "" {
		project := scaffold.Detect(root)
		if project.Stack != scaffold.StackUnknown {
			if secret := scaffold.ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET"); secret != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Using signing secret from "+scaffold.EnvFileName(project)+".")
				return secret, nil
			}
		}
	}
	if c != nil {
		return fetchOrMintSecret(cmd, c)
	}
	return "", nil
}

// fetchOrMintSecret prefers the org's real signing secret so existing verify
// code works unchanged.
//
// Only a scope refusal earns the ephemeral fallback: any other failure (the
// API is down, the key is dead, the URL is wrong) used to be swallowed into a
// throwaway secret, so every forwarded event failed the merchant's signature
// check for a reason nothing on screen explained.
func fetchOrMintSecret(cmd *cobra.Command, c *api.Client) (string, error) {
	var cfg struct {
		SigningSecret string `json:"signing_secret"`
	}

	err := c.Do(cmd.Context(), api.Request{Path: "/webhooks/config"}, &cfg)

	switch {
	case err == nil && cfg.SigningSecret != "":
		fmt.Fprintln(cmd.OutOrStdout(), "Signing with your account's webhook secret.")

		return cfg.SigningSecret, nil
	case err == nil:
		fmt.Fprintln(cmd.OutOrStdout(), "Your account has no webhook signing secret configured.")
	default:
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			return "", fmt.Errorf("read webhook config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Your key cannot read the webhook config (needs webhooks:read).")
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate ephemeral secret: %w", err)
	}

	secret := "whsec_local_" + hex.EncodeToString(raw)

	fmt.Fprintf(cmd.OutOrStdout(), "Signing with an ephemeral secret:\n  %s\n", secret)

	return secret, nil
}

// envelopeFor decodes an event's data into the envelope the delivery
// enrichment is written into, falling back to a wrapper when the frame is not
// a JSON object. A literal `null` frame decodes without error *and* leaves the
// map nil, so the nil check is not redundant — writing to it would panic.
func envelopeFor(evt api.SSEEvent) map[string]any {
	var envelope map[string]any

	if err := json.Unmarshal([]byte(evt.Data), &envelope); err != nil || envelope == nil {
		// Production names the event in `event`. `type` is kept alongside it
		// for one release so handlers built against the old fallback envelope
		// keep working.
		return map[string]any{"event": evt.Type, "type": evt.Type, "data": evt.Data}
	}

	return envelope
}

type eventForwarder struct {
	target string
	secret string
	out    *cobra.Command
	httpc  *http.Client
	// runID makes delivery ids unique per process. A handler that dedupes on
	// delivery id — the production-correct behaviour — silently dropped every
	// event of the second `reevit listen` run when the ids restarted at 1.
	runID    string
	delivery int
}

func newEventForwarder(target, secret string, out *cobra.Command, httpc *http.Client) *eventForwarder {
	return &eventForwarder{
		target: target,
		secret: secret,
		out:    out,
		httpc:  httpc,
		runID:  uuid.NewString()[:8],
	}
}

func (f *eventForwarder) handle(evt api.SSEEvent) {
	f.delivery++

	// Mirror the production delivery envelope enrichment: delivery id +
	// attempt + signature timestamp live in the SIGNED body.
	envelope := envelopeFor(evt)

	deliveryID := "evtd_local_" + f.runID + "_" + strconv.Itoa(f.delivery)
	envelope["delivery_id"] = deliveryID
	envelope["attempt"] = 1
	envelope["signature_timestamp"] = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(f.out.ErrOrStderr(), "encode event: %v\n", err)

		return
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.target, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(f.out.ErrOrStderr(), "build forward request: %v\n", err)

		return
	}

	// A real delivery envelope carries `event`; only the fallback above and
	// older payloads carry `type`.
	eventType, _ := envelope["event"].(string)
	if eventType == "" {
		eventType, _ = envelope["type"].(string)
	}

	if eventType == "" {
		eventType = evt.Type
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reevit-Signature", SignBody(f.secret, body))
	req.Header.Set("X-Reevit-Delivery-ID", deliveryID)
	req.Header.Set("X-Reevit-Delivery-Attempt", "1")
	req.Header.Set("X-Reevit-Mode", "sandbox")
	req.Header.Set("X-Reevit-Signature-Timestamp", envelope["signature_timestamp"].(string))

	started := time.Now()

	resp, err := f.httpc.Do(req)
	if err != nil {
		fmt.Fprintf(f.out.ErrOrStderr(), "%s → forward failed: %v\n", eventType, err)

		return
	}

	_ = resp.Body.Close()

	fmt.Fprintf(f.out.OutOrStdout(), "%s  %s → %d (%dms)\n",
		time.Now().Format("15:04:05"), eventType, resp.StatusCode, time.Since(started).Milliseconds())
}

// SignBody produces the production webhook signature for a payload:
// sha256=<hex HMAC-SHA256(body, secret)>.
func SignBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func init() {
	listenCmd.Flags().StringVar(&listenForwardTo, "forward-to", "", "local endpoint to POST events to (required)")
	listenCmd.Flags().StringVar(&listenSecret, "secret", "", "override the signing secret")
	listenCmd.Flags().StringVar(&listenSecret, "signing-secret", "", "override the signing secret")
	rootCmd.AddCommand(listenCmd)
}
