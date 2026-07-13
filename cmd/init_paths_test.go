package cmd

import (
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func TestApplyPathOverrides(t *testing.T) {
	initWebhookPath = "src/server/hooks/reevit.ts"
	initCheckoutPath = ""

	defer func() { initWebhookPath = "" }()

	targets := []scaffold.Target{
		{Key: scaffold.TargetWebhook, Files: map[string]string{"next-webhook.ts.tmpl": "app/api/webhooks/reevit/route.ts"}},
		{Key: scaffold.TargetCheckout, Files: map[string]string{"next-checkout.tsx.tmpl": "components/reevit-checkout-button.tsx"}},
	}

	applyPathOverrides(targets)

	if targets[0].Files["next-webhook.ts.tmpl"] != "src/server/hooks/reevit.ts" {
		t.Errorf("webhook path not overridden: %v", targets[0].Files)
	}

	if targets[1].Files["next-checkout.tsx.tmpl"] != "components/reevit-checkout-button.tsx" {
		t.Errorf("checkout path must keep its default: %v", targets[1].Files)
	}
}
