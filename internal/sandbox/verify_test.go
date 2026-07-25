package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyProjectCredentials(t *testing.T) {
	t.Parallel()

	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Header.Get("X-Reevit-Key")+" "+r.URL.Path)
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("verification mutation lacks idempotency key")
		}
		switch r.URL.Path {
		case "/v1/payments/intents":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_1", "status": "succeeded"})
		case "/v1/checkout/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_2", "session_secret": "cs_usable"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if _, err := VerifyServerPayment(context.Background(), server.URL, "pfk_test_server.secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCheckout(context.Background(), server.URL, "pfk_test_checkout.secret"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 ||
		calls[0] != "pfk_test_server.secret /v1/payments/intents" ||
		calls[1] != "pfk_test_checkout.secret /v1/checkout/sessions" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestVerifyCheckoutRequiresUsableSecret(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_2"})
	}))
	defer server.Close()

	if _, err := VerifyCheckout(context.Background(), server.URL, "pfk_test_checkout.secret"); err == nil {
		t.Fatal("VerifyCheckout accepted a session without a client/session secret")
	}
}
