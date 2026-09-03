package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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

	secret, err := resolveListenSecret(command, nil, dir)
	if err != nil {
		t.Fatalf("resolveListenSecret: %v", err)
	}

	if secret != "whsec_project" {
		t.Fatalf("secret = %q", secret)
	}
	if strings.Contains(out.String(), secret) {
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

	if got != "whsec_flag" {
		t.Fatalf("secret = %q, want flag value", got)
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

	f := newEventForwarder(local.URL, "whsec_test", listenCmd, local.Client())

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
	first := newEventForwarder("http://127.0.0.1:1", "s", listenCmd, &http.Client{})
	second := newEventForwarder("http://127.0.0.1:1", "s", listenCmd, &http.Client{})

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
	command.SetOut(&out)
	command.SetContext(context.Background())

	c := api.New(config.Config{APIKey: "pfk_test_x.sec", BaseURL: server.URL, Mode: "test"})

	secret, err := fetchOrMintSecret(command, c)
	if err != nil {
		t.Fatalf("fetchOrMintSecret: %v", err)
	}

	if !strings.HasPrefix(secret, "whsec_local_") {
		t.Fatalf("secret = %q, want an ephemeral one", secret)
	}

	if !strings.Contains(out.String(), "webhooks:read") {
		t.Errorf("output does not name the missing scope:\n%s", out.String())
	}

	if !strings.Contains(out.String(), secret) {
		t.Errorf("the ephemeral secret must be printed:\n%s", out.String())
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
