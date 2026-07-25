package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	secret := resolveListenSecret(command, nil, dir)
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
	if got := resolveListenSecret(command, nil, dir); got != "whsec_flag" {
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
