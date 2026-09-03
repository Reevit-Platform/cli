package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// pairingServer scripts the backend: a fixed start response, then a sequence
// of poll responses (each entry is either a status string or "429").
func pairingServer(t *testing.T, pollSequence []string) *httptest.Server {
	t.Helper()

	polls := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "clia_1",
				"pairing_code": "GX7M-4KP9",
				"poll_secret":  "secret",
				"browser_url":  "https://dashboard.example/cli/confirm?code=GX7M-4KP9",
				"expires_at":   time.Now().Add(10 * time.Minute),
				"interval":     1,
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/cli/auth/"):
			if r.Header.Get(pollSecretHeader) != "secret" {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			step := pollSequence[min(polls, len(pollSequence)-1)]
			polls++

			switch step {
			case "429":
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
			case "approved":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "approved",
					"api_key": map[string]any{
						"id":     strings.TrimSuffix(pairingAPIKey, goldenKeySuffix),
						"raw":    pairingAPIKey,
						"name":   "CLI (host)",
						"scopes": []string{"payments:read", "payments:write", "webhooks:read", "webhooks:write"},
						"mode":   "test",
					},
					"org": map[string]any{"id": "org_1", "name": "Acme"},
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]string{"status": step})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func runBrowserLoginAgainst(t *testing.T, server *httptest.Server) (string, error) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_MODE", "")

	var out bytes.Buffer

	cmd := loginCmd
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	err := browserLogin(cmd, false)

	return configPath, err
}

func TestBrowserLoginSavesTestModeKey(t *testing.T) {
	server := pairingServer(t, []string{"pending", "approved"})
	defer server.Close()

	configPath, err := runBrowserLoginAgainst(t, server)
	if err != nil {
		t.Fatalf("browserLogin: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var saved struct {
		APIKey string `json:"api_key"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if saved.APIKey != pairingAPIKey {
		t.Errorf("saved key = %q", saved.APIKey)
	}

	if saved.Mode != "test" {
		t.Errorf("saved mode = %q, want test — the login must land in test mode", saved.Mode)
	}
}

func TestBrowserLoginHonors429ThenSucceeds(t *testing.T) {
	server := pairingServer(t, []string{"429", "approved"})
	defer server.Close()

	if _, err := runBrowserLoginAgainst(t, server); err != nil {
		t.Fatalf("browserLogin after 429: %v", err)
	}
}

func TestBrowserLoginDenied(t *testing.T) {
	server := pairingServer(t, []string{"denied"})
	defer server.Close()

	_, err := runBrowserLoginAgainst(t, server)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want denied message", err)
	}
}

func TestBrowserLoginExpired(t *testing.T) {
	server := pairingServer(t, []string{"expired"})
	defer server.Close()

	_, err := runBrowserLoginAgainst(t, server)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want expired message", err)
	}
}

// A start response without expires_at used to leave the poll running for the
// life of the process. It must now stop at pairingDefaultTTL.
func TestBrowserLoginStopsWhenTheServerOmitsExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth":
			w.WriteHeader(http.StatusCreated)
			// No expires_at: the hazard this test pins.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "clia_1",
				"pairing_code": "GX7M-4KP9",
				"poll_secret":  "secret",
				"browser_url":  "https://dashboard.example/cli/confirm",
				"interval":     1,
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/cli/auth/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	previous := pairingDefaultTTL
	pairingDefaultTTL = 1 * time.Second

	t.Cleanup(func() { pairingDefaultTTL = previous })

	started := time.Now()

	_, err := runBrowserLoginAgainst(t, server)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want the expiry message", err)
	}

	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("poll ran for %s — the default TTL did not bound it", elapsed)
	}
}

// The first poll must go out before the first sleep, so a code the user
// already approved resolves immediately.
func TestBrowserLoginPollsBeforeSleeping(t *testing.T) {
	server := pairingServer(t, []string{"approved"})
	defer server.Close()

	started := time.Now()

	if _, err := runBrowserLoginAgainst(t, server); err != nil {
		t.Fatalf("browserLogin: %v", err)
	}

	// interval is 1s; an immediate first poll finishes well inside it.
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("took %s, want the first poll to precede the first sleep", elapsed)
	}
}

// Ctrl-C during the pairing wait must unwind with the conventional 130
// instead of leaving the poll running until the request expires.
func TestBrowserLoginPollStopsOnCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	start := pairingStartResponse{ID: "pair_1", PollSecret: "secret", Interval: 5}

	done := make(chan error, 1)

	go func() {
		_, err := pollPairing(ctx, server.Client(), server.URL, start, io.Discard)
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if got := ExitCode(err); got != 130 {
			t.Fatalf("ExitCode = %d, want 130 (err %v)", got, err)
		}

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want it to wrap context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the pairing poll ignored cancellation")
	}
}

// The poll response has always decoded `scopes` and never shown them, which
// left "what can this key actually do?" unanswerable without opening the
// dashboard. The paired key is minted by the backend, not chosen by the user,
// so the CLI is the only place that answer can come from.
func TestBrowserLoginPrintsTheKeyScopes(t *testing.T) {
	out, err := runBrowserLoginCapturing(t, pairingServer(t, []string{"approved"}))
	if err != nil {
		t.Fatalf("browserLogin: %v", err)
	}

	if !strings.Contains(out, "scopes: payments:read, payments:write, webhooks:read, webhooks:write") {
		t.Fatalf("output = %q, want the joined scope list from the poll response", out)
	}

	if !strings.Contains(out, "test-mode key") {
		t.Fatalf("output = %q, want the key's mode named", out)
	}
}

// `reevit init` runs this same flow when it finds no credential. Closing with
// "cd your-project && reevit init" in the middle of an init run would send the
// user in a circle, so the next step belongs to the login command alone.
func TestBrowserLoginOmitsTheNextStepWhenInitDrivesIt(t *testing.T) {
	server := pairingServer(t, []string{"approved"})

	loginOut, err := runBrowserLoginCapturing(t, server)
	if err != nil {
		t.Fatalf("browserLogin: %v", err)
	}

	if !strings.Contains(loginOut, "reevit init") {
		t.Fatalf("login output = %q, want the next step", loginOut)
	}

	initOut, err := runBrowserLoginCapturingAs(t, server, initCmd)
	if err != nil {
		t.Fatalf("browserLogin via init: %v", err)
	}

	if strings.Contains(initOut, "reevit init") {
		t.Fatalf("init output = %q, want no self-referential next step", initOut)
	}
}

func runBrowserLoginCapturing(t *testing.T, server *httptest.Server) (string, error) {
	t.Helper()

	return runBrowserLoginCapturingAs(t, server, loginCmd)
}

func runBrowserLoginCapturingAs(t *testing.T, server *httptest.Server, cmd *cobra.Command) (string, error) {
	t.Helper()

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_MODE", "")

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
	})

	err := browserLogin(cmd, false)

	return out.String(), err
}
