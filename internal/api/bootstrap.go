package api

import (
	"context"
	"net/http"
)

type BootstrapCredential struct {
	ID     string   `json:"id"`
	Raw    string   `json:"raw,omitempty"`
	Scopes []string `json:"scopes"`
}

type BootstrapRequest struct {
	ProjectID             string   `json:"project_id"`
	ProjectName           string   `json:"project_name"`
	Capabilities          []string `json:"capabilities"`
	Origin                string   `json:"origin,omitempty"`
	ExistingServerKeyID   string   `json:"existing_server_key_id,omitempty"`
	ExistingCheckoutKeyID string   `json:"existing_checkout_key_id,omitempty"`
	RotateCredentials     bool     `json:"rotate_credentials"`
}

type BootstrapResult struct {
	Project struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		OrganizationID string `json:"organization_id"`
	} `json:"project"`
	Mode        string `json:"mode"`
	Credentials struct {
		Server   *BootstrapCredential `json:"server"`
		Checkout *BootstrapCredential `json:"checkout"`
	} `json:"credentials"`
	Simulator struct {
		ConnectionID string `json:"connection_id"`
		Ready        bool   `json:"ready"`
	} `json:"simulator"`
	Checkout struct {
		Origin        string `json:"origin"`
		OriginAllowed bool   `json:"origin_allowed"`
	} `json:"checkout"`
}

func (c *Client) BootstrapProject(ctx context.Context, input BootstrapRequest) (BootstrapResult, error) {
	var result BootstrapResult
	err := c.Do(ctx, Request{
		Method: http.MethodPost, Path: "/cli/bootstrap", Body: input, Idempotent: true,
	}, &result)
	return result, err
}
