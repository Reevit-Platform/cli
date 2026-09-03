package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/ui"
)

func TestListenPrefersProjectSigningSecret(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"next":"16"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("REEVIT_WEBHOOK_SECRET=whsec_project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	source, err := resolveListenSecret(command, nil, dir)
	if err != nil {
		t.Fatalf("resolveListenSecret: %v", err)
	}

	if source.secret != "whsec_project" {
		t.Fatalf("secret = %q", source.secret)
	}
	if source.description != "REEVIT_WEBHOOK_SECRET from .env.local" {
		t.Errorf("description = %q, want the file it came from", source.description)
	}
	// A secret the user already has needs no echo, and the header line must
	// name the file so a stale value is findable.
	if source.display != "" {
		t.Errorf("display = %q, want no echo of a secret the user already has", source.display)
	}
	if strings.Contains(out.String(), source.secret) {
		t.Fatalf("project secret was printed: %s", out.String())
	}
}

func TestListenFlagTakesPrecedenceOverProjectSecret(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"next":"16"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("REEVIT_WEBHOOK_SECRET=whsec_project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := listenSecret
	listenSecret = "whsec_flag"
	defer func() { listenSecret = old }()

	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	got, err := resolveListenSecret(command, nil, dir)
	if err != nil {
		t.Fatalf("resolveListenSecret: %v", err)
	}

	if got.secret != "whsec_flag" {
		t.Fatalf("secret = %q, want flag value", got.secret)
	}
	if !strings.Contains(got.description, "--signing-secret") {
		t.Errorf("description = %q, want it to name the flag", got.description)
	}
}

// The signature must verify with the documented production scheme —
// sha256=<hex HMAC-SHA256(raw body, secret)> — so merchant verify code
// written against real webhooks accepts forwarded events unchanged.
func TestForwardedEventsCarryVerifiableSignatures(t *testing.T) {
	type received struct {
		body []byte
		sig  string
	}

	var (
		mu   sync.Mutex
		hits []received
	)

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		mu.Lock()
		hits = append(hits, received{body: body, sig: r.Header.Get("X-Reevit-Signature")})
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	f := &eventForwarder{
		target: local.URL,
		secret: "whsec_test",
		out:    listenCmd,
		httpc:  local.Client(),
	}

	f.handle(api.SSEEvent{Type: "payment.succeeded", Data: `{"type":"payment.succeeded","data":{"id":"pay_1"}}`})

	mu.Lock()
	defer mu.Unlock()

	if len(hits) != 1 {
		t.Fatalf("forwarded %d events, want 1", len(hits))
	}

	if want := SignBody("whsec_test", hits[0].body); hits[0].sig != want {
		t.Fatalf("signature %s does not verify against the raw body (want %s)", hits[0].sig, want)
	}

	var envelope map[string]any
	if err := json.Unmarshal(hits[0].body, &envelope); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}

	for _, key := range []string{"delivery_id", "attempt", "signature_timestamp"} {
		if envelope[key] == nil {
			t.Fatalf("envelope missing %s (production parity)", key)
		}
	}
}

func TestStreamParsesSSEFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: payment.succeeded\ndata: {\"id\":\"pay_1\"}\n\nevent: payment.failed\ndata: {\"id\":\"pay_2\"}\n\n"))
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk", BaseURL: server.URL, Mode: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []api.SSEEvent

	_ = c.Stream(ctx, "/events/stream", func(evt api.SSEEvent) { got = append(got, evt) })

	if len(got) != 2 || got[0].Type != "payment.succeeded" || got[1].Data != `{"id":"pay_2"}` {
		t.Fatalf("parsed %+v", got)
	}
}

// `data: null` decodes without an error and leaves the map nil, so the old
// "only fall back on error" guard let the next write panic and take the whole
// listen session down.
func TestForwarderSurvivesNullEventData(t *testing.T) {
	type received struct {
		body []byte
	}

	var (
		mu   sync.Mutex
		hits []received
	)

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		hits = append(hits, received{body: body})
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	f := newEventForwarder(local.URL, "whsec_test", listenCmd, ui.Styler{}, ui.Styler{}, local.Client())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handle panicked on a null frame: %v", r)
		}
	}()

	f.handle(api.SSEEvent{Type: "ping", Data: "null"})

	mu.Lock()
	defer mu.Unlock()

	if len(hits) != 1 {
		t.Fatalf("forwarded %d events, want 1", len(hits))
	}

	var envelope map[string]any
	if err := json.Unmarshal(hits[0].body, &envelope); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}

	if envelope["type"] != "ping" {
		t.Errorf("type = %v, want ping", envelope["type"])
	}

	if envelope["data"] != "null" {
		t.Errorf("data = %v, want the raw frame", envelope["data"])
	}
}

// A stream that ran for a while was healthy; the drop after it is a new
// incident. Without the reset the backoff ratchets to 30s and every later
// reconnect in the session waits half a minute.
func TestListenResetsBackoffAfterAHealthyStream(t *testing.T) {
	previous := healthyStreamAge
	healthyStreamAge = 10 * time.Millisecond

	t.Cleanup(func() { healthyStreamAge = previous })

	var streams int32

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/events/stream") {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		n := atomic.AddInt32(&streams, 1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Stream 3 stays open past healthyStreamAge, so the drop after it must
		// reset the ratcheted backoff. The others end immediately.
		if n == 3 {
			time.Sleep(80 * time.Millisecond)
		}
	}))
	defer api.Close()

	resetFlags()
	t.Cleanup(resetFlags)

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_backoff.sec")
	t.Setenv("REEVIT_API_URL", api.URL)
	t.Setenv("REEVIT_MODE", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr lockedBuffer

	done := make(chan error, 1)

	go func() {
		done <- ExecuteWith(ctx,
			[]string{"listen", "--forward-to", "http://127.0.0.1:1", "--signing-secret", "whsec_x"},
			strings.NewReader(""), &stdout, &stderr)
	}()

	deadline := time.After(10 * time.Second)

	for atomic.LoadInt32(&streams) < 4 {
		select {
		case err := <-done:
			t.Fatalf("listen returned early: %v", err)
		case <-deadline:
			t.Fatalf("only %d streams opened; stderr:\n%s", atomic.LoadInt32(&streams), stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done

	lines := reconnectDelays(stderr.String())
	if len(lines) < 3 {
		t.Fatalf("reconnect delays = %v, want at least 3:\n%s", lines, stderr.String())
	}

	// 1st drop: 1s (fresh). 2nd: 2s (ratcheted). 3rd: 1s again, because the
	// stream before it ran past healthyStreamAge.
	if lines[0] != "1s" || lines[1] != "2s" || lines[2] != "1s" {
		t.Fatalf("reconnect delays = %v, want [1s 2s 1s …]\n%s", lines, stderr.String())
	}

	if !strings.Contains(stderr.String(), "not replayed") {
		t.Errorf("reconnect did not warn about the unreplayed gap:\n%s", stderr.String())
	}
}

func reconnectDelays(s string) []string {
	var out []string

	for _, line := range strings.Split(s, "\n") {
		_, after, ok := strings.Cut(line, "reconnecting in ")
		if ok {
			out = append(out, strings.TrimSpace(after))
		}
	}

	return out
}

// lockedBuffer lets the test read stderr while the command goroutine writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// Two runs must not reuse delivery ids: a handler that dedupes on delivery id
// dropped every event of the second run.
func TestForwardersUseDistinctDeliveryIDsPerRun(t *testing.T) {
	first := newEventForwarder("http://127.0.0.1:1", "s", listenCmd, ui.Styler{}, ui.Styler{}, &http.Client{})
	second := newEventForwarder("http://127.0.0.1:1", "s", listenCmd, ui.Styler{}, ui.Styler{}, &http.Client{})

	if first.runID == "" || first.runID == second.runID {
		t.Fatalf("run ids %q and %q must differ", first.runID, second.runID)
	}
}

// Anything other than a scope refusal must abort: silently signing with a
// throwaway secret made every forwarded event fail the merchant's check for a
// reason nothing on screen explained.
func TestListenFailsWhenWebhookConfigIsUnreadable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/webhooks/config" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal","message":"boom"}`))

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	command.SetContext(context.Background())

	c := api.New(config.Config{APIKey: "pfk_test_x.sec", BaseURL: server.URL, Mode: "test"})

	_, err := fetchOrMintSecret(command, c)
	if err == nil || !strings.Contains(err.Error(), "webhook config") {
		t.Fatalf("err = %v, want a webhook config failure", err)
	}
}

// A 403 is the one honest reason to mint an ephemeral secret: the key simply
// cannot read the config.
func TestListenMintsEphemeralSecretOnScopeRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"insufficient_scope","message":"missing webhooks:read"}`))
	}))
	defer server.Close()

	var out bytes.Buffer

	command := &cobra.Command{}
	// The secret and the scope notice are conversation: they go to stderr.
	command.SetOut(io.Discard)
	command.SetErr(&out)
	command.SetContext(context.Background())

	c := api.New(config.Config{APIKey: "pfk_test_x.sec", BaseURL: server.URL, Mode: "test"})

	source, err := fetchOrMintSecret(command, c)
	if err != nil {
		t.Fatalf("fetchOrMintSecret: %v", err)
	}

	if !strings.HasPrefix(source.secret, "whsec_local_") {
		t.Fatalf("secret = %q, want an ephemeral one", source.secret)
	}

	// "an ephemeral secret" on its own does not tell the user whether to
	// grant a scope or create a webhook secret. The reason must survive, and
	// the scope name is the only actionable half of it.
	if !strings.Contains(source.description, "no webhook config readable") {
		t.Errorf("description does not say why it is ephemeral: %q", source.description)
	}

	if !strings.Contains(source.description, "webhooks:read") {
		t.Errorf("description does not name the missing scope: %q", source.description)
	}

	// The user cannot verify a signature with a secret they never saw.
	if source.display != source.secret {
		t.Errorf("display = %q, want the ephemeral secret itself", source.display)
	}

	if !strings.Contains(source.guidance, "REEVIT_WEBHOOK_SECRET") {
		t.Errorf("guidance = %q, want it to say where the secret goes", source.guidance)
	}

	// Resolution is silent now: the caller owns the order of the screen, so
	// the secret cannot be printed above the header that explains it.
	if out.String() != "" {
		t.Errorf("secret resolution printed on its own:\n%s", out.String())
	}
}

// `id:` frames are parsed even though the client never sends the resume header
// back — the backend discards it — so adding resume later is a one-line change.
func TestStreamParsesEventIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("id: 42\nevent: payment.succeeded\ndata: {\"id\":\"pay_1\"}\n\n"))
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk", BaseURL: server.URL, Mode: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []api.SSEEvent

	_ = c.Stream(ctx, "/events/stream", func(evt api.SSEEvent) { got = append(got, evt) })

	if len(got) != 1 || got[0].ID != "42" {
		t.Fatalf("parsed %+v, want one event with ID 42", got)
	}
}

// The per-event line is why anyone runs `listen`: it has to say, at a glance,
// whether the handler accepted the delivery. A 4xx that looks exactly like a
// 200 is the failure mode this guards.
func TestForwarderMarksTheHandlerVerdict(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   string
		reject string
	}{
		{name: "2xx is a success", status: http.StatusOK, want: "ok ", reject: "x "},
		{name: "4xx is a failure", status: http.StatusBadRequest, want: "x ", reject: "ok "},
		{name: "5xx is a failure", status: http.StatusInternalServerError, want: "x ", reject: "ok "},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer local.Close()

			var out bytes.Buffer

			command := &cobra.Command{}
			command.SetOut(&out)
			command.SetErr(io.Discard)
			command.SetContext(context.Background())

			f := newEventForwarder(local.URL, "whsec_test", command, ui.Styler{}, ui.Styler{}, local.Client())
			f.handle(api.SSEEvent{Type: "payment.succeeded", Data: `{"id":"pay_1"}`})

			line := out.String()
			if !strings.HasPrefix(line, test.want) {
				t.Fatalf("line = %q, want it to start with %q", line, test.want)
			}

			if strings.HasPrefix(line, test.reject) {
				t.Fatalf("line = %q, must not start with %q", line, test.reject)
			}

			// The arrow degrades with the rest of the vocabulary, so a
			// non-UTF-8 terminal never sees a stray U+2192.
			if strings.Contains(line, "→") {
				t.Fatalf("line = %q, want the ASCII arrow from the plain styler", line)
			}
		})
	}
}

// The event line is a column layout, not a sentence: a long session is only
// readable if the status codes stack up under one another. `%-26s` on the
// event type is what makes that true, so the padding is asserted directly
// rather than inferred from one example.
func TestForwardedEventLinesAlignIntoColumns(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	var out bytes.Buffer

	command := &cobra.Command{}
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetContext(context.Background())

	f := newEventForwarder(local.URL, "whsec_test", command, ui.Styler{}, ui.Styler{}, local.Client())
	f.handle(api.SSEEvent{Type: "payment.succeeded", Data: `{"id":"pay_1"}`})
	f.handle(api.SSEEvent{Type: "refund.created", Data: `{"id":"pay_2"}`})

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q, want two event lines", lines)
	}

	columns := make([]int, 0, 2)

	for _, line := range lines {
		at := strings.Index(line, "> ")
		if at < 0 {
			t.Fatalf("line = %q, want the status marker", line)
		}

		columns = append(columns, at)
	}

	if columns[0] != columns[1] {
		t.Fatalf("status column at %d and %d; the event type is not padded", columns[0], columns[1])
	}

	// Alignment is worthless if it collapses the moment a long event type
	// arrives, so pin the width rather than just the agreement.
	if !strings.Contains(lines[0], "payment.succeeded          > 200") {
		t.Errorf("line = %q, want the event type padded to 26 columns", lines[0])
	}

	// The duration is a scannable column now, not a parenthetical aside.
	if !strings.HasSuffix(lines[0], "ms") || strings.Contains(lines[0], "ms)") {
		t.Errorf("line = %q, want a bare duration column", lines[0])
	}
}

// A red "404" in the status column reads as "Reevit failed". It did not: the
// developer's own handler answered, and the fix is in their code.
func TestForwarderBlamesTheHandlerForNon2xxOnStderr(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		wantErr string
	}{
		{name: "404 is explained", status: http.StatusNotFound, wantErr: "your handler returned 404 for payment.succeeded"},
		{name: "200 says nothing", status: http.StatusOK, wantErr: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer local.Close()

			var stdout, stderr bytes.Buffer

			command := &cobra.Command{}
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			command.SetContext(context.Background())

			f := newEventForwarder(local.URL, "whsec_test", command, ui.Styler{}, ui.Styler{}, local.Client())
			f.handle(api.SSEEvent{Type: "payment.succeeded", Data: `{"id":"pay_1"}`})

			if test.wantErr == "" {
				if stderr.String() != "" {
					t.Fatalf("stderr = %q, want nothing said about a healthy delivery", stderr.String())
				}

				return
			}

			if !strings.Contains(stderr.String(), test.wantErr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantErr)
			}

			// The explanation is commentary. Keeping it out of stdout is what
			// lets `reevit listen > deliveries.log` stay parseable.
			if strings.Contains(stdout.String(), "your handler") {
				t.Fatalf("stdout = %q, must carry only the delivery line", stdout.String())
			}
		})
	}
}

// api.Client.Stream returns nil when the server closes the connection — the
// ordinary outcome of a dev-server restart. Formatting that as a cause used to
// dereference a nil error.
func TestStreamDropCauseNamesACleanCloseWithoutPanicking(t *testing.T) {
	t.Parallel()

	if got := streamDropCause(nil); got != "the server closed the stream" {
		t.Errorf("streamDropCause(nil) = %q", got)
	}

	if got := streamDropCause(errors.New("request GET /events: dial tcp 1.2.3.4:1: refused")); got != "dial tcp 1.2.3.4:1: refused" {
		t.Errorf("streamDropCause = %q, want the transport cause only", got)
	}
}

// `listen` spends most of its life idle, so "connected and waiting" and "hung
// on startup" look identical without a ready line — and the ephemeral secret,
// the one string the user has to copy, used to print above the header that
// explains what they are looking at.
func TestListenOpensWithAHeaderThenTheReadyLine(t *testing.T) {
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/events/stream") {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer stream.Close()

	resetFlags()
	t.Cleanup(resetFlags)

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_header.sec")
	t.Setenv("REEVIT_API_URL", stream.URL)
	t.Setenv("REEVIT_MODE", "")

	// This test runs the real ExecuteWith, so the glyph vocabulary is resolved
	// from the process locale (internal/ui.utf8Locale). Asserting on a glyph
	// without pinning the locale asserts on the developer's environment: this
	// test was written on a machine with LC_ALL, LC_CTYPE and LANG all empty,
	// so it snapshotted the ASCII stand-in and then failed on every CI runner,
	// which sets LANG=C.UTF-8. Pin it to UTF-8, as golden_test.go does, so the
	// assertion names the glyph a user actually sees.
	//
	// Empty is equivalent to unset for both checks — utf8Locale skips empty
	// values, and TERM="" is not "dumb" — so t.Setenv suffices and the cleanup
	// is automatic.
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("TERM", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr lockedBuffer

	done := make(chan error, 1)

	go func() {
		done <- ExecuteWith(ctx,
			[]string{"listen", "--forward-to", "http://127.0.0.1:1", "--signing-secret", "whsec_x"},
			strings.NewReader(""), &stdout, &stderr)
	}()

	deadline := time.After(10 * time.Second)

	for !strings.Contains(stderr.String(), "stream dropped") {
		select {
		case err := <-done:
			t.Fatalf("listen returned early: %v", err)
		case <-deadline:
			t.Fatalf("never reconnected; stderr:\n%s", stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done

	transcript := stderr.String()

	for _, want := range []string{
		"\nReevit listen\n",
		"  Forwarding to   http://127.0.0.1:1\n",
		"  Signing with    the --signing-secret you passed\n",
		"✓ Connected — waiting for test-mode events (Ctrl-C to stop)\n",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("stderr is missing %q:\n%s", want, transcript)
		}
	}

	// Order is the point: a ready line above the header would describe a
	// connection the user cannot yet identify.
	header, ready := strings.Index(transcript, "Reevit listen"), strings.Index(transcript, "Connected")
	if header >= 0 && ready >= 0 && header > ready {
		t.Errorf("the ready line precedes the header:\n%s", transcript)
	}

	// A drop must say what happened and what the CLI will do about it,
	// without a raw Go error on the headline.
	if !strings.Contains(transcript, "! stream dropped — reconnecting in 1s\n") {
		t.Errorf("stderr does not carry the reconnect notice:\n%s", transcript)
	}

	if !strings.Contains(transcript, "the server closed the stream") {
		t.Errorf("stderr does not say why the stream ended:\n%s", transcript)
	}

	// The whole transcript is conversation. Nothing but delivered events is
	// allowed on stdout, which is what makes redirecting it useful.
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want only forwarded-event lines", stdout.String())
	}
}

// jsonForwarderCommand is a forwarder host with --json already set. The real
// flag is persistent on the root; a bare cobra.Command has no flags at all,
// which is how every other forwarder test gets the human rendering.
func jsonForwarderCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{}
	command.Flags().Bool("json", true, "")
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetContext(context.Background())

	return command
}

// NDJSON means one object per line and nothing else on the stream. A consumer
// reads this with `while read -r line; do ... done`, so a wrapped line, a
// blank line or a stray human line breaks the loop rather than the parse.
func TestListenEmitsOneJSONObjectPerDelivery(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	var stdout, stderr bytes.Buffer

	f := newEventForwarder(local.URL, "whsec_test",
		jsonForwarderCommand(&stdout, &stderr), ui.Styler{}, ui.Styler{}, local.Client())

	for _, event := range []string{"payment.succeeded", "payment.failed", "refund.created"} {
		f.handle(api.SSEEvent{Type: event, Data: `{"event":"` + event + `","data":{"id":"pay_1"}}`})
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout has %d lines, want one per delivery:\n%s", len(lines), stdout.String())
	}

	seen := map[string]bool{}

	for i, line := range lines {
		var doc listenEventDocument
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("line %d is not JSON (%v): %s", i, err, line)
		}

		if doc.Schema != schemaListenEvent {
			t.Errorf("line %d schema = %q, want %q", i, doc.Schema, schemaListenEvent)
		}

		if doc.Status == nil || *doc.Status != http.StatusOK {
			t.Errorf("line %d status = %v, want 200", i, doc.Status)
		}

		// null, not omitted: a consumer that branches on `.error` must not
		// have to tell "absent" from "no error".
		if doc.Error != nil {
			t.Errorf("line %d error = %q, want null on a 2xx", i, *doc.Error)
		}

		if _, err := time.Parse(time.RFC3339, doc.Time); err != nil {
			t.Errorf("line %d time = %q, want RFC 3339: %v", i, doc.Time, err)
		}

		// The delivery id is what a handler dedupes on, so two events in one
		// run sharing one would make the second look like a replay.
		if doc.DeliveryID == "" || seen[doc.DeliveryID] {
			t.Errorf("line %d delivery_id = %q, want a fresh one", i, doc.DeliveryID)
		}

		seen[doc.DeliveryID] = true
	}

	// The human column layout would corrupt the same stream it shares.
	if strings.Contains(stdout.String(), "ms\n") && !strings.Contains(stdout.String(), `"duration_ms"`) {
		t.Errorf("stdout still carries the human line:\n%s", stdout.String())
	}
}

// A handler that is not running is the single most common thing `listen`
// reports. Dropping those deliveries from the JSON would make the log agree
// with a successful run.
func TestListenJSONReportsAFailedForwardWithANullStatus(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Port 1 is reserved and never listening.
	f := newEventForwarder("http://127.0.0.1:1", "whsec_test",
		jsonForwarderCommand(&stdout, &stderr), ui.Styler{}, ui.Styler{}, &http.Client{Timeout: 2 * time.Second})

	f.handle(api.SSEEvent{Type: "payment.succeeded", Data: `{"event":"payment.succeeded"}`})

	var doc listenEventDocument
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &doc); err != nil {
		t.Fatalf("stdout is not one JSON object (%v): %q", err, stdout.String())
	}

	if doc.Status != nil {
		t.Errorf("status = %d, want null — nothing answered", *doc.Status)
	}

	if doc.Error == nil || *doc.Error == "" {
		t.Fatalf("error = %v, want the transport failure", doc.Error)
	}

	if doc.Type != "payment.succeeded" {
		t.Errorf("type = %q, want the event named even when it never left", doc.Type)
	}

	// The human sentence keeps its own wording on stderr; --json adds a line
	// to stdout rather than moving the explanation.
	if !strings.Contains(stderr.String(), "forward failed") {
		t.Errorf("stderr = %q, want the human failure line untouched", stderr.String())
	}
}

// stdout is what a script redirects into a file. The event body is the only
// part of a delivery that can hold customer data, and the signing secret is
// the one string that must never be written down by accident.
func TestListenJSONCarriesNeitherTheBodyNorTheSecret(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	var stdout, stderr bytes.Buffer

	const secret = "whsec_local_do_not_log_me"

	f := newEventForwarder(local.URL, secret,
		jsonForwarderCommand(&stdout, &stderr), ui.Styler{}, ui.Styler{}, local.Client())

	f.handle(api.SSEEvent{
		Type: "payment.succeeded",
		Data: `{"event":"payment.succeeded","data":{"customer_email":"ama@example.com","amount":4500}}`,
	})

	for _, leak := range []string{secret, "ama@example.com", "customer_email"} {
		if strings.Contains(stdout.String(), leak) {
			t.Errorf("stdout leaks %q:\n%s", leak, stdout.String())
		}
	}
}

// The ready line is the machine twin of "Connected — waiting for test-mode
// events": without it, a consumer cannot tell an idle stream from a hung one.
// It names where the secret came from, because a handler rejecting every
// event is nearly always verifying against a different one — and it must do
// that without printing the secret onto the stream being logged.
func TestListenReadyLineNamesTheSecretSourceNotTheSecret(t *testing.T) {
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/webhooks/config") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"insufficient_scope","message":"missing webhooks:read"}`))

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer stream.Close()

	resetFlags()
	t.Cleanup(resetFlags)

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_ready.sec")
	t.Setenv("REEVIT_API_URL", stream.URL)
	t.Setenv("REEVIT_MODE", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr lockedBuffer

	done := make(chan error, 1)

	go func() {
		done <- ExecuteWith(ctx,
			[]string{"listen", "--json", "--forward-to", "http://127.0.0.1:1"},
			strings.NewReader(""), &stdout, &stderr)
	}()

	deadline := time.After(10 * time.Second)

	for stdout.String() == "" {
		select {
		case err := <-done:
			t.Fatalf("listen returned early: %v", err)
		case <-deadline:
			t.Fatalf("no ready line; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done

	var ready listenReadyDocument
	if err := json.Unmarshal([]byte(strings.SplitN(stdout.String(), "\n", 2)[0]), &ready); err != nil {
		t.Fatalf("first stdout line is not the ready object (%v):\n%s", err, stdout.String())
	}

	if ready.Schema != schemaListenReady {
		t.Errorf("schema = %q, want %q", ready.Schema, schemaListenReady)
	}

	if ready.ForwardTo != "http://127.0.0.1:1" {
		t.Errorf("forward_to = %q, want the target", ready.ForwardTo)
	}

	if ready.SecretSource != "ephemeral" {
		t.Errorf("secret_source = %q, want ephemeral on a scope refusal", ready.SecretSource)
	}

	// The ephemeral secret is on stderr, where the user needs to copy it —
	// and nowhere near the stream a script is piping to a file.
	if strings.Contains(stdout.String(), "whsec_local_") {
		t.Errorf("stdout carries the ephemeral secret:\n%s", stdout.String())
	}

	if !strings.Contains(stderr.String(), "whsec_local_") {
		t.Errorf("stderr = %q, want the ephemeral secret still shown to the human", stderr.String())
	}
}
