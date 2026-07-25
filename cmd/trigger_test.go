package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

// The trigger amounts must mirror the backend simulator's MagicOutcomes table
// (adapters/psp/stub/magic.go) — this pins the contract.
func TestTriggerAmountsMatchSimulatorContract(t *testing.T) {
	want := map[string]int64{
		"payment.succeeded":          4000,
		"payment.failed":             4001,
		"payment.insufficient_funds": 4002,
		"payment.timeout":            4003,
		"payment.provider_downtime":  4004,
	}

	for event, amount := range want {
		if triggerAmounts[event] != amount {
			t.Fatalf("%s = %d, want %d", event, triggerAmounts[event], amount)
		}
	}

	if len(triggerAmounts) != len(want) {
		t.Fatalf("trigger table has %d entries, want %d", len(triggerAmounts), len(want))
	}
}

func TestEnsureSimulatorConnection(t *testing.T) {
	var bootstrap api.BootstrapRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/cli/bootstrap" {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("bootstrap must carry an Idempotency-Key")
		}
		_ = json.NewDecoder(r.Body).Decode(&bootstrap)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"simulator": map[string]any{"connection_id": "conn_sim_1", "ready": true},
		})
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk_test", BaseURL: server.URL, Mode: "test"})

	id, err := ensureSimulatorConnection(context.Background(), c)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if id != "conn_sim_1" {
		t.Fatalf("id = %s", id)
	}

	if bootstrap.ProjectID == "" || bootstrap.ProjectName == "" || len(bootstrap.Capabilities) != 0 {
		t.Fatalf("bootstrap = %#v; trigger should request only simulator setup", bootstrap)
	}
}

func TestTriggerSimulatorEventsUseDistinctCustomers(t *testing.T) {
	t.Parallel()

	var customerIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/bootstrap":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"simulator": map[string]any{"connection_id": "conn_sim_1", "ready": true},
			})
		case "/v1/payments/intents":
			var body struct {
				CustomerID string `json:"customer_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode trigger body: %v", err)
			}
			customerIDs = append(customerIDs, body.CustomerID)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_1", "status": "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk_test", BaseURL: server.URL, Mode: "test"})
	for range 2 {
		if _, _, err := triggerSimulatorEvent(
			context.Background(), c, "payment.succeeded", 4000, "GHS",
		); err != nil {
			t.Fatalf("trigger simulator event: %v", err)
		}
	}

	if len(customerIDs) != 2 || customerIDs[0] == "" || customerIDs[1] == "" ||
		customerIDs[0] == customerIDs[1] {
		t.Fatalf("trigger customer IDs must be distinct and non-empty: %#v", customerIDs)
	}
}

func TestClientSendsAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reevit-Key") != "rk_test_abc" {
			t.Errorf("missing api key header")
		}

		if r.Header.Get("X-Reevit-Mode") != "test" {
			t.Errorf("missing mode header")
		}

		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk_test_abc", BaseURL: server.URL, Mode: "test"})
	if err := c.Do(context.Background(), api.Request{Path: "/payments"}, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
}

func TestClientSurfacesAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "insufficient_scope", "message": "missing payments:write"})
	}))
	defer server.Close()

	c := api.New(config.Config{APIKey: "rk", BaseURL: server.URL, Mode: "test"})

	err := c.Do(context.Background(), api.Request{Path: "/payments"}, nil)

	apiErr, ok := err.(*api.APIError)
	if !ok || apiErr.Status != 403 || apiErr.Code != "insufficient_scope" {
		t.Fatalf("err = %v", err)
	}
}
