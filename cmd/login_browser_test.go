package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
						"id":     "pfk_test_abc",
						"raw":    "pfk_test_abc.sec",
						"name":   "CLI (host)",
						"scopes": []string{"payments:read"},
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

	if saved.APIKey != "pfk_test_abc.sec" {
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
