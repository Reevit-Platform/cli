package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
