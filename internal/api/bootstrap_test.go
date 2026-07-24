package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Reevit-Platform/cli/internal/config"
)

func TestBootstrapProjectUsesLoginKeyOnlyForAuthorization(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cli/bootstrap" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Reevit-Key"); got != "pfk_test_login.secret" {
			t.Fatalf("authorization key = %q", got)
		}
		var body BootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.ProjectID != "rvproj_123" || len(body.Capabilities) != 2 {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"project":{"id":"rvproj_123","name":"shop","organization_id":"org_123"},
			"mode":"test",
			"credentials":{
				"server":{"id":"pfk_test_server","raw":"pfk_test_server.secret","scopes":["payments:read","payments:write"]},
				"checkout":{"id":"pfk_test_checkout","raw":"pfk_test_checkout.secret","scopes":["checkout:write"]}
			},
			"simulator":{"connection_id":"conn_sim","ready":true},
			"checkout":{"origin":"http://localhost:3000","origin_allowed":true}
		}`))
	}))
	defer server.Close()

	client := New(config.Config{
		APIKey: "pfk_test_login.secret", BaseURL: server.URL, Mode: "test",
	})
	got, err := client.BootstrapProject(context.Background(), BootstrapRequest{
		ProjectID: "rvproj_123", ProjectName: "shop",
		Capabilities: []string{"server", "checkout"},
		Origin:       "http://localhost:3000",
	})
	if err != nil {
		t.Fatalf("BootstrapProject() error = %v", err)
	}
	if got.Credentials.Server.Raw != "pfk_test_server.secret" ||
		got.Credentials.Checkout.Raw != "pfk_test_checkout.secret" {
		t.Fatalf("credentials = %#v", got.Credentials)
	}
}
