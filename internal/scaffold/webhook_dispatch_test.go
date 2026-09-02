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

	body, err := json.Marshal(map[string]any{
		"event":               "payment.succeeded",
		"data":                map[string]any{"id": "pay_dispatch"},
		"delivery_id":         "evtd_dispatch_1",
		"attempt":             1,
		"signature_timestamp": time.Now().UTC().Format(time.RFC3339),
		"api_version":         "2026-03-05",
	})
	if err != nil {
		t.Fatal(err)
	}

	return body
}

// instrument replaces a template's inert "TODO: fulfil the order" body with a
// side effect the test can observe, so the assertion is that the handler took
// the payment.succeeded branch — not merely that it parsed the body. The
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
		`if err := os.WriteFile("dispatched.txt", []byte("payment.succeeded"), 0o600); err != nil {
			panic(err)
		}`)

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/dispatch\n\ngo 1.23\n")
	write(t, root, "reevit_webhook.go", source)
	write(t, root, "dispatch_test.go", goDispatchTest)
	write(t, root, "body.json", string(productionBody(t)))

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
	"testing"
)

func TestDispatchesOnEvent(t *testing.T) {
	body, err := os.ReadFile("body.json")
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte(os.Getenv("REEVIT_WEBHOOK_SECRET")))
	mac.Write(body)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/reevit", bytes.NewReader(body))
	req.Header.Set("X-Reevit-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	rec := httptest.NewRecorder()
	HandleReevitWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := os.ReadFile("dispatched.txt")
	if err != nil {
		t.Fatalf("handler returned 200 without dispatching: %v", err)
	}

	if string(got) != "payment.succeeded" {
		t.Fatalf("dispatched = %q", got)
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
		`globalThis.__dispatched = "payment.succeeded";`)

	root := t.TempDir()
	write(t, root, "node_modules/@reevit/node/package.json",
		`{"name":"@reevit/node","version":"0.0.0-test","type":"module","main":"index.js"}`)
	write(t, root, "node_modules/@reevit/node/index.js", reevitNodeStub)
	write(t, root, "handler.mjs", source)
	write(t, root, "run.mjs", nodeDispatchRunner)
	write(t, root, "body.json", string(productionBody(t)))

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

const body = readFileSync("body.json", "utf8");
const signature = "sha256=" + createHmac("sha256", process.env.REEVIT_WEBHOOK_SECRET).update(body).digest("hex");

const response = await POST(new Request("http://localhost/api/webhooks/reevit", {
  method: "POST",
  headers: { "x-reevit-signature": signature, "content-type": "application/json" },
  body,
}));

if (response.status !== 200) {
  throw new Error("status " + response.status + ": " + (await response.text()));
}

if (globalThis.__dispatched !== "payment.succeeded") {
  throw new Error("handler returned 200 without dispatching (dispatched=" + globalThis.__dispatched + ")");
}
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
		`open("dispatched.txt", "w", encoding="utf-8").write("payment.succeeded")`)

	root := t.TempDir()
	write(t, root, "reevit.py", reevitPythonStub)
	write(t, root, "reevit_webhook.py", source)
	write(t, root, "run.py", pythonDispatchRunner)
	write(t, root, "body.json", string(productionBody(t)))

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

body = pathlib.Path("body.json").read_bytes()
secret = os.environ["REEVIT_WEBHOOK_SECRET"]
signature = "sha256=" + hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()

if not reevit_webhook.handle_reevit_event(body, signature):
    sys.exit("handler rejected a correctly signed production body")

dispatched = pathlib.Path("dispatched.txt")
if not dispatched.exists():
    sys.exit("handler accepted the delivery without dispatching")

if dispatched.read_text(encoding="utf-8") != "payment.succeeded":
    sys.exit("dispatched = " + dispatched.read_text(encoding="utf-8"))
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
		`file_put_contents(__DIR__ . '/dispatched.txt', 'payment.succeeded');`)

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

	if string(got) != "payment.succeeded" {
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
