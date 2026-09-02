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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

// verifyingHandler mimics the scaffolded webhook templates: sha256=hex
// HMAC-SHA256 of the raw body, 401 on mismatch.
func verifyingHandler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Reevit-Signature"))) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)

			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func TestCheckWebhookEndToEndAgainstVerifyingHandler(t *testing.T) {
	server := httptest.NewServer(verifyingHandler("whsec_doctor"))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "whsec_doctor")

	if res.failures != 0 {
		t.Fatalf("verifying handler must pass both checks; output:\n%s", buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "signed test event accepted") || !strings.Contains(out, "tampered event rejected") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

// TestCheckWebhookEndToEndProbeNamesTheEventInEvent pins the probe payload to
// the production envelope. A handler generated from the fixed templates reads
// `event`; a probe that only sent `type` would let such a handler fall through
// to its default branch and still pass, which is the exact blindness that let
// the wrong-field bug ship.
func TestCheckWebhookEndToEndProbeNamesTheEventInEvent(t *testing.T) {
	var seen struct {
		Event string `json:"event"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)

		mac := hmac.New(sha256.New, []byte("whsec_doctor"))
		mac.Write(payload)

		if !hmac.Equal([]byte("sha256="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Reevit-Signature"))) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)

			return
		}

		if err := json.Unmarshal(payload, &seen); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)

			return
		}

		// Dispatch the way the generated handlers now do: on `event` only.
		if seen.Event != "payment.succeeded" {
			http.Error(w, "unknown event", http.StatusBadRequest)

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "whsec_doctor")

	if res.failures != 0 {
		t.Fatalf("a handler that reads only `event` must pass; output:\n%s", buf.String())
	}

	if seen.Event != "payment.succeeded" {
		t.Fatalf("probe payload event = %q, want payment.succeeded", seen.Event)
	}
}

func TestCheckWebhookEndToEndCatchesWrongSecret(t *testing.T) {
	server := httptest.NewServer(verifyingHandler("the_real_secret"))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "a_different_secret")

	if res.failures == 0 {
		t.Fatalf("mismatched secret must fail the signed check; output:\n%s", buf.String())
	}
}

func TestCheckWebhookEndToEndCatchesNoVerification(t *testing.T) {
	// A handler that accepts everything — the tampered check must flag it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "whsec_doctor")

	if res.failures == 0 {
		t.Fatal("accept-everything handler must fail the tampered-signature check")
	}

	if !strings.Contains(buf.String(), "TAMPERED event was accepted") {
		t.Errorf("unexpected output:\n%s", buf.String())
	}
}

func TestCheckWebhookEndToEndUnreachableServer(t *testing.T) {
	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, "http://127.0.0.1:1", "whsec")

	if res.failures == 0 {
		t.Fatal("unreachable server must be reported as a failure")
	}
}

func TestCheckBootstrapStatusPassesCompleteProject(t *testing.T) {
	manifest := scaffold.Manifest{
		ProjectID: "rvproj_test", ServerKeyID: "pfk_test_server",
		CheckoutKeyID: "pfk_test_checkout", Origin: "http://localhost:5173",
	}
	var status api.BootstrapResult
	status.Project.ID = manifest.ProjectID
	status.Mode = "test"
	status.Credentials.Server = &api.BootstrapCredential{
		ID: manifest.ServerKeyID, Scopes: []string{"payments:read", "payments:write"},
	}
	status.Credentials.Checkout = &api.BootstrapCredential{
		ID: manifest.CheckoutKeyID, Scopes: []string{"checkout:write"},
	}
	status.Simulator.Ready = true
	status.Checkout.OriginAllowed = true

	var buf bytes.Buffer
	res := &doctorResult{}
	checkBootstrapStatus(&buf, res, manifest, status)
	if res.failures != 0 {
		t.Fatalf("complete project should pass; output:\n%s", buf.String())
	}
}

func TestCheckBootstrapStatusRejectsMismatchedCredential(t *testing.T) {
	manifest := scaffold.Manifest{ProjectID: "rvproj_test", ServerKeyID: "pfk_test_expected"}
	var status api.BootstrapResult
	status.Project.ID = manifest.ProjectID
	status.Mode = "test"
	status.Credentials.Server = &api.BootstrapCredential{ID: "pfk_test_other"}
	status.Simulator.Ready = true

	var buf bytes.Buffer
	res := &doctorResult{}
	checkBootstrapStatus(&buf, res, manifest, status)
	if res.failures == 0 {
		t.Fatalf("mismatched credential must fail; output:\n%s", buf.String())
	}
}

func TestCheckAppURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var buf bytes.Buffer
	res := &doctorResult{}
	checkAppURL(context.Background(), &buf, res, server.URL)
	if res.failures != 0 {
		t.Fatalf("reachable app should pass; output:\n%s", buf.String())
	}
}

func TestManifestCapabilitiesDistinguishCheckoutOnlyProjects(t *testing.T) {
	t.Parallel()

	manifest := scaffold.Manifest{Capabilities: []string{"checkout"}}
	if manifestHasCapability(manifest, "server") || manifestHasCapability(manifest, "webhook") {
		t.Fatal("checkout-only manifest unexpectedly requires server or webhook configuration")
	}
	if !manifestHasCapability(manifest, "checkout") {
		t.Fatal("checkout capability was not detected")
	}
}

func TestOptionalAppCheckPrintsExactDevCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	res := &doctorResult{}
	checkOptionalAppURL(
		context.Background(), &out, res,
		"http://127.0.0.1:1/reevit-demo", []string{"pnpm", "dev"},
	)
	if res.warnings != 1 || !strings.Contains(out.String(), "`pnpm dev`") {
		t.Fatalf("output = %q warnings=%d", out.String(), res.warnings)
	}
}

func TestRunningInCIEnablesStrictMode(t *testing.T) {
	t.Setenv("CI", "true")
	if !runningInCI() {
		t.Fatal("CI=true did not enable strict doctor behavior")
	}
}

func TestPythonSDKProbeUsesDetectedEnvironmentManager(t *testing.T) {
	t.Parallel()

	tests := map[scaffold.Installer]string{
		scaffold.InstallerUV:     "uv run python -c import reevit",
		scaffold.InstallerPoetry: "poetry run python -c import reevit",
		scaffold.InstallerPipenv: "pipenv run python -c import reevit",
		scaffold.InstallerPip:    "python -c import reevit",
	}
	for installer, want := range tests {
		if got := strings.Join(pythonSDKProbeCommand(installer), " "); got != want {
			t.Errorf("%s probe = %q, want %q", installer, got, want)
		}
	}
}

// doctor reused one delivery id for every run, so a handler that dedupes on
// delivery id — the production-correct behaviour — was reported as broken.
func TestDoctorSendsAUniqueDeliveryIDPerCall(t *testing.T) {
	var (
		mu  sync.Mutex
		ids []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ids = append(ids, r.Header.Get("X-Reevit-Delivery-ID"))
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	evt := api.SSEEvent{Type: "payment.succeeded", Data: `{"type":"payment.succeeded"}`}

	for range 2 {
		if _, err := forwardPlatformEvent(context.Background(), server.URL, "whsec", evt); err != nil {
			t.Fatalf("forwardPlatformEvent: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(ids) != 2 {
		t.Fatalf("got %d deliveries, want 2", len(ids))
	}

	if ids[0] == ids[1] {
		t.Fatalf("both deliveries used %q — ids must be unique", ids[0])
	}

	for _, id := range ids {
		if !strings.HasPrefix(id, "evtd_doctor_") || id == "evtd_doctor_e2e" {
			t.Fatalf("delivery id = %q", id)
		}
	}
}

// The old 16-slot channel with a `default:` discard threw away the matching
// event whenever the account was busy enough to fill it first.
func TestDoctorE2ESinkKeepsTheMatchBehindABusyStream(t *testing.T) {
	events := make(chan api.SSEEvent, 16)
	sink := &e2eEventSink{out: events}

	// 40 unrelated events arrive before the payment id is known, then the
	// matching one.
	for i := range 40 {
		sink.accept(api.SSEEvent{Type: "other", Data: fmt.Sprintf(`{"id":"pay_other_%d"}`, i)})
	}

	sink.accept(api.SSEEvent{Type: "payment.succeeded", Data: `{"id":"pay_target"}`})

	sink.arm("pay_target")

	select {
	case got := <-events:
		if !strings.Contains(got.Data, "pay_target") {
			t.Fatalf("delivered %q, want the matching event", got.Data)
		}
	default:
		t.Fatal("the matching event never reached the consumer")
	}

	if n := sink.unrelated.Load(); n != 40 {
		t.Errorf("unrelated = %d, want 40 — the timeout message reports this", n)
	}
}

// Events arriving after arm are filtered in the callback, so a firehose of
// unrelated traffic can no longer evict the one event that matters.
func TestDoctorE2ESinkFiltersAfterArming(t *testing.T) {
	events := make(chan api.SSEEvent, 16)
	sink := &e2eEventSink{out: events}

	sink.arm("pay_target")

	for i := range 40 {
		sink.accept(api.SSEEvent{Type: "other", Data: fmt.Sprintf(`{"id":"pay_other_%d"}`, i)})
	}

	sink.accept(api.SSEEvent{Type: "payment.succeeded", Data: `{"id":"pay_target"}`})

	select {
	case got := <-events:
		if !strings.Contains(got.Data, "pay_target") {
			t.Fatalf("delivered %q", got.Data)
		}
	default:
		t.Fatal("the matching event never reached the consumer")
	}

	if n := sink.unrelated.Load(); n != 40 {
		t.Errorf("unrelated = %d, want 40", n)
	}
}
