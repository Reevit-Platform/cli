package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The generated scaffold's fulfilment/notification TODOs are instrumented by
// the smoke harness. A 200 alone would not prove that either branch ran.
var nativeObserved []string

func TestNativeGeneratedPaymentOutcomes(t *testing.T) {
	secret := "whsec_native_smoke"
	t.Setenv("REEVIT_WEBHOOK_SECRET", secret)
	for index, test := range []struct {
		name, field, status, want string
	}{
		{"succeeded", "event", "succeeded", "succeeded"},
		{"failed", "event", "failed", "failed"},
		{"pending", "event", "pending", ""},
		{"legacy type fallback", "type", "succeeded", "succeeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nativeObserved = nil
			body, err := json.Marshal(map[string]any{
				test.field:            "payment.updated",
				"data":                map[string]any{"id": "pmt_native", "status": test.status},
				"delivery_id":         fmt.Sprintf("evtd_native_%d", index),
				"signature_timestamp": time.Now().UTC().Format(time.RFC3339),
			})
			if err != nil {
				t.Fatal(err)
			}
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			request := httptest.NewRequest(http.MethodPost, "/webhooks/reevit", bytes.NewReader(body))
			request.Header.Set("X-Reevit-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
			response := httptest.NewRecorder()
			HandleReevitWebhook(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}
			if test.want == "" {
				if len(nativeObserved) != 0 {
					t.Fatalf("pending payment dispatched: %v", nativeObserved)
				}
			} else if len(nativeObserved) != 1 || nativeObserved[0] != test.want {
				t.Fatalf("observed = %v, want one %q dispatch", nativeObserved, test.want)
			}
		})
	}
}
