package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

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
