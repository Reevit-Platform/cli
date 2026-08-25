package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/Reevit-Platform/cli/internal/config"
)

// recorder captures what the client actually put on the wire, which is the only
// thing these tests care about — the response is a fixed stub.
type recorder struct {
	server   *httptest.Server
	requests []*http.Request
	bodies   []string
	status   int
	response string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()

	rec := &recorder{status: http.StatusOK, response: "{}"}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
		}

		rec.requests = append(rec.requests, r)
		rec.bodies = append(rec.bodies, string(buf))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.status)
		_, _ = w.Write([]byte(rec.response))
	}))
	t.Cleanup(rec.server.Close)

	return rec
}

func (r *recorder) client() *Client {
	return New(config.Config{APIKey: "pfk_test_k.secret", BaseURL: r.server.URL, Mode: "test"})
}

func (r *recorder) keys() []string {
	out := make([]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.Header.Get("Idempotency-Key"))
	}

	return out
}

func TestIdempotencyKeyIsStableAcrossIdenticalCalls(t *testing.T) {
	t.Parallel()

	rec := newRecorder(t)
	client := rec.client()

	// The regression this whole change exists for: the same logical operation
	// issued twice — a user re-running a command that timed out — must carry
	// the same key, or the backend cannot tell it is a retry. The old
	// uuid.NewString() made these differ every single time.
	req := Request{
		Method:     http.MethodPost,
		Path:       "/payments/intents",
		Idempotent: true,
		Body:       map[string]any{"amount": 5000, "currency": "GHS"},
	}

	for range 2 {
		if err := client.Do(context.Background(), req, nil); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}

	keys := rec.keys()
	if keys[0] != keys[1] {
		t.Fatalf("identical operations produced different keys: %q vs %q", keys[0], keys[1])
	}

	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(keys[0]) {
		t.Fatalf("key = %q, want 32 lowercase hex characters", keys[0])
	}
}

func TestIdempotencyKeyIgnoresBodyAssemblyOrder(t *testing.T) {
	t.Parallel()

	rec := newRecorder(t)
	client := rec.client()

	// Two maps built in opposite orders are the same operation. encoding/json
	// sorts map keys, so hashing the marshalled bytes canonicalises for free —
	// this test is what keeps that assumption honest.
	bodies := []map[string]any{
		{"amount": 5000, "currency": "GHS", "country": "GH"},
		{"country": "GH", "currency": "GHS", "amount": 5000},
	}

	for _, body := range bodies {
		err := client.Do(context.Background(), Request{
			Method: http.MethodPost, Path: "/payments/intents", Idempotent: true, Body: body,
		}, nil)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}

	if keys := rec.keys(); keys[0] != keys[1] {
		t.Fatalf("key depends on map assembly order: %q vs %q", keys[0], keys[1])
	}
}

func TestIdempotencyKeyVariesWithTheOperation(t *testing.T) {
	t.Parallel()

	base := Request{
		Method:     http.MethodPost,
		Path:       "/payments/intents",
		Idempotent: true,
		Body:       map[string]any{"amount": 5000, "currency": "GHS"},
	}

	// Everything that makes this a *different* operation has to move the key,
	// or two unrelated calls would collapse into one at the backend.
	variants := map[string]func(Request) Request{
		"different amount": func(r Request) Request {
			r.Body = map[string]any{"amount": 6000, "currency": "GHS"}

			return r
		},
		"different path": func(r Request) Request {
			r.Path = "/checkout/sessions"

			return r
		},
		"different method": func(r Request) Request {
			r.Method = http.MethodPut

			return r
		},
		"different query": func(r Request) Request {
			r.Query = url.Values{"project_id": []string{"rvproj_1"}}

			return r
		},
	}

	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := newRecorder(t)
			client := rec.client()

			for _, req := range []Request{base, mutate(base)} {
				if err := client.Do(context.Background(), req, nil); err != nil {
					t.Fatalf("Do() error = %v", err)
				}
			}

			if keys := rec.keys(); keys[0] == keys[1] {
				t.Fatalf("%s reused key %q", name, keys[0])
			}
		})
	}
}

func TestIdempotencyKeyOverrideForcesADistinctOperation(t *testing.T) {
	t.Parallel()

	rec := newRecorder(t)
	client := rec.client()

	req := Request{
		Method: http.MethodPost, Path: "/payments/intents", Idempotent: true,
		Body: map[string]any{"amount": 5000},
	}
	override := req
	override.IdempotencyKey = "deliberate-second-charge"

	for _, r := range []Request{req, override} {
		if err := client.Do(context.Background(), r, nil); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}

	keys := rec.keys()
	if keys[1] != "deliberate-second-charge" {
		t.Fatalf("override key = %q", keys[1])
	}

	if keys[0] == keys[1] {
		t.Fatal("override did not change the key")
	}
}

func TestNonIdempotentRequestSendsNoKey(t *testing.T) {
	t.Parallel()

	rec := newRecorder(t)

	err := rec.client().Do(context.Background(), Request{Path: "/payments"}, nil)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if got := rec.keys()[0]; got != "" {
		t.Fatalf("read request carried an Idempotency-Key %q", got)
	}
}

func TestDoSendsAuthHeadersAndEncodesQuery(t *testing.T) {
	t.Parallel()

	rec := newRecorder(t)
	rec.response = `{"id":"pay_1"}`

	var out struct {
		ID string `json:"id"`
	}

	err := rec.client().Do(context.Background(), Request{
		Path:  "/payments",
		Query: url.Values{"limit": []string{"5"}, "status": []string{"succeeded"}},
	}, &out)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	got := rec.requests[0]
	if got.Method != http.MethodGet {
		t.Fatalf("method = %q, want GET when Request.Method is empty", got.Method)
	}

	if got.URL.Path != "/v1/payments" {
		t.Fatalf("path = %q, want the /v1 prefix applied once", got.URL.Path)
	}

	if got.URL.RawQuery != "limit=5&status=succeeded" {
		t.Fatalf("query = %q", got.URL.RawQuery)
	}

	for header, want := range map[string]string{
		"X-Reevit-Key":    "pfk_test_k.secret",
		"X-Reevit-Mode":   "test",
		"X-Reevit-Client": "reevit-cli",
		"Content-Type":    "application/json",
	} {
		if have := got.Header.Get(header); have != want {
			t.Fatalf("%s = %q, want %q", header, have, want)
		}
	}

	if out.ID != "pay_1" {
		t.Fatalf("decoded id = %q", out.ID)
	}
}

func TestDoSurfacesAPIErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		status   int
		response string
		wantCode string
		wantText string
	}{
		"structured error": {
			status:   http.StatusForbidden,
			response: `{"code":"missing_scope","message":"payments:write required"}`,
			wantCode: "missing_scope",
			wantText: "payments:write required",
		},
		// A gateway or proxy answers with something that is not the API's error
		// shape at all; the status still has to reach the user as a code.
		"unparseable body": {
			status:   http.StatusBadGateway,
			response: `<html>bad gateway</html>`,
			wantCode: "http_502",
		},
		// Older routes use "error" rather than "message".
		"legacy error field": {
			status:   http.StatusBadRequest,
			response: `{"code":"invalid_request","error":"amount must be positive"}`,
			wantCode: "invalid_request",
			wantText: "amount must be positive",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := newRecorder(t)
			rec.status, rec.response = tc.status, tc.response

			err := rec.client().Do(context.Background(), Request{Path: "/payments"}, nil)

			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("error = %#v, want *APIError", err)
			}

			if apiErr.Status != tc.status || apiErr.Code != tc.wantCode {
				t.Fatalf("status/code = %d/%q, want %d/%q",
					apiErr.Status, apiErr.Code, tc.status, tc.wantCode)
			}

			if tc.wantText != "" && !regexp.MustCompile(regexp.QuoteMeta(tc.wantText)).
				MatchString(apiErr.Error()) {
				t.Fatalf("Error() = %q, want it to mention %q", apiErr.Error(), tc.wantText)
			}
		})
	}
}
