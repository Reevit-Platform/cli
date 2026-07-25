package cmd

import (
	"bytes"
	"strings"
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

func TestConfigureCheckoutPromptsForPageFieldsAndMetadata(t *testing.T) {
	resetInitTestFlags()
	defer resetInitTestFlags()

	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{"dependencies":{"react":"19","vite":"7"}}`)
	writeAcceptanceFile(t, root, "src/App.jsx", `export default function App() { return <main />; }`)
	project := scaffold.Detect(root)
	targets := scaffold.TargetsFor(project)

	var out bytes.Buffer
	initCmd.SetIn(strings.NewReader("\n1,2,3\norder_id,product_sku\n"))
	initCmd.SetOut(&out)
	defer func() {
		initCmd.SetIn(nil)
		initCmd.SetOut(nil)
	}()
	if err := configureCheckout(initCmd, project, targets, true); err != nil {
		t.Fatal(err)
	}
	options := targets[0].Checkout
	if options == nil || options.PagePath != "src/App.jsx" {
		t.Fatalf("checkout options = %#v", options)
	}
	if got := len(options.Fields); got != 3 {
		t.Fatalf("fields = %v", options.Fields)
	}
	if strings.Join(options.MetadataFields, ",") != "order_id,product_sku" {
		t.Fatalf("metadata = %v", options.MetadataFields)
	}
	if len(targets[0].Edits) != 1 || targets[0].Edits[0].Path != "src/App.jsx" {
		t.Fatalf("checkout edits = %#v", targets[0].Edits)
	}
}

func TestConfigureCheckoutFlagsAndStandaloneSentinel(t *testing.T) {
	resetInitTestFlags()
	defer resetInitTestFlags()

	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{"dependencies":{"react":"19","vite":"7"}}`)
	project := scaffold.Detect(root)

	initCheckoutPage = "-"
	initCheckoutFields = []string{"price,email"}
	initCheckoutMeta = []string{"order_id,product_sku"}
	targets := scaffold.TargetsFor(project)
	if err := configureCheckout(initCmd, project, targets, false); err != nil {
		t.Fatal(err)
	}
	options := targets[0].Checkout
	if options == nil || options.PagePath != "" || len(targets[0].Edits) != 0 {
		t.Fatalf("standalone checkout = %#v, edits = %#v", options, targets[0].Edits)
	}
	if len(options.Fields) != 2 || options.Fields[0] != scaffold.CheckoutFieldAmount {
		t.Fatalf("fields = %v", options.Fields)
	}
}

func TestCheckoutFlagsRequireCheckoutTarget(t *testing.T) {
	resetInitTestFlags()
	defer resetInitTestFlags()
	initCheckoutFields = []string{"email"}
	if err := configureCheckout(
		initCmd,
		scaffold.Project{},
		[]scaffold.Target{{Key: scaffold.TargetWebhook}},
		false,
	); err == nil {
		t.Fatal("expected checkout flags without checkout target to fail")
	}
}
