// Package api is a minimal Reevit API client for the CLI. Authorization is
// the scoped API key; the backend enforces scopes on every route.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

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

type Request struct {
	Method     string
	Path       string // e.g. /payments
	Query      url.Values
	Body       any
	Idempotent bool // adds an Idempotency-Key (money-moving routes)
}

func (c *Client) Do(ctx context.Context, req Request, out any) error {
	endpoint := c.cfg.BaseURL + "/v1" + req.Path
	if len(req.Query) > 0 {
		endpoint += "?" + req.Query.Encode()
	}

	var body io.Reader

	if req.Body != nil {
		raw, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}

		body = bytes.NewReader(raw)
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
		httpReq.Header.Set("Idempotency-Key", uuid.NewString())
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
