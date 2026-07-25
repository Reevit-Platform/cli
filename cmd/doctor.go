package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/sandbox"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

var (
	doctorWebhookURL string
	doctorAppURL     string
	doctorE2E        bool
	doctorStrict     bool
)

// doctorResult tallies outcomes so the command can exit non-zero on failures.
type doctorResult struct {
	failures int
	warnings int
}

func (r *doctorResult) pass(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "  ✔ "+format+"\n", args...)
}

func (r *doctorResult) fail(out io.Writer, format string, args ...any) {
	r.failures++

	fmt.Fprintf(out, "  ✖ "+format+"\n", args...)
}

func (r *doctorResult) warn(out io.Writer, format string, args ...any) {
	r.warnings++

	fmt.Fprintf(out, "  • "+format+"\n", args...)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that Reevit is set up correctly in this project",
	Long: `Verifies the whole local setup: your CLI credentials against the API, the
project's env wiring, the installed SDK, and — when your dev server is
running — that your webhook handler accepts a correctly signed event AND
rejects a tampered one.

Webhook check:
  reevit doctor --webhook-url http://localhost:3000/api/webhooks/reevit

It signs a synthetic event with the REEVIT_WEBHOOK_SECRET from your env file,
so the check proves your handler's signature verification end to end.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		res := &doctorResult{}

		// --- 1. CLI credentials ---
		fmt.Fprintln(out, "\nCLI credentials")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if cfg.APIKey == "" {
			res.fail(out, "no API key configured — run `reevit login`")
		} else {
			mode := "test"
			if strings.HasPrefix(cfg.APIKey, "pfk_live") {
				mode = "LIVE"
			}

			res.pass(out, "API key configured (%s mode)", mode)

			var probe any
			if err := api.New(cfg).Do(cmd.Context(), api.Request{Path: "/payments"}, &probe); err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.Status == 401 {
					res.fail(out, "the API rejected your key — run `reevit login` for a fresh one")
				} else if apiErr, ok := err.(*api.APIError); ok && apiErr.Status == 403 {
					res.pass(out, "key authenticates (narrow scopes — some commands may be limited)")
				} else {
					res.warn(out, "could not reach the API to verify the key (%v)", err)
				}
			} else {
				res.pass(out, "key authenticates against %s", cfg.BaseURL)
			}
		}

		// --- 2. Project files ---
		fmt.Fprintln(out, "\nProject files")

		root, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}

		project := scaffold.Detect(root)
		if project.Stack == scaffold.StackUnknown {
			res.fail(out, "no project detected here — run doctor from your project root")
			printDoctorSummary(out, res)

			return fmt.Errorf("doctor found %d problem(s)", res.failures)
		}

		res.pass(out, "%s project", project.Stack)

		manifest, manifestErr := scaffold.ReadManifest(project)
		switch {
		case manifestErr != nil:
			res.fail(out, "cannot read .reevit/manifest.json (%v)", manifestErr)
		case manifest.ProjectID == "":
			res.fail(out, "project manifest is missing — run `reevit init`")
		case manifest.Status != "complete":
			res.fail(out, "project setup is %q — rerun `reevit init` to complete it", manifest.Status)
		default:
			res.pass(out, "project manifest complete (%s)", manifest.ProjectID)
		}

		if pkg, installed := scaffold.SDKPackageFor(project); pkg != "" {
			if !installed && project.Stack == scaffold.StackPython {
				probe := pythonSDKProbeCommand(project.Installer)
				check := exec.CommandContext(cmd.Context(), probe[0], probe[1:]...)
				check.Dir = project.Root
				installed = check.Run() == nil
			}
			if installed {
				res.pass(out, "SDK installed (%s)", pkg)
			} else {
				res.fail(out, "SDK not installed — run `reevit init` or add %s", pkg)
			}
		}

		fmt.Fprintln(out, "  Environment: "+scaffold.EnvFileName(project))

		envKey := scaffold.ReadEnvValue(project, "REEVIT_API_KEY")
		handlerFile, handlerPath := scaffold.WebhookHandler(project)
		hasServer := manifestHasCapability(manifest, "server") || manifest.ServerKeyID != ""
		hasWebhook := manifestHasCapability(manifest, "webhook") || handlerFile != ""

		switch {
		case !hasServer:
			// Checkout-only projects intentionally have no server credential.
		case envKey == "":
			res.fail(out, "REEVIT_API_KEY is not set — run `reevit init` to wire it")
		case strings.HasPrefix(envKey, "pfk_"):
			res.pass(out, "REEVIT_API_KEY set")
		default:
			res.warn(out, "REEVIT_API_KEY doesn't look like a Reevit key (expected pfk_…)")
		}

		if scaffold.ReadEnvValue(project, "REEVIT_ORG_ID") == "" {
			res.warn(out, "REEVIT_ORG_ID is not set — SDK clients need it alongside the key")
		} else {
			res.pass(out, "REEVIT_ORG_ID set")
		}

		clientVar := scaffold.ClientKeyVar(project.Stack)
		checkoutKey := scaffold.ReadEnvValue(project, clientVar)
		if cfg.APIKey != "" && (cfg.APIKey == envKey || (checkoutKey != "" && cfg.APIKey == checkoutKey)) {
			res.fail(out, "CLI login credential is also used as a project key — run `reevit login`, then `reevit init --rotate-test-keys`")
		}

		webhookSecret := scaffold.ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET")
		if hasWebhook && webhookSecret == "" {
			res.fail(out, "REEVIT_WEBHOOK_SECRET is empty — rerun `reevit init` to wire the webhook handler")
		} else if hasWebhook {
			res.pass(out, "REEVIT_WEBHOOK_SECRET set")
		}

		// The checkout templates read a browser-exposed variable whose name is
		// a framework convention (NEXT_PUBLIC_* / VITE_*) — a plain REEVIT_*
		// var never reaches the client bundle. Checked whenever checkout usage
		// is implied: a scaffolded component (any path may have moved) or a
		// frontend Reevit SDK in the dependencies.
		if clientVar != "" {
			sdkPkg, sdkInstalled := scaffold.SDKPackageFor(project)
			frontendSDK := sdkInstalled && (sdkPkg == "@reevit/react" || sdkPkg == "@reevit/vue" || sdkPkg == "@reevit/svelte")

			if scaffold.CheckoutComponent(project) != "" || frontendSDK {
				if scaffold.ReadEnvValue(project, clientVar) == "" {
					res.fail(out, "%s is not set — checkout components read it (browser-side); rerun `reevit init` or add it", clientVar)
				} else {
					res.pass(out, "%s set (browser-exposed checkout key)", clientVar)
				}
			}
		}

		// --- 4. Platform bootstrap ---
		fmt.Fprintln(out, "\nPlatform sandbox")
		if manifest.ProjectID != "" && cfg.APIKey != "" {
			status, statusErr := api.New(cfg).BootstrapStatus(cmd.Context(), manifest.ProjectID, manifest.Origin)
			if statusErr != nil {
				res.fail(out, "could not verify project bootstrap (%v)", statusErr)
			} else {
				checkBootstrapStatus(out, res, manifest, status)
				if manifest.CheckoutKeyID != "" && checkoutKey != "" {
					if _, verifyErr := sandbox.VerifyCheckout(cmd.Context(), cfg.BaseURL, checkoutKey); verifyErr != nil {
						res.fail(out, "checkout credential could not initialize a sandbox session (%v)", verifyErr)
					} else {
						res.pass(out, "checkout credential initialized a sandbox session")
					}
				}
				if manifest.ServerKeyID != "" && envKey != "" {
					payment, verifyErr := sandbox.VerifyServerPayment(cmd.Context(), cfg.BaseURL, envKey)
					if verifyErr != nil {
						res.fail(out, "server credential could not create a sandbox payment (%v)", verifyErr)
					} else if payment.PaymentState != "" && payment.PaymentState != "succeeded" {
						res.fail(out, "sandbox verification payment returned %s", payment.PaymentState)
					} else {
						res.pass(out, "server credential completed a simulator payment")
					}
				}
			}
		} else {
			res.warn(out, "platform bootstrap check skipped until login and init are complete")
		}

		// --- 5. Generated files ---
		if len(manifest.GeneratedFiles) > 0 {
			for _, rel := range manifest.GeneratedFiles {
				if _, statErr := os.Stat(filepath.Join(project.Root, rel)); statErr != nil {
					res.fail(out, "generated file is missing: %s — rerun `reevit init`", rel)
				}
			}
		}

		// --- 6. Runnable checkout ---
		fmt.Fprintln(out, "\nRunning application")
		if doctorAppURL != "" {
			checkAppURL(cmd.Context(), out, res, doctorAppURL)
		} else if demoPath := scaffold.DemoPath(project); demoPath != "" {
			origin := manifest.Origin
			if origin == "" {
				origin = fmt.Sprintf("http://localhost:%d", scaffold.DefaultPort(project))
			}
			checkOptionalAppURL(
				cmd.Context(), out, res,
				strings.TrimRight(origin, "/")+demoPath,
				scaffold.DevCommand(project),
			)
		} else {
			res.pass(out, "no browser demo selected")
		}

		// --- 7. Webhook handler ---
		fmt.Fprintln(out, "\nSigned webhooks")

		if handlerFile != "" {
			res.pass(out, "handler found at %s", handlerFile)
		} else if hasWebhook {
			res.fail(out, "configured webhook handler is missing — rerun `reevit init`")
		} else {
			res.pass(out, "webhook integration not selected")
		}

		switch {
		case doctorWebhookURL == "" && hasWebhook:
			res.warn(out, "live check skipped — start your dev server and run:\n      reevit doctor --webhook-url http://localhost:<port>%s", handlerPath)
		case doctorWebhookURL == "":
			// nothing to check against
		case webhookSecret == "":
			res.fail(out, "cannot run the live check: REEVIT_WEBHOOK_SECRET is empty (the handler would reject every event)")
		default:
			checkWebhookEndToEnd(cmd.Context(), out, res, doctorWebhookURL, webhookSecret)

			if doctorE2E {
				fmt.Fprintln(out, "\nEnd-to-end (simulator → platform → your handler)")
				checkWebhookE2E(cmd, out, res, doctorWebhookURL, webhookSecret)
			}
		}

		if doctorE2E && doctorWebhookURL == "" {
			res.fail(out, "--e2e needs --webhook-url so the platform event has somewhere to land")
		}

		printDoctorSummary(out, res)

		strict := doctorStrict || runningInCI()
		if res.failures > 0 || (strict && res.warnings > 0) {
			if res.failures == 0 {
				return fmt.Errorf("doctor found %d warning(s) in strict mode", res.warnings)
			}
			return fmt.Errorf("doctor found %d problem(s)", res.failures)
		}

		return nil
	},
}

func pythonSDKProbeCommand(installer scaffold.Installer) []string {
	probe := []string{"python", "-c", "import reevit"}
	switch installer {
	case scaffold.InstallerUV:
		return append([]string{"uv", "run"}, probe...)
	case scaffold.InstallerPoetry:
		return append([]string{"poetry", "run"}, probe...)
	case scaffold.InstallerPipenv:
		return append([]string{"pipenv", "run"}, probe...)
	default:
		return probe
	}
}

func manifestHasCapability(manifest scaffold.Manifest, capability string) bool {
	return slices.Contains(manifest.Capabilities, capability)
}

func checkBootstrapStatus(out io.Writer, res *doctorResult, manifest scaffold.Manifest, status api.BootstrapResult) {
	if status.Mode != "test" {
		res.fail(out, "project is not in test mode")
	} else {
		res.pass(out, "test mode active")
	}

	if status.Project.ID != manifest.ProjectID {
		res.fail(out, "platform project identity does not match the local manifest")
	}
	if manifest.ServerKeyID != "" {
		if status.Credentials.Server == nil || status.Credentials.Server.ID != manifest.ServerKeyID ||
			!sameScopes(status.Credentials.Server.Scopes, []string{"payments:read", "payments:write"}) {
			res.fail(out, "server credential is missing, revoked, or does not match the manifest")
		} else {
			res.pass(out, "server credential active with payments:read/write")
		}
	}
	if manifest.CheckoutKeyID != "" {
		if status.Credentials.Checkout == nil || status.Credentials.Checkout.ID != manifest.CheckoutKeyID ||
			!sameScopes(status.Credentials.Checkout.Scopes, []string{"checkout:write"}) {
			res.fail(out, "checkout credential is missing, revoked, or does not match the manifest")
		} else {
			res.pass(out, "browser credential active with checkout:write only")
		}
	}
	if status.Simulator.Ready {
		res.pass(out, "sandbox simulator ready")
	} else {
		res.fail(out, "sandbox simulator is not ready")
	}
	if manifest.Origin != "" {
		if status.Checkout.OriginAllowed {
			res.pass(out, "checkout origin allowed (%s)", manifest.Origin)
		} else {
			res.fail(out, "checkout origin is not allowed (%s)", manifest.Origin)
		}
	}
}

func sameScopes(got, want []string) bool {
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func checkAppURL(ctx context.Context, out io.Writer, res *doctorResult, target string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		res.fail(out, "invalid --app-url (%v)", err)
		return
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		res.fail(out, "could not reach checkout demo at %s (%v)", target, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		res.pass(out, "checkout demo reachable (%d)", resp.StatusCode)
	} else {
		res.fail(out, "checkout demo returned %d", resp.StatusCode)
	}
}

func checkOptionalAppURL(
	ctx context.Context,
	out io.Writer,
	res *doctorResult,
	target string,
	devCommand []string,
) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		res.warn(out, "could not construct checkout demo URL %s (%v)", target, err)
		return
	}
	resp, err := (&http.Client{Timeout: 1500 * time.Millisecond}).Do(req)
	if err != nil {
		if len(devCommand) > 0 {
			res.warn(
				out,
				"checkout app is not running — start it with `%s`, then rerun `reevit doctor --strict` (%s)",
				strings.Join(devCommand, " "), target,
			)
		} else {
			res.warn(out, "checkout app is not running — start it, then rerun `reevit doctor --strict` (%s)", target)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		res.pass(out, "checkout demo reachable (%d)", resp.StatusCode)
	} else {
		res.warn(out, "checkout demo returned %d at %s", resp.StatusCode, target)
	}
}

func runningInCI() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CI")))
	return value != "" && value != "0" && value != "false"
}

// checkWebhookEndToEnd proves the handler's signature verification both ways:
// a correctly signed synthetic event must be accepted, and the same payload
// with a tampered signature must be rejected.
func checkWebhookEndToEnd(ctx context.Context, out io.Writer, res *doctorResult, url, secret string) {
	payload := []byte(fmt.Sprintf(
		`{"type":"payment.succeeded","data":{"id":"doctor_check","amount":100,"currency":"GHS"},"created_at":%q}`,
		time.Now().UTC().Format(time.RFC3339),
	))

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	goodSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	status, err := postWebhook(ctx, url, payload, goodSig)

	switch {
	case err != nil:
		res.fail(out, "could not reach %s (%v) — is your dev server running?", url, err)

		return
	case status >= 200 && status < 300:
		res.pass(out, "signed test event accepted (%d)", status)
	default:
		res.fail(out, "signed test event REJECTED (%d) — check that the handler reads REEVIT_WEBHOOK_SECRET and verifies the raw body", status)
	}

	// Tampered signature must NOT be accepted.
	status, err = postWebhook(ctx, url, payload, "sha256="+strings.Repeat("0", 64))

	switch {
	case err != nil:
		res.warn(out, "tampered-signature check could not run (%v)", err)
	case status >= 200 && status < 300:
		res.fail(out, "TAMPERED event was accepted (%d) — the handler is not verifying signatures", status)
	default:
		res.pass(out, "tampered event rejected (%d)", status)
	}
}

// checkWebhookE2E proves the ENTIRE pipeline: a real sandbox payment through
// the simulator produces a platform-generated event, which is delivered to
// the handler with a production-shaped envelope and signature. This is what
// production deliveries will look like, end to end.
func checkWebhookE2E(cmd *cobra.Command, out io.Writer, res *doctorResult, targetURL, secret string) {
	c, err := client()
	if err != nil {
		res.fail(out, "e2e needs a logged-in CLI: %v", err)

		return
	}

	if c.Mode() != "test" {
		res.fail(out, "the e2e check only runs in test mode (REEVIT_MODE=%s)", c.Mode())

		return
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
	defer cancel()

	events := make(chan api.SSEEvent, 16)
	streamErr := make(chan error, 1)

	go func() {
		streamErr <- c.Stream(ctx, "/events/stream?mode=test", func(evt api.SSEEvent) {
			select {
			case events <- evt:
			default:
			}
		})
	}()

	// Let the stream establish before creating the payment, so the resulting
	// events can't race past us.
	select {
	case err := <-streamErr:
		res.fail(out, "could not open the event stream (%v)", err)

		return
	case <-time.After(1500 * time.Millisecond):
	}

	paymentID, _, err := triggerSimulatorEvent(ctx, c, "payment.succeeded", triggerAmounts["payment.succeeded"], "GHS")
	if err != nil {
		res.fail(out, "could not create a sandbox payment via the simulator (%v)", err)

		return
	}

	res.pass(out, "sandbox payment created through the real pipeline (%s)", paymentID)

	for {
		select {
		case <-ctx.Done():
			res.fail(out, "timed out waiting for the platform event (90s) — check `reevit listen` works for this account")

			return
		case err := <-streamErr:
			res.fail(out, "event stream dropped before the event arrived (%v)", err)

			return
		case evt := <-events:
			if !strings.Contains(evt.Data, paymentID) {
				continue
			}

			status, err := forwardPlatformEvent(ctx, targetURL, secret, evt)

			switch {
			case err != nil:
				res.fail(out, "could not deliver the platform event to %s (%v)", targetURL, err)
			case status >= 200 && status < 300:
				res.pass(out, "platform event delivered and accepted by your handler (%d)", status)
			default:
				res.fail(out, "your handler rejected the real platform event (%d) — its envelope differs from the synthetic one; check your parsing", status)
			}

			return
		}
	}
}

// forwardPlatformEvent wraps a streamed event in the production delivery
// envelope (delivery id, attempt, signature timestamp inside the SIGNED
// body) and POSTs it — the same shape `reevit listen` and real deliveries use.
func forwardPlatformEvent(ctx context.Context, targetURL, secret string, evt api.SSEEvent) (int, error) {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(evt.Data), &envelope); err != nil {
		envelope = map[string]any{"type": evt.Type, "data": evt.Data}
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	envelope["delivery_id"] = "evtd_doctor_e2e"
	envelope["attempt"] = 1
	envelope["signature_timestamp"] = ts

	body, err := json.Marshal(envelope)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reevit-Signature", SignBody(secret, body))
	req.Header.Set("X-Reevit-Delivery-ID", "evtd_doctor_e2e")
	req.Header.Set("X-Reevit-Delivery-Attempt", "1")
	req.Header.Set("X-Reevit-Mode", "sandbox")
	req.Header.Set("X-Reevit-Signature-Timestamp", ts)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

func postWebhook(ctx context.Context, url string, payload []byte, signature string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reevit-Signature", signature)

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

func printDoctorSummary(out io.Writer, res *doctorResult) {
	fmt.Fprintln(out)

	switch {
	case res.failures > 0:
		fmt.Fprintf(out, "%d problem(s) found — fix the ✖ items above and rerun `reevit doctor`.\n", res.failures)
	case res.warnings > 0:
		fmt.Fprintf(out, "Setup looks good (%d note(s) above).\n", res.warnings)
	default:
		fmt.Fprintln(out, "Everything checks out.")
	}
}

func init() {
	doctorCmd.Flags().StringVar(&doctorWebhookURL, "webhook-url", "", "your running app's webhook endpoint, e.g. http://localhost:3000/api/webhooks/reevit")
	doctorCmd.Flags().StringVar(&doctorAppURL, "app-url", "", "your running checkout demo URL, e.g. http://localhost:3000/reevit-demo")
	doctorCmd.Flags().BoolVar(&doctorE2E, "e2e", false, "also fire a REAL sandbox payment through the simulator and deliver the resulting platform event to --webhook-url")
	doctorCmd.Flags().BoolVar(&doctorStrict, "strict", false, "treat warnings as failures (useful in CI)")

	rootCmd.AddCommand(doctorCmd)
}
