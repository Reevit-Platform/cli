package api

import (
	"context"
)

// AccountSummary is the safe organization identity shown by setup prompts.
type AccountSummary struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

// AccountSummary returns the organization attached to the configured login key.
func (c *Client) AccountSummary(ctx context.Context) (AccountSummary, error) {
	var result AccountSummary
	err := c.Do(ctx, Request{Path: "/cli/account"}, &result)

	return result, err
}
