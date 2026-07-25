package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Reevit-Platform/cli/internal/config"
)

func TestAccountSummary(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.URL.Path != "/v1/cli/account" ||
			r.Header.Get("X-Reevit-Key") != "pfk_test_login.secret" {
			t.Fatalf("request path=%q key=%q", r.URL.Path, r.Header.Get("X-Reevit-Key"))
		}
		_ = json.NewEncoder(w).Encode(AccountSummary{
			OrganizationID: "org_123", OrganizationName: "Acme",
		})
	}))
	defer server.Close()

	got, err := New(config.Config{
		APIKey: "pfk_test_login.secret", BaseURL: server.URL, Mode: "test",
	}).AccountSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OrganizationName != "Acme" || got.OrganizationID != "org_123" {
		t.Fatalf("summary = %#v", got)
	}
}
