// Package api is a minimal Reevit API client for the CLI. Authorization is
// the scoped API key; the backend enforces scopes on every route.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Reevit-Platform/cli/internal/config"
)

type Client struct {
	cfg  config.Config
	http *http.Client
}

func New(cfg config.Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) Mode() string { return c.cfg.Mode }

type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	ErrText string `json:"error"`
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.ErrText
	}

	return fmt.Sprintf("Reevit API %d (%s): %s", e.Status, e.Code, msg)
}

// Hint returns a one-line next step for the API failures a developer can
// actually act on, or "" when there is nothing useful to say. The CLI's error
// printer looks for this method (cmd.RenderError).
func (e *APIError) Hint() string {
	switch {
	case e.Status == 401:
		return "run `reevit login` to get a fresh key"
	case e.Status == 403:
		return "your key lacks a required scope — run `reevit login` for a fresh test-mode key, " +
			"or use a key with the scope from Dashboard → Developers → API keys"
	case e.Status == 429:
		return "you are being rate limited — wait a moment and retry"
	case e.Status >= 500:
		return "Reevit returned a server error — retry; if it persists, check https://status.reevit.io"
	}

	return ""
}

type Request struct {
	Method     string
	Path       string // e.g. /payments
	Query      url.Values
	Body       any
	Idempotent bool // adds an Idempotency-Key (money-moving routes)

	// IdempotencyKey overrides the derived key.
	//
	// The escape hatch for the cost of deriving: two deliberate, byte-identical
	// operations inside the backend's idempotency window collapse into one. Set
	// this to force a genuinely separate second call.
	IdempotencyKey string
}

// deriveIdempotencyKey builds a key from the operation itself — method, path
// (query included) and the exact body bytes about to be sent.
//
// A fresh uuid.NewString() per invocation, which is what this used to send,
// cannot deduplicate anything. A key only earns the name when a *retry of the
// same logical operation* carries the same value, and a per-call UUID
// guarantees it never does: re-running a command after a timeout looked like a
// brand-new payment to the backend, leaving its own duplicate guards as the
// only thing between a retry and a second charge.
//
// Hashing the marshalled body instead of re-canonicalising the Go value means
// the key describes exactly what goes on the wire, so the two cannot drift.
// encoding/json is deterministic for a given value — map keys are sorted and
// struct fields follow declaration order — so the same operation hashes the
// same on every run. Reordering a request struct's fields would change the key
// across CLI versions; that only affects a retry spanning an upgrade inside the
// backend's window, which is not worth pinning declaration order for.
//
// The API key is deliberately not mixed in. The backend already scopes
// idempotency records by organization, and salting per key would make the same
// operation from two shells look distinct — the exact behaviour being fixed.
func deriveIdempotencyKey(method, target string, body []byte) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\n", method, target)
	_, _ = sum.Write(body)

	return hex.EncodeToString(sum.Sum(nil))[:32]
}

func (c *Client) Do(ctx context.Context, req Request, out any) error {
	target := req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}

	endpoint := c.cfg.BaseURL + "/v1" + target

	var (
		payload []byte
		body    io.Reader
	)

	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}

		payload = encoded
		body = bytes.NewReader(payload)
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Reevit-Key", c.cfg.APIKey)
	httpReq.Header.Set("X-Reevit-Mode", c.cfg.Mode)
	httpReq.Header.Set("X-Reevit-Client", "reevit-cli")

	if req.Idempotent {
		key := req.IdempotencyKey
		if key == "" {
			key = deriveIdempotencyKey(method, target, payload)
		}

		httpReq.Header.Set("Idempotency-Key", key)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, req.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, apiErr)

		if apiErr.Code == "" {
			apiErr.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}

		return apiErr
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
