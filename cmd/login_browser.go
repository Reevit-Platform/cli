package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

// pollSecretHeader carries the pairing poll secret — a header, never a query
// param, matching the backend contract.
const pollSecretHeader = "X-Reevit-Poll-Secret"

type pairingStartResponse struct {
	ID          string    `json:"id"`
	PairingCode string    `json:"pairing_code"`
	PollSecret  string    `json:"poll_secret"`
	BrowserURL  string    `json:"browser_url"`
	ExpiresAt   time.Time `json:"expires_at"`
	Interval    int       `json:"interval"`
}

type pairingPollResponse struct {
	Status string `json:"status"`
	APIKey *struct {
		ID     string   `json:"id"`
		Raw    string   `json:"raw"`
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
		Mode   string   `json:"mode"`
	} `json:"api_key"`
	Org *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"org"`
}

// browserLogin drives the pairing flow: start a session, hand the user to the
// dashboard, poll until approved, save the delivered test-mode key.
func browserLogin(cmd *cobra.Command, openBrowser bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	httpc := &http.Client{Timeout: 30 * time.Second}
	ctx := cmd.Context()

	hostname, _ := os.Hostname()

	start, err := startPairing(ctx, httpc, cfg.BaseURL, hostname)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "\nYour pairing code is  %s\n", start.PairingCode)
	fmt.Fprintf(out, "Confirm it in your browser:  %s\n\n", start.BrowserURL)

	if openBrowser {
		if err := openInBrowser(start.BrowserURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "could not open a browser (%v) — open the link above manually\n", err)
		}
	}

	fmt.Fprint(out, "Waiting for approval")

	result, err := pollPairing(ctx, httpc, cfg.BaseURL, start, out)
	if err != nil {
		return err
	}

	cfg.APIKey = result.APIKey.Raw
	cfg.Mode = "test"

	if result.Org != nil {
		cfg.OrgID = result.Org.ID
		cfg.OrgName = result.Org.Name
	}

	p, err := config.Save(cfg)
	if err != nil {
		return err
	}

	orgName := ""
	if result.Org != nil {
		orgName = result.Org.Name
	}

	if orgName == "" {
		orgName = "your organization"
	}

	fmt.Fprintf(out, "\n✔ Logged in to %s as %s\n", orgName, result.APIKey.Name)
	fmt.Fprintf(out, "✔ Saved to %s (test mode)\n\n", p)
	fmt.Fprintln(out, "Heads up: this is a TEST-MODE key — perfect for `reevit listen`, `reevit trigger`,")
	fmt.Fprintln(out, "and integrating safely. When you're ready for live traffic, create a live key in")
	fmt.Fprintln(out, "Dashboard → Developers → API keys, then run:  reevit login --key <live_key>")

	return nil
}

func startPairing(ctx context.Context, httpc *http.Client, baseURL, deviceName string) (pairingStartResponse, error) {
	body, err := json.Marshal(map[string]string{"device_name": deviceName})
	if err != nil {
		return pairingStartResponse{}, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/cli/auth", bytes.NewReader(body))
	if err != nil {
		return pairingStartResponse{}, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reevit-Client", "reevit-cli")

	resp, err := httpc.Do(req)
	if err != nil {
		return pairingStartResponse{}, fmt.Errorf("start pairing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return pairingStartResponse{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		apiErr := &api.APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, apiErr)

		if apiErr.Code == "" {
			apiErr.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}

		return pairingStartResponse{}, apiErr
	}

	var start pairingStartResponse
	if err := json.Unmarshal(raw, &start); err != nil {
		return pairingStartResponse{}, fmt.Errorf("decode response: %w", err)
	}

	if start.Interval <= 0 {
		start.Interval = 5
	}

	return start, nil
}

// pollPairing polls until the session resolves. It honors the server's
// interval, backs off on 429 Retry-After, and gives up at the session expiry.
func pollPairing(ctx context.Context, httpc *http.Client, baseURL string, start pairingStartResponse, out io.Writer) (pairingPollResponse, error) {
	interval := time.Duration(start.Interval) * time.Second
	deadline := start.ExpiresAt

	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return pairingPollResponse{}, fmt.Errorf("\nthe pairing request expired before it was approved — run `reevit login` again")
		}

		select {
		case <-ctx.Done():
			return pairingPollResponse{}, fmt.Errorf("\nlogin canceled")
		case <-time.After(interval):
		}

		fmt.Fprint(out, ".")

		result, retryAfter, err := pollOnce(ctx, httpc, baseURL, start)
		if err != nil {
			return pairingPollResponse{}, err
		}

		if retryAfter > 0 {
			interval = retryAfter

			continue
		}

		interval = time.Duration(start.Interval) * time.Second

		switch result.Status {
		case "pending":
			continue
		case "approved":
			if result.APIKey == nil || result.APIKey.Raw == "" {
				return pairingPollResponse{}, fmt.Errorf("\nthe server approved the pairing but sent no key — run `reevit login` again")
			}

			return result, nil
		case "denied":
			return pairingPollResponse{}, fmt.Errorf("\nthe pairing request was denied in the dashboard")
		case "expired":
			return pairingPollResponse{}, fmt.Errorf("\nthe pairing request expired before it was approved — run `reevit login` again")
		case "consumed":
			return pairingPollResponse{}, fmt.Errorf("\nthis pairing request was already used — run `reevit login` again")
		default:
			return pairingPollResponse{}, fmt.Errorf("\nunexpected pairing status %q", result.Status)
		}
	}
}

// pollOnce returns the poll result, or a positive retryAfter when the server
// asked us to slow down (HTTP 429).
func pollOnce(ctx context.Context, httpc *http.Client, baseURL string, start pairingStartResponse) (pairingPollResponse, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/cli/auth/"+start.ID, nil)
	if err != nil {
		return pairingPollResponse{}, 0, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set(pollSecretHeader, start.PollSecret)
	req.Header.Set("X-Reevit-Client", "reevit-cli")

	resp, err := httpc.Do(req)
	if err != nil {
		return pairingPollResponse{}, 0, fmt.Errorf("poll pairing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return pairingPollResponse{}, 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 10 * time.Second
		if v := resp.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
				retryAfter = time.Duration(secs) * time.Second
			}
		}

		return pairingPollResponse{}, retryAfter, nil
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := &api.APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, apiErr)

		if apiErr.Code == "" {
			apiErr.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}

		return pairingPollResponse{}, 0, apiErr
	}

	var result pairingPollResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return pairingPollResponse{}, 0, fmt.Errorf("decode response: %w", err)
	}

	return result, 0, nil
}

func openInBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
