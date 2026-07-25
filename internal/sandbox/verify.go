// Package sandbox performs API-level checks against the project credentials
// created by init. These checks do not start or automate the developer's app.
package sandbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

type Result struct {
	PaymentID    string
	PaymentState string
	SessionID    string
}

func VerifyServerPayment(ctx context.Context, baseURL, key string) (Result, error) {
	client := api.New(config.Config{APIKey: key, BaseURL: baseURL, Mode: "test"})
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	err := client.Do(ctx, api.Request{
		Method: http.MethodPost, Path: "/payments/intents", Idempotent: true,
		Body: map[string]any{
			"amount": 4000, "currency": "GHS", "method": "mobile_money",
			"country": "GH", "description": "Reevit init verification",
			"customer_id": verificationCustomerID(),
			"metadata":    map[string]any{"created_via": "reevit-init"},
		},
	}, &response)
	if err != nil {
		return Result{}, fmt.Errorf("verify server payment: %w", err)
	}
	if response.ID == "" {
		return Result{}, fmt.Errorf("verify server payment: API returned no payment id")
	}
	return Result{PaymentID: response.ID, PaymentState: response.Status}, nil
}

func VerifyCheckout(ctx context.Context, baseURL, key string) (Result, error) {
	client := api.New(config.Config{APIKey: key, BaseURL: baseURL, Mode: "test"})
	var response struct {
		ID            string `json:"id"`
		ClientSecret  string `json:"client_secret"`
		SessionSecret string `json:"session_secret"`
	}
	err := client.Do(ctx, api.Request{
		Method: http.MethodPost, Path: "/checkout/sessions", Idempotent: true,
		Body: map[string]any{
			"amount": 5000, "currency": "GHS", "method": "mobile_money",
			"country": "GH", "description": "Reevit init checkout verification",
			"customer_id": verificationCustomerID(),
			"metadata":    map[string]any{"created_via": "reevit-init"},
		},
	}, &response)
	if err != nil {
		return Result{}, fmt.Errorf("verify checkout initialization: %w", err)
	}
	if response.ID == "" || (response.SessionSecret == "" && response.ClientSecret == "") {
		return Result{}, fmt.Errorf("verify checkout initialization: API returned no usable session")
	}
	return Result{SessionID: response.ID}, nil
}

func verificationCustomerID() string {
	return "reevit_cli_verify_" + uuid.NewString()
}
