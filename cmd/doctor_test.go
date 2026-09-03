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
	"time"

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
	// The dev command is the remedy, so it is rendered on its own line as
	// something to type — not quoted inside the sentence, where it has to be
	// picked out of the prose before it can be copied.
	if res.warnings != 1 || !strings.Contains(out.String(), "\n    > pnpm dev\n") {
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

// timestampCheckingHandler is a handler generated from the fixed templates: it
// verifies the signature and then rejects a delivery whose signature_timestamp
// is outside the tolerance window.
func timestampCheckingHandler(secret string, tolerance time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)

		if !hmac.Equal([]byte("sha256="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Reevit-Signature"))) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)

			return
		}

		var envelope struct {
			SignatureTimestamp string `json:"signature_timestamp"`
		}

		if err := json.Unmarshal(payload, &envelope); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)

			return
		}

		at, err := time.Parse(time.RFC3339, envelope.SignatureTimestamp)
		if err != nil || time.Since(at).Abs() > tolerance {
			http.Error(w, "stale signature", http.StatusBadRequest)

			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

// TestCheckWebhookEndToEndProbesStaleTimestamps — the probe payload must carry
// the replay fields production signs into the body, and a handler that honours
// them must come out clean.
func TestCheckWebhookEndToEndProbesStaleTimestamps(t *testing.T) {
	server := httptest.NewServer(timestampCheckingHandler("whsec_doctor", 5*time.Minute))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "whsec_doctor")

	out := buf.String()

	if res.failures != 0 {
		t.Fatalf("a timestamp-checking handler must pass every probe; output:\n%s", out)
	}

	if res.warnings != 0 {
		t.Fatalf("a timestamp-checking handler must not be warned about; output:\n%s", out)
	}

	if !strings.Contains(out, "stale event rejected") {
		t.Errorf("missing the stale-timestamp pass line:\n%s", out)
	}
}

// TestCheckWebhookEndToEndWarnsOnMissingTimestampCheck — a handler with no
// timestamp check is warned, not failed: older scaffolds and hand-written
// handlers legitimately lack one, and --strict already turns warnings into a
// failed run.
func TestCheckWebhookEndToEndWarnsOnMissingTimestampCheck(t *testing.T) {
	server := httptest.NewServer(verifyingHandler("whsec_doctor"))
	defer server.Close()

	var buf bytes.Buffer

	res := &doctorResult{}
	checkWebhookEndToEnd(context.Background(), &buf, res, server.URL, "whsec_doctor")

	out := buf.String()

	if res.failures != 0 {
		t.Fatalf("a missing timestamp check must not fail the run; output:\n%s", out)
	}

	if res.warnings != 1 {
		t.Fatalf("warnings = %d, want exactly 1; output:\n%s", res.warnings, out)
	}

	for _, want := range []string{
		"handler accepted a 20-minute-old signature; it has no replay window",
		// The regeneration command is the remedy, on its own line.
		"\n    > reevit init --overwrite\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// TestDoctorProbePayloadCarriesTheReplayFields pins the envelope shape: the
// dispatcher signs delivery_id, attempt and signature_timestamp into the body,
// so a probe without them is thinner than anything production sends.
func TestDoctorProbePayloadCarriesTheReplayFields(t *testing.T) {
	sentAt := time.Now().Add(-20 * time.Minute)

	payload, deliveryID, timestamp := doctorProbePayload(sentAt)

	var envelope struct {
		Event              string `json:"event"`
		Type               string `json:"type"`
		DeliveryID         string `json:"delivery_id"`
		Attempt            int    `json:"attempt"`
		SignatureTimestamp string `json:"signature_timestamp"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("probe payload is not valid JSON: %v\n%s", err, payload)
	}

	if envelope.Event != "payment.succeeded" || envelope.Type != "payment.succeeded" {
		t.Errorf("event = %q, type = %q", envelope.Event, envelope.Type)
	}

	if envelope.DeliveryID != deliveryID || envelope.DeliveryID == "" {
		t.Errorf("delivery_id = %q, want %q", envelope.DeliveryID, deliveryID)
	}

	if envelope.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", envelope.Attempt)
	}

	if envelope.SignatureTimestamp != timestamp {
		t.Errorf("signature_timestamp = %q, want %q", envelope.SignatureTimestamp, timestamp)
	}

	at, err := time.Parse(time.RFC3339, envelope.SignatureTimestamp)
	if err != nil {
		t.Fatalf("signature_timestamp is not RFC3339: %v", err)
	}

	if delta := at.Sub(sentAt).Abs(); delta > time.Second {
		t.Errorf("signature_timestamp is %s away from the requested time", delta)
	}

	// Two probes must never share a delivery id — a handler that dedupes on
	// it would drop the second and doctor would report it as broken.
	if _, second, _ := doctorProbePayload(sentAt); second == deliveryID {
		t.Errorf("delivery ids repeat: %q", second)
	}
}

// The JSON document is assembled while the run streams, so the order in the
// struct is the order on screen. A consumer reading `.sections[2]` and a human
// reading the third heading have to be looking at the same thing.
func TestDoctorResultCollectsSectionsAndStatusesInOrder(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{}

	res.section(&out, "Credentials")
	res.pass(&out, "API key configured (%s mode)", "test")
	res.failr(&out, "reevit login", "the API rejected your key")

	res.section(&out, "Environment (.env.local)")
	res.skip("REEVIT_API_KEY not checked (this project has no server credential)")
	res.warnr(&out, "reevit init", "REEVIT_ORG_ID is not set")

	want := []doctorSection{
		{Name: "Credentials", Checks: []doctorCheck{
			{Status: "pass", Message: "API key configured (test mode)"},
			{Status: "fail", Message: "the API rejected your key", Remedy: "reevit login"},
		}},
		{Name: "Environment (.env.local)", Checks: []doctorCheck{
			{Status: "skip", Message: "REEVIT_API_KEY not checked (this project has no server credential)"},
			{Status: "warn", Message: "REEVIT_ORG_ID is not set", Remedy: "reevit init"},
		}},
	}

	if diff := fmt.Sprintf("%+v", res.sections); diff != fmt.Sprintf("%+v", want) {
		t.Errorf("sections =\n%+v\nwant\n%+v", res.sections, want)
	}

	if res.failures != 1 || res.warnings != 1 {
		t.Errorf("failures = %d warnings = %d, want 1 and 1", res.failures, res.warnings)
	}

	// A skip is recorded and never printed: the human path says nothing at
	// these points and 031 owns that wording.
	if strings.Contains(out.String(), "no server credential") {
		t.Errorf("output = %q, want the skip recorded but not printed", out.String())
	}
}

// `note` is a cause, not a command. Serialising it as a remedy would tell a
// script to run "dial tcp 127.0.0.1:1: connect: connection refused".
func TestDoctorNoteSerialisesAsADetailNotARemedy(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{}
	res.section(&out, "Credentials")
	res.warn(&out, "could not reach the API to verify the key")
	res.note(&out, "connection refused")

	check := res.sections[0].Checks[0]

	if check.Detail != "connection refused" {
		t.Errorf("detail = %q, want the cause attached to the finding above it", check.Detail)
	}

	if check.Remedy != "" {
		t.Errorf("remedy = %q, want a cause never to become a command to run", check.Remedy)
	}
}

// A check recorded before any heading still has to be reachable, or the
// tallies stop matching what the sections contain.
func TestDoctorCheckWithoutASectionIsStillRecorded(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{}
	res.fail(&out, "no project detected here")

	if len(res.sections) != 1 || len(res.sections[0].Checks) != 1 {
		t.Fatalf("sections = %+v, want one check in an unnamed section", res.sections)
	}
}

// --quiet thins doctor's stream to what is wrong. It is the one flag that
// could turn a diagnostic into a silent exit code, so the split is pinned
// here as well as in the goldens.
func TestQuietDoctorPrintsFindingsAndNotPasses(t *testing.T) {
	var out bytes.Buffer

	res := &doctorResult{quiet: true}
	res.section(&out, "Credentials")
	res.pass(&out, "API key configured (test mode)")
	res.failr(&out, "reevit login", "the API rejected your key")
	res.warn(&out, "could not reach the API to verify the key")

	got := out.String()

	for _, gone := range []string{"Credentials", "API key configured"} {
		if strings.Contains(got, gone) {
			t.Errorf("output = %q, want %q suppressed", got, gone)
		}
	}

	for _, kept := range []string{"the API rejected your key", "reevit login", "could not reach the API"} {
		if !strings.Contains(got, kept) {
			t.Errorf("output = %q, want %q kept — --quiet trims narration, not findings", got, kept)
		}
	}

	// The JSON document is unaffected by --quiet: it is a different stream
	// with a different reader.
	if n := len(res.sections[0].Checks); n != 3 {
		t.Errorf("checks = %d, want all 3 recorded regardless of what printed", n)
	}
}
