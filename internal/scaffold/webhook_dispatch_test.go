package scaffold

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Production deliveries name the event in `event`, not `type`
// (backend `internal/usecase/payments/service.go` builds the envelope, and the
// outbound dispatcher never adds or renames a `type` field). Every generated
// handler used to switch on `type`, so a merchant who shipped one verified the
// signature, returned 200 and did nothing at all.
//
// These tests pin the defensive read — `event` first, `type` as a fallback for
// `reevit listen`'s unparseable-frame envelope, `doctor`'s probe and any
// fixture a merchant already stored.

// webhookDispatchNeedles pins, per template, the text that proves the handler
// reads `event`, and the legacy expression that must be gone.
var webhookDispatchNeedles = map[string]struct {
	required  string
	forbidden string
}{
	"next-webhook.ts.tmpl":       {`const eventType = event.event ?? event.type;`, `switch (event.type)`},
	"express-webhook.ts.tmpl":    {`const eventType = event.event ?? event.type;`, `switch (event.type)`},
	"next-pages-webhook.ts.tmpl": {`const eventType = event.event ?? event.type;`, `event.type === "payment.succeeded"`},
	"nuxt-webhook.ts.tmpl":       {`const eventType = payload.event ?? payload.type;`, `payload.type === "payment.succeeded"`},
	"sveltekit-webhook.ts.tmpl":  {`const eventType = event.event ?? event.type;`, `event.type === "payment.succeeded"`},
	"go-webhook.go.tmpl":         {"eventType := event.Event", "switch event.Type {"},
	"python-webhook.py.tmpl":     {`event_type = event.get("event") or event.get("type")`, `event.get("type") == "payment.succeeded"`},
	"python-django-webhook.py.tmpl": {`event_type = event.get("event") or event.get("type")`,
		`event.get("type") == "payment.succeeded"`},
	"python-fastapi-webhook.py.tmpl": {`event_type = event.get("event") or event.get("type")`,
		`event.get("type") == "payment.succeeded"`},
	"python-flask-webhook.py.tmpl": {`event_type = event.get("event") or event.get("type")`,
		`event.get("type") == "payment.succeeded"`},
	"php-webhook.php.tmpl":     {`$eventType = $event['event'] ?? $event['type'] ?? '';`, `switch ($event['type'] ?? '')`},
	"laravel-webhook.php.tmpl": {`$eventType = $event['event'] ?? $event['type'] ?? '';`, `switch ($event['type'] ?? '')`},
}

// nonexistentEvents are names the platform does not send. They read as real —
// they are the names the docs used to carry, and `reevit trigger` still uses
// them as *scenario* labels — which is exactly why a scaffold reaches for them.
// A handler that branches on one of these verifies the signature, answers 200
// and does nothing, forever, with nothing to see in any log.
var nonexistentEvents = []string{"payment.succeeded", "payment.failed"}

// TestNoWebhookTemplateNamesAnEventWeDoNotSend is the guard the last fix was
// missing. PR #10 corrected the key every handler read — `event`, not `type` —
// and left the value alone, so the scaffolds went on matching a name that is
// never delivered and the bug survived its own fix. Checking the names as a
// class, across every template, is what makes that not repeat.
func TestNoWebhookTemplateNamesAnEventWeDoNotSend(t *testing.T) {
	t.Parallel()

	names, err := webhookTemplateNames()
	if err != nil {
		t.Fatal(err)
	}

	if len(names) != 12 {
		t.Fatalf("found %d webhook templates, want 12", len(names))
	}

	for _, name := range names {
		source, err := render(name, templateData{})
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}

		for _, event := range nonexistentEvents {
			// Quoted, because that is what a branch looks like in all five
			// languages. The templates name these events in prose to explain
			// why they are absent, and backticks keep that from reading as a
			// branch here.
			for _, literal := range []string{`"` + event + `"`, `'` + event + `'`} {
				if strings.Contains(source, literal) {
					t.Errorf("%s branches on %s, which the platform never sends", name, literal)
				}
			}
		}
	}
}

// TestEveryWebhookTemplateDispatchesOnEvent covers all twelve templates in both
// TS and JS renders. It is the only check that reaches the templates whose
// runtimes the executable tests below do not stand up.
func TestEveryWebhookTemplateDispatchesOnEvent(t *testing.T) {
	t.Parallel()

	names, err := webhookTemplateNames()
	if err != nil {
		t.Fatal(err)
	}

	if len(names) != 12 {
		t.Fatalf("found %d webhook templates, want 12: %v", len(names), names)
	}

	for _, name := range names {
		needles, ok := webhookDispatchNeedles[name]
		if !ok {
			t.Fatalf("no dispatch expectation recorded for %s — add one", name)
		}

		for _, ts := range []bool{true, false} {
			out, err := render(name, templateData{TS: ts})
			if err != nil {
				t.Fatalf("render %s (TS=%v): %v", name, ts, err)
			}

			if !strings.Contains(out, needles.required) {
				t.Errorf("%s (TS=%v) must dispatch on the production field:\nwant %q in\n%s",
					name, ts, needles.required, out)
			}

			if strings.Contains(out, needles.forbidden) {
				t.Errorf("%s (TS=%v) still dispatches on the legacy field %q", name, ts, needles.forbidden)
			}
		}
	}
}

// webhookReplayNeedles pins the replay guard every generated handler must
// carry: the named tolerance constant, a read of the signed signature_timestamp
// and delivery_id, and the "make this persistent" instruction on the dedupe
// stub.
var webhookReplayNeedles = []string{
	"signature_timestamp",
	"delivery_id",
	"Replace with a persistent store (database unique index on delivery_id) before production.",
}

// TestEveryWebhookTemplateGuardsAgainstReplay — the CLI signs delivery_id,
// attempt and signature_timestamp into the body precisely so handlers can
// reject a replayed capture and dedupe a retry storm. Generated handlers used
// to ignore all three.
func TestEveryWebhookTemplateGuardsAgainstReplay(t *testing.T) {
	t.Parallel()

	names, err := webhookTemplateNames()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		for _, ts := range []bool{true, false} {
			out, err := render(name, templateData{TS: ts})
			if err != nil {
				t.Fatalf("render %s (TS=%v): %v", name, ts, err)
			}

			needles := webhookReplayNeedles
			// The constant is spelled in each language's own convention.
			if strings.HasSuffix(name, ".go.tmpl") {
				needles = append([]string{"reevitWebhookToleranceSeconds = 300"}, needles...)
			} else {
				needles = append([]string{"REEVIT_WEBHOOK_TOLERANCE_SECONDS"}, needles...)
			}

			for _, needle := range needles {
				if !strings.Contains(out, needle) {
					t.Errorf("%s (TS=%v) is missing %q", name, ts, needle)
				}
			}
		}
	}
}

func webhookTemplateNames() ([]string, error) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}

	var names []string

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "webhook") {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

// productionBody is the envelope a real delivery carries: `event`, plus the
// replay fields the outbound dispatcher signs into the body.
func productionBody(t *testing.T) []byte {
	t.Helper()

	return deliveryBody(t, "evtd_dispatch_1", time.Now().UTC())
}

// staleBody is the same delivery with a signature timestamp far outside the
// five-minute tolerance — a captured delivery being replayed.
func staleBody(t *testing.T) []byte {
	t.Helper()

	return deliveryBody(t, "evtd_dispatch_stale", time.Now().UTC().Add(-20*time.Minute))
}

func deliveryBody(t *testing.T, deliveryID string, at time.Time) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		// The platform has no payment.succeeded. A terminal outcome is
		// delivered as payment.updated with the result in data.status, and
		// that is what the handlers under test have to branch on.
		"event":               "payment.updated",
		"data":                map[string]any{"id": "pay_dispatch", "status": "succeeded"},
		"delivery_id":         deliveryID,
		"attempt":             1,
		"signature_timestamp": at.Format(time.RFC3339),
		"api_version":         "2026-03-05",
	})
	if err != nil {
		t.Fatal(err)
	}

	return body
}

// instrument replaces a template's inert "TODO: fulfil the order" body with a
// side effect the test can observe, so the assertion is that the handler took
// the succeeded branch — not merely that it parsed the body. The
// effect writes a literal rather than the template's variable so the
// instrumented source still compiles against the unfixed template, and the
// failure is the silent no-op itself rather than a build error.
func instrument(t *testing.T, source, todo, effect string) string {
	t.Helper()

	if !strings.Contains(source, todo) {
		t.Fatalf("instrumentation anchor %q is gone from the template:\n%s", todo, source)
	}

	return strings.Replace(source, todo, effect, 1)
}

const dispatchSecret = "whsec_dispatch"

// TestGoWebhookTemplateDispatchesOnEvent compiles and runs the generated Go
// handler against a production-shaped body. Go is the quietest of the twelve:
// a `json:"type"` tag on a payload without `type` is not an error, it just
// leaves the field empty, so the switch falls to default and the handler
// answers 200 having done nothing.
func TestGoWebhookTemplateDispatchesOnEvent(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}

	source, err := render("go-webhook.go.tmpl", templateData{})
	if err != nil {
		t.Fatal(err)
	}

	source = instrument(t, source,
		"// TODO: fulfil the order for event.Data",
		`sink, openErr := os.OpenFile("dispatched.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if openErr != nil {
			panic(openErr)
		}
		if _, writeErr := sink.WriteString("payment.updated\n"); writeErr != nil {
			panic(writeErr)
		}
		if closeErr := sink.Close(); closeErr != nil {
			panic(closeErr)
		}`)

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/dispatch\n\ngo 1.23\n")
	write(t, root, "reevit_webhook.go", source)
	write(t, root, "dispatch_test.go", goDispatchTest)
	write(t, root, "body.json", string(productionBody(t)))
	write(t, root, "stale.json", string(staleBody(t)))

	cmd := exec.Command(goBin, "test", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "REEVIT_WEBHOOK_SECRET="+dispatchSecret)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Go handler did not dispatch on `event`: %v\n%s", err, output)
	}
}

const goDispatchTest = `package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func post(t *testing.T, name string) int {
	t.Helper()

	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte(os.Getenv("REEVIT_WEBHOOK_SECRET")))
	mac.Write(body)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/reevit", bytes.NewReader(body))
	req.Header.Set("X-Reevit-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	rec := httptest.NewRecorder()
	HandleReevitWebhook(rec, req)

	return rec.Code
}

func dispatches(t *testing.T) int {
	t.Helper()

	raw, err := os.ReadFile("dispatched.txt")
	if os.IsNotExist(err) {
		return 0
	}

	if err != nil {
		t.Fatal(err)
	}

	return len(strings.Fields(string(raw)))
}

func TestDispatchesOnEvent(t *testing.T) {
	if code := post(t, "body.json"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	if got := dispatches(t); got != 1 {
		t.Fatalf("dispatched %d times, want 1 — the handler answered 200 without dispatching", got)
	}
}

func TestAcksAReplayedDeliveryWithoutRedispatching(t *testing.T) {
	if code := post(t, "body.json"); code != http.StatusOK {
		t.Fatalf("replay status = %d, want an idempotent 200", code)
	}

	if got := dispatches(t); got != 1 {
		t.Fatalf("dispatched %d times after a replay, want 1", got)
	}
}

func TestRejectsAStaleSignatureTimestamp(t *testing.T) {
	if code := post(t, "stale.json"); code != http.StatusBadRequest {
		t.Fatalf("stale status = %d, want 400", code)
	}

	if got := dispatches(t); got != 1 {
		t.Fatalf("a 20-minute-old delivery was dispatched (%d total)", got)
	}
}
`

// TestNextWebhookTemplateDispatchesOnEvent runs the generated App Router
// handler under node against the same production-shaped body.
func TestNextWebhookTemplateDispatchesOnEvent(t *testing.T) {
	t.Parallel()

	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}

	// The JS render, so the file runs as ESM without a transpiler.
	source, err := render("next-webhook.ts.tmpl", templateData{TS: false})
	if err != nil {
		t.Fatal(err)
	}

	source = instrument(t, source,
		"// TODO: fulfil the order for event.data",
		`globalThis.__dispatched = (globalThis.__dispatched ?? 0) + 1;`)

	root := t.TempDir()
	write(t, root, "node_modules/@reevit/node/package.json",
		`{"name":"@reevit/node","version":"0.0.0-test","type":"module","main":"index.js"}`)
	write(t, root, "node_modules/@reevit/node/index.js", reevitNodeStub)
	write(t, root, "handler.mjs", source)
	write(t, root, "run.mjs", nodeDispatchRunner)
	write(t, root, "body.json", string(productionBody(t)))
	write(t, root, "stale.json", string(staleBody(t)))

	cmd := exec.Command(nodeBin, "run.mjs")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "REEVIT_WEBHOOK_SECRET="+dispatchSecret)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Next handler did not dispatch on `event`: %v\n%s", err, output)
	}
}

// reevitNodeStub stands in for @reevit/node so the test needs no registry.
// It implements exactly what the template calls.
const reevitNodeStub = `import { createHmac, timingSafeEqual } from "node:crypto";

export function verifyWebhookSignature(body, signature, secret) {
  if (typeof signature !== "string" || !signature.startsWith("sha256=") || !secret) return false;
  const raw = typeof body === "string" ? body : body.toString("utf8");
  const expected = "sha256=" + createHmac("sha256", secret).update(raw).digest("hex");
  if (expected.length !== signature.length) return false;
  return timingSafeEqual(Buffer.from(expected), Buffer.from(signature));
}
`

const nodeDispatchRunner = `import { readFileSync } from "node:fs";
import { createHmac } from "node:crypto";
import { POST } from "./handler.mjs";

globalThis.__dispatched = 0;

async function post(name) {
  const body = readFileSync(name, "utf8");
  const signature = "sha256=" + createHmac("sha256", process.env.REEVIT_WEBHOOK_SECRET).update(body).digest("hex");

  return POST(new Request("http://localhost/api/webhooks/reevit", {
    method: "POST",
    headers: { "x-reevit-signature": signature, "content-type": "application/json" },
    body,
  }));
}

function expect(label, actual, wanted) {
  if (actual !== wanted) throw new Error(label + ": got " + actual + ", want " + wanted);
}

expect("signed delivery status", (await post("body.json")).status, 200);
expect("dispatches after one delivery (200 with nothing dispatched?)", globalThis.__dispatched, 1);

expect("replayed delivery status", (await post("body.json")).status, 200);
expect("dispatches after a replay", globalThis.__dispatched, 1);

expect("stale delivery status", (await post("stale.json")).status, 400);
expect("dispatches after a stale delivery", globalThis.__dispatched, 1);
`

// TestPythonWebhookTemplateDispatchesOnEvent runs the generated module-style
// Python handler against the same body.
func TestPythonWebhookTemplateDispatchesOnEvent(t *testing.T) {
	t.Parallel()

	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 unavailable: %v", err)
	}

	source, err := render("python-webhook.py.tmpl", templateData{})
	if err != nil {
		t.Fatal(err)
	}

	source = instrument(t, source,
		`pass  # TODO: fulfil the order for event["data"]`,
		`open("dispatched.txt", "a", encoding="utf-8").write("payment.updated\n")`)

	root := t.TempDir()
	write(t, root, "reevit.py", reevitPythonStub)
	write(t, root, "reevit_webhook.py", source)
	write(t, root, "run.py", pythonDispatchRunner)
	write(t, root, "body.json", string(productionBody(t)))
	write(t, root, "stale.json", string(staleBody(t)))

	cmd := exec.Command(pythonBin, "run.py")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "REEVIT_WEBHOOK_SECRET="+dispatchSecret)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Python handler did not dispatch on `event`: %v\n%s", err, output)
	}
}

const reevitPythonStub = `"""Stub for the reevit package: only what the template calls."""

import hashlib
import hmac


def verify_webhook_signature(body, signature, secret):
    if not signature or not signature.startswith("sha256=") or not secret:
        return False
    if isinstance(body, str):
        body = body.encode("utf-8")
    expected = hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature[len("sha256=") :])
`

const pythonDispatchRunner = `import hashlib
import hmac
import os
import pathlib
import sys

import reevit_webhook

SECRET = os.environ["REEVIT_WEBHOOK_SECRET"].encode("utf-8")


def post(name):
    body = pathlib.Path(name).read_bytes()
    signature = "sha256=" + hmac.new(SECRET, body, hashlib.sha256).hexdigest()

    return reevit_webhook.handle_reevit_event(body, signature)


def dispatches():
    path = pathlib.Path("dispatched.txt")

    return len(path.read_text(encoding="utf-8").split()) if path.exists() else 0


if not post("body.json"):
    sys.exit("handler rejected a correctly signed production body")

if dispatches() != 1:
    sys.exit("accepted the delivery but dispatched %d times" % dispatches())

if not post("body.json"):
    sys.exit("a replayed delivery must be acknowledged, not rejected")

if dispatches() != 1:
    sys.exit("a replayed delivery was dispatched again (%d total)" % dispatches())

if post("stale.json"):
    sys.exit("a 20-minute-old signature was accepted")

if dispatches() != 1:
    sys.exit("a stale delivery was dispatched (%d total)" % dispatches())
`

// TestPHPWebhookTemplateDispatchesOnEvent runs the generated standalone PHP
// script through the CLI SAPI, in its own process so the template's `exit;`
// on a rejected signature cannot be mistaken for a pass.
func TestPHPWebhookTemplateDispatchesOnEvent(t *testing.T) {
	t.Parallel()

	phpBin, err := exec.LookPath("php")
	if err != nil {
		t.Skipf("php unavailable: %v", err)
	}

	source, err := render("php-webhook.php.tmpl", templateData{})
	if err != nil {
		t.Fatal(err)
	}

	source = instrument(t, source,
		`// TODO: fulfil the order for $event['data']`,
		`file_put_contents(__DIR__ . '/dispatched.txt', 'payment.updated');`)

	// php://input is not readable under the CLI SAPI, and the header arrives
	// through $_SERVER rather than a real request. Rewrite only those two
	// environment reads; the parsing and dispatch under test are untouched.
	source = strings.Replace(source,
		"file_get_contents('php://input')",
		"file_get_contents(__DIR__ . '/body.json')", 1)
	source = strings.Replace(source,
		"$_SERVER['HTTP_X_REEVIT_SIGNATURE'] ?? null",
		"getenv('REEVIT_TEST_SIGNATURE') ?: null", 1)
	source = strings.Replace(source,
		"$_SERVER['HTTP_X_REEVIT_SIGNATURE_TIMESTAMP'] ?? null",
		"getenv('REEVIT_TEST_SIGNATURE_TIMESTAMP') ?: null", 1)

	root := t.TempDir()
	body := productionBody(t)
	write(t, root, "vendor/autoload.php", phpVendorStub)
	write(t, root, "reevit-webhook.php", source)
	write(t, root, "body.json", string(body))

	cmd := exec.Command(phpBin, "reevit-webhook.php")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"REEVIT_WEBHOOK_SECRET="+dispatchSecret,
		"REEVIT_TEST_SIGNATURE="+signPayload(dispatchSecret, body))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated PHP handler exited non-zero: %v\n%s", err, output)
	}

	got, err := os.ReadFile(filepath.Join(root, "dispatched.txt"))
	if err != nil {
		t.Fatalf("PHP handler finished (%q) without dispatching on `event`: %v", output, err)
	}

	if string(got) != "payment.updated" {
		t.Fatalf("dispatched = %q", got)
	}
}

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const phpVendorStub = `<?php

namespace Reevit\Webhooks;

final class SignatureVerifier
{
    public static function verify(?string $payload, ?string $signature, string $secret): bool
    {
        if ($payload === null || $signature === null || $secret === '') {
            return false;
        }
        if (strncmp($signature, 'sha256=', 7) !== 0) {
            return false;
        }
        $expected = hash_hmac('sha256', $payload, $secret);

        return hash_equals($expected, substr($signature, 7));
    }
}
`
