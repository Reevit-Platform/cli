package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
	"github.com/spf13/cobra"
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

func TestConfigureCheckoutPromptsForPageAndWorkflowMetadataFields(t *testing.T) {
	initCheckoutPage = ""
	initCheckoutFields = nil
	initCheckoutMeta = nil
	initYes = false
	initTargets = []string{"checkout"}
	defer func() {
		initCheckoutPage = ""
		initCheckoutFields = nil
		initCheckoutMeta = nil
		initTargets = nil
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"react":"19"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src/App.jsx"), []byte("export default () => <main />"), 0o644); err != nil {
		t.Fatal(err)
	}

	project := scaffold.Detect(dir)
	targets := scaffold.TargetsFor(project)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("\n1,2,3,4\norder_id, product_sku\n"))

	if err := configureCheckout(cmd, project, targets); err != nil {
		t.Fatalf("configureCheckout: %v", err)
	}

	got := targets[0].Checkout
	if got == nil || got.PagePath != "src/App.jsx" {
		t.Fatalf("checkout options = %+v", got)
	}
	if len(got.Fields) != 4 {
		t.Fatalf("fields = %v, want four selected fields", got.Fields)
	}
	if strings.Join(got.MetadataFields, ",") != "order_id,product_sku" {
		t.Fatalf("metadata fields = %v", got.MetadataFields)
	}
}

func TestConfigureCheckoutPromptsForChoicesMissingFromPartialFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"react":"19"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src/App.jsx"), []byte("export default () => <main />"), 0o644); err != nil {
		t.Fatal(err)
	}

	initCheckoutPage = "src/App.jsx"
	initCheckoutFields = nil
	initCheckoutMeta = nil
	initYes = false
	initTargets = []string{"checkout"}
	defer func() {
		initCheckoutPage = ""
		initCheckoutFields = nil
		initCheckoutMeta = nil
		initTargets = nil
	}()

	project := scaffold.Detect(dir)
	targets := scaffold.TargetsFor(project)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("1,3\n\n"))

	if err := configureCheckout(cmd, project, targets); err != nil {
		t.Fatalf("configureCheckout: %v", err)
	}
	if strings.Contains(out.String(), "Which existing page") {
		t.Fatalf("page prompt should be skipped when --checkout-page is set:\n%s", out.String())
	}
	got := targets[0].Checkout
	if got == nil || got.PagePath != "src/App.jsx" || len(got.Fields) != 2 {
		t.Fatalf("checkout options = %+v", got)
	}
}
