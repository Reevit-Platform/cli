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
	"github.com/Reevit-Platform/cli/internal/ui"
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
const listenGapNotice = "events emitted while disconnected are not replayed — resend them from the dashboard"

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
	Example: `  reevit listen --forward-to http://localhost:3000/api/webhooks/reevit
  reevit listen --forward-to http://localhost:8000/webhooks/reevit --signing-secret whsec_...`,
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

		// The stream itself is this command's data; everything it says about
		// the stream is conversation and belongs on stderr.
		sty := styleOf(cmd)
		notice := noticeStream(cmd)

		source, err := resolveListenSecret(cmd, c, root)
		if err != nil {
			return err
		}

		// Header first. Secret resolution used to print as it went, which put
		// the ephemeral secret — the one string the user has to copy — above
		// the header that says what they are looking at.
		fmt.Fprintln(notice, sty.err.Heading("Reevit listen"))
		fmt.Fprintln(notice)
		fmt.Fprintf(notice, "  Forwarding to   %s\n", sty.err.URL(listenForwardTo))
		fmt.Fprintf(notice, "  Signing with    %s\n", source.description)

		if source.display != "" {
			fmt.Fprintln(notice, sty.err.Command(source.display))
			fmt.Fprintln(notice, "  "+sty.err.Step(source.guidance))
		}

		// Without a ready line, "connected and idle" and "hung" look identical,
		// and `listen` is idle by design for most of its life.
		fmt.Fprintln(notice)
		fmt.Fprintln(notice, sty.err.Success("Connected — waiting for test-mode events (Ctrl-C to stop)"))

		// The machine-readable twin of the ready line. A consumer reading
		// NDJSON off stdout has the same problem the human does — "connected
		// and idle" and "hung" look identical — and it cannot see the line
		// above, which is on stderr.
		//
		// It carries where events are going and where the secret came from,
		// never the secret itself: this stream is what a script pipes into a
		// log file.
		if jsonOut, _ := outputMode(cmd); jsonOut {
			if err := emitJSON(cmd, listenReadyDocument{
				Schema:       schemaListenReady,
				ForwardTo:    listenForwardTo,
				SecretSource: source.kind,
			}); err != nil {
				return err
			}
		}

		forwarder := newEventForwarder(
			listenForwardTo, source.secret, cmd, sty.out, sty.err, &http.Client{Timeout: 15 * time.Second},
		)

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

			// The reconnect block stays on stderr even under --quiet: a
			// silent gap in a forwarded event log is the one thing a user
			// tailing this must not be allowed to mistake for quiet traffic.
			dropped := cmd.ErrOrStderr()

			fmt.Fprintln(dropped, sty.err.Warning(fmt.Sprintf("stream dropped — reconnecting in %s", backoff)))
			fmt.Fprintln(dropped, "  "+sty.err.Dim(streamDropCause(err)))
			fmt.Fprintln(dropped, "  "+sty.err.Dim(listenGapNotice))

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

// listenSecretSource is what the header needs to say about signing. Resolution
// used to print as it went, so the ephemeral secret — the one string the user
// has to copy — landed above the header explaining what they were looking at.
// Returning the description instead lets the caller order the screen.
type listenSecretSource struct {
	secret      string
	kind        string // flag|env|account|ephemeral|none — for --json
	description string // fills the "Signing with" line
	display     string // the secret itself, when the user has to copy it
	guidance    string // what to do with it
}

// listenReadyDocument is the first line of `listen --json`: the stream is up.
//
// `secret_source` says where the signing key came from, because a handler
// that rejects every event is nearly always verifying against a different
// secret than the one signing. The secret itself is never here — NDJSON on
// stdout is what a script redirects into a file.
type listenReadyDocument struct {
	Schema       string `json:"schema"`
	ForwardTo    string `json:"forward_to"`
	SecretSource string `json:"secret_source"`
}

// listenEventDocument is one delivery.
//
// `status` and `error` are pointers so both are always present and each can be
// null: a delivery that never reached the handler has no status, and one that
// did has no error. `omitempty` would have made a 0-status transport failure
// indistinguishable from a missing field.
//
// The event body is deliberately absent. It is already POSTed to the
// handler — the one place it is needed — and it is the only part of a
// delivery that can contain customer data.
type listenEventDocument struct {
	Schema     string  `json:"schema"`
	Time       string  `json:"time"`
	Type       string  `json:"type"`
	DeliveryID string  `json:"delivery_id"`
	Status     *int    `json:"status"`
	DurationMS int64   `json:"duration_ms"`
	Error      *string `json:"error"`
}

// streamDropCause names why the stream ended. Stream returns a nil error when
// the server closes the connection cleanly, which is the common case behind a
// dev-server restart — and has no message of its own to print.
func streamDropCause(err error) string {
	if err == nil {
		return "the server closed the stream"
	}

	return shortDialCause(err)
}

func resolveListenSecret(cmd *cobra.Command, c *api.Client, root string) (listenSecretSource, error) {
	if listenSecret != "" {
		return listenSecretSource{
			secret:      listenSecret,
			kind:        "flag",
			description: "the --signing-secret you passed",
		}, nil
	}
	if root != "" {
		project := scaffold.Detect(root)
		if project.Stack != scaffold.StackUnknown {
			if secret := scaffold.ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET"); secret != "" {
				return listenSecretSource{
					secret:      secret,
					kind:        "env",
					description: "REEVIT_WEBHOOK_SECRET from " + scaffold.EnvFileName(project),
				}, nil
			}
		}
	}
	if c != nil {
		return fetchOrMintSecret(cmd, c)
	}
	return listenSecretSource{kind: "none", description: "nothing — no secret resolved"}, nil
}

// fetchOrMintSecret prefers the org's real signing secret so existing verify
// code works unchanged.
//
// Only a scope refusal earns the ephemeral fallback: any other failure (the
// API is down, the key is dead, the URL is wrong) used to be swallowed into a
// throwaway secret, so every forwarded event failed the merchant's signature
// check for a reason nothing on screen explained.
func fetchOrMintSecret(cmd *cobra.Command, c *api.Client) (listenSecretSource, error) {
	var cfg struct {
		SigningSecret string `json:"signing_secret"`
	}

	err := c.Do(cmd.Context(), api.Request{Path: "/webhooks/config"}, &cfg)

	// Why the secret is ephemeral belongs on the header line: "ephemeral"
	// alone does not tell the user whether to grant a scope or create a
	// secret, and those are different afternoons.
	var why string

	switch {
	case err == nil && cfg.SigningSecret != "":
		return listenSecretSource{
			secret:      cfg.SigningSecret,
			kind:        "account",
			description: "your account's webhook secret",
		}, nil
	case err == nil:
		why = "no webhook secret configured for this account"
	default:
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			return listenSecretSource{}, fmt.Errorf("read webhook config: %w", err)
		}

		why = "no webhook config readable — needs webhooks:read"
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return listenSecretSource{}, fmt.Errorf("generate ephemeral secret: %w", err)
	}

	secret := "whsec_local_" + hex.EncodeToString(raw)

	return listenSecretSource{
		secret:      secret,
		kind:        "ephemeral",
		description: "an ephemeral secret (" + why + ")",
		display:     secret,
		guidance:    "Put it in REEVIT_WEBHOOK_SECRET so your handler can verify these events",
	}, nil
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
	sty    ui.Styler
	// errSty styles the stderr commentary. The event lines are this
	// command's data and stay on stdout; why a delivery failed is
	// conversation and must not corrupt a piped log.
	errSty ui.Styler
	httpc  *http.Client
	// runID makes delivery ids unique per process. A handler that dedupes on
	// delivery id — the production-correct behaviour — silently dropped every
	// event of the second `reevit listen` run when the ids restarted at 1.
	runID    string
	delivery int
	// jsonOut swaps the stdout line for an NDJSON object. Read once at
	// construction: the flag cannot change while the stream is running, and
	// re-reading it per delivery would let a half-JSON log exist.
	jsonOut bool
}

func newEventForwarder(
	target, secret string,
	out *cobra.Command,
	sty ui.Styler,
	errSty ui.Styler,
	httpc *http.Client,
) *eventForwarder {
	forwarder := &eventForwarder{
		target: target,
		secret: secret,
		out:    out,
		sty:    sty,
		errSty: errSty,
		httpc:  httpc,
		runID:  uuid.NewString()[:8],
	}

	forwarder.jsonOut, _ = outputMode(out)

	return forwarder
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

	// A real delivery envelope carries `event`; only the fallback above and
	// older payloads carry `type`. Resolved before the first failure exit so
	// that every NDJSON line names its event, including the ones that never
	// left the process.
	eventType, _ := envelope["event"].(string)
	if eventType == "" {
		eventType, _ = envelope["type"].(string)
	}

	if eventType == "" {
		eventType = evt.Type
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(f.out.ErrOrStderr(), "encode event: %v\n", err)
		f.emitFailure(eventType, deliveryID, 0, err)

		return
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.target, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(f.out.ErrOrStderr(), "build forward request: %v\n", err)
		f.emitFailure(eventType, deliveryID, 0, err)

		return
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
		fmt.Fprintf(f.out.ErrOrStderr(), "%s %s forward failed: %v\n", eventType, marker(f.sty.Step("")), err)
		f.emitFailure(eventType, deliveryID, time.Since(started), err)

		return
	}

	_ = resp.Body.Close()

	elapsed := time.Since(started)

	if f.jsonOut {
		f.emitEvent(eventType, deliveryID, &resp.StatusCode, elapsed, nil)
	} else {
		// %-26s so the status codes line up into a column the eye can scan; a
		// ragged right edge is what makes a long `listen` session unreadable.
		line := fmt.Sprintf("%s  %-26s %s %d  %dms",
			time.Now().Format("15:04:05"), eventType, marker(f.sty.Step("")),
			resp.StatusCode, elapsed.Milliseconds())

		// The handler's own verdict: a 2xx is the whole point of `listen`, and
		// anything else is the failure the developer is here to see.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			line = f.sty.Success(line)
		} else {
			line = f.sty.Failure(line)
		}

		fmt.Fprintln(f.out.OutOrStdout(), line)
	}

	// A bare 404 in the status column reads as "Reevit failed". Naming the
	// handler as the author of the status points the fix at the right file.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintln(f.out.ErrOrStderr(),
			"  "+f.errSty.Dim(fmt.Sprintf("your handler returned %d for %s", resp.StatusCode, eventType)))
	}
}

// emitFailure records a delivery that never got a status back.
//
// Under --json only: the human path already printed its own sentence, in the
// wording plan 031 chose, and this exists so the NDJSON log has one line per
// event rather than a silent gap where the failures were. A consumer counting
// lines against the dashboard would otherwise conclude the events never
// arrived.
func (f *eventForwarder) emitFailure(eventType, deliveryID string, elapsed time.Duration, cause error) {
	if !f.jsonOut {
		return
	}

	f.emitEvent(eventType, deliveryID, nil, elapsed, cause)
}

func (f *eventForwarder) emitEvent(
	eventType, deliveryID string,
	status *int,
	elapsed time.Duration,
	cause error,
) {
	doc := listenEventDocument{
		Schema:     schemaListenEvent,
		Time:       time.Now().UTC().Format(time.RFC3339),
		Type:       eventType,
		DeliveryID: deliveryID,
		Status:     status,
		DurationMS: elapsed.Milliseconds(),
	}

	if cause != nil {
		message := cause.Error()
		doc.Error = &message
	}

	// An encoder failure here would be a broken stdout, which the next line
	// would hit too; there is nowhere useful to report it that is not the
	// stream we just failed to write.
	_ = emitJSON(f.out, doc)
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
	// Two flags for one variable is a compatibility alias, not a choice the
	// user should have to make. Hide the older spelling so --help offers one.
	_ = listenCmd.Flags().MarkHidden("secret")
	rootCmd.AddCommand(listenCmd)
}
