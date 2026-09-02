package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// triggerStubAPI stands in for the platform: bootstrap reports a ready
// simulator, and the intent comes back as a pending payment.
func triggerStubAPI(t *testing.T, amounts *[]int64) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/bootstrap":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"simulator": map[string]any{"connection_id": "conn_sim_1", "ready": true},
			})
		case "/v1/payments/intents":
			var body struct {
				Amount int64 `json:"amount"`
			}

			_ = json.NewDecoder(r.Body).Decode(&body)

			if amounts != nil {
				*amounts = append(*amounts, body.Amount)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pay_01JQTEST", "status": "pending"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	t.Cleanup(server.Close)

	return server
}

func runTrigger(t *testing.T, server *httptest.Server, args ...string) (stdout, stderr string) {
	t.Helper()

	resetFlags()
	t.Cleanup(resetFlags)

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_trigger.sec")
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_MODE", "")
	t.Setenv("REEVIT_TELEMETRY", "0")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")

	var out, errOut bytes.Buffer

	if err := ExecuteWith(context.Background(), args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("trigger: %v\nstderr:\n%s", err, errOut.String())
	}

	return out.String(), errOut.String()
}

// `id=$(reevit trigger payment.succeeded)` has to yield an id, not a sentence.
// Everything the command says about what it did is conversation and belongs on
// stderr, where a redirect leaves it alone.
func TestTriggerPutsOnlyThePaymentIDOnStdout(t *testing.T) {
	server := triggerStubAPI(t, nil)

	stdout, stderr := runTrigger(t, server, "trigger", "payment.succeeded")

	if stdout != "pay_01JQTEST\n" {
		t.Fatalf("stdout = %q, want exactly the payment id and a newline", stdout)
	}

	for _, want := range []string{
		"> Using the sandbox simulator\n",
		"ok Triggered payment.succeeded\n",
		"- payment pay_01JQTEST created through the sandbox simulator (status: pending)\n",
		"- the outcome resolves asynchronously; watch it land with:\n",
		"reevit listen --forward-to http://localhost:3000/api/webhooks/reevit\n",
		"reevit payments list\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}

	// "Triggered payment.succeeded" on stdout would end up inside the id.
	if strings.Contains(stdout, "Triggered") {
		t.Errorf("stdout = %q, want no commentary", stdout)
	}
}

// The simulator branches on the amount, so an override silently discards the
// outcome the user asked for. 4000 is what makes payment.succeeded succeed.
func TestTriggerWarnsWhenAnOverrideStopsBeingMagic(t *testing.T) {
	for _, test := range []struct {
		name     string
		amount   string
		wantWarn bool
	}{
		{name: "a non-magic override is called out", amount: "1234", wantWarn: true},
		{name: "another outcome's magic amount is not", amount: "4003", wantWarn: false},
		{name: "the event's own magic amount is not", amount: "4000", wantWarn: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var amounts []int64

			server := triggerStubAPI(t, &amounts)

			_, stderr := runTrigger(t, server, "trigger", "payment.succeeded", "--amount", test.amount)

			warned := strings.Contains(stderr, "is not a magic amount — this will be an ordinary sandbox payment")
			if warned != test.wantWarn {
				t.Fatalf("warned = %v, want %v; stderr:\n%s", warned, test.wantWarn, stderr)
			}

			if test.wantWarn && !strings.Contains(stderr, "! "+test.amount+" is not a magic amount") {
				t.Errorf("the warning does not quote the amount:\n%s", stderr)
			}

			// The override still has to reach the API — the warning explains
			// the consequence, it does not veto the request.
			if len(amounts) != 1 || strconv.FormatInt(amounts[0], 10) != test.amount {
				t.Errorf("amounts = %v, want the override to be sent", amounts)
			}
		})
	}
}

// A generic placeholder makes the suggestion un-pasteable. Whenever init has
// scaffolded a handler here, its real route is already known.
func TestLocalWebhookURLPrefersTheScaffoldedHandler(t *testing.T) {
	dir := t.TempDir()

	for rel, body := range map[string]string{
		"package.json":                     `{"dependencies":{"next":"16"},"scripts":{"dev":"next dev"}}`,
		"app/api/webhooks/reevit/route.ts": "export async function POST() {}",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chdir(previous) })

	if got := localWebhookURL(); got != "http://localhost:3000/api/webhooks/reevit" {
		t.Errorf("localWebhookURL() = %q, want the detected handler route", got)
	}

	// An empty directory is not a project, and the suggestion still has to be
	// something the user can paste and edit.
	empty := t.TempDir()
	if err := os.Chdir(empty); err != nil {
		t.Fatal(err)
	}

	if got := localWebhookURL(); got != "http://localhost:3000/api/webhooks/reevit" {
		t.Errorf("localWebhookURL() = %q outside a project, want a pasteable default", got)
	}
}

func TestIsMagicAmountCoversEveryDocumentedOutcome(t *testing.T) {
	t.Parallel()

	for event, amount := range triggerAmounts {
		if !isMagicAmount(amount) {
			t.Errorf("%s's amount %d is not recognised as magic", event, amount)
		}
	}

	for _, amount := range []int64{0, 1, 3999, 4005, 100000} {
		if isMagicAmount(amount) {
			t.Errorf("%d is not a documented magic amount", amount)
		}
	}
}
