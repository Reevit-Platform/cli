package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

var (
	listenForwardTo string
	listenSecret    string
)

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
		secret := resolveListenSecret(cmd, c, root)

		fmt.Fprintf(cmd.OutOrStdout(), "Forwarding test-mode events to %s\n", listenForwardTo)

		forwarder := &eventForwarder{target: listenForwardTo, secret: secret, out: cmd, httpc: &http.Client{Timeout: 15 * time.Second}}

		// Reconnect with backoff: local dev streams drop on hot reloads.
		backoff := time.Second

		for {
			err := c.Stream(cmd.Context(), "/events/stream?mode=test", forwarder.handle)
			if cmd.Context().Err() != nil {
				return nil
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "stream dropped (%v) — reconnecting in %s\n", err, backoff)

			select {
			case <-cmd.Context().Done():
				return nil
			case <-time.After(backoff):
			}

			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	},
}

func resolveListenSecret(cmd *cobra.Command, c *api.Client, root string) string {
	if listenSecret != "" {
		return listenSecret
	}
	if root != "" {
		project := scaffold.Detect(root)
		if project.Stack != scaffold.StackUnknown {
			if secret := scaffold.ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET"); secret != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Using signing secret from "+scaffold.EnvFileName(project)+".")
				return secret
			}
		}
	}
	if c != nil {
		return fetchOrMintSecret(cmd, c)
	}
	return ""
}

// fetchOrMintSecret prefers the org's real signing secret so existing verify
// code works unchanged; narrow-scoped keys get an ephemeral printed secret.
func fetchOrMintSecret(cmd *cobra.Command, c *api.Client) string {
	var cfg struct {
		SigningSecret string `json:"signing_secret"`
	}

	if err := c.Do(cmd.Context(), api.Request{Path: "/webhooks/config"}, &cfg); err == nil && cfg.SigningSecret != "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Signing with your account's webhook secret.")

		return cfg.SigningSecret
	}

	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	secret := "whsec_local_" + hex.EncodeToString(raw)

	fmt.Fprintf(cmd.OutOrStdout(), "No webhook config readable — signing with ephemeral secret:\n  %s\n", secret)

	return secret
}

type eventForwarder struct {
	target   string
	secret   string
	out      *cobra.Command
	httpc    *http.Client
	delivery int
}

func (f *eventForwarder) handle(evt api.SSEEvent) {
	f.delivery++

	// Mirror the production delivery envelope enrichment: delivery id +
	// attempt + signature timestamp live in the SIGNED body.
	var envelope map[string]any
	if err := json.Unmarshal([]byte(evt.Data), &envelope); err != nil {
		envelope = map[string]any{"type": evt.Type, "data": evt.Data}
	}

	deliveryID := "evtd_local_" + strconv.Itoa(f.delivery)
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

	eventType, _ := envelope["type"].(string)
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
