package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckoutFieldAndMetadataParsing(t *testing.T) {
	fields, err := ParseCheckoutFields([]string{"price", "EMAIL", "price"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fields, []CheckoutField{CheckoutFieldAmount, CheckoutFieldEmail}; len(got) != len(want) ||
		got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if _, err := ParseCheckoutFields([]string{"card_number"}); err == nil {
		t.Fatal("expected unknown checkout field to fail")
	}

	metadata, err := ParseMetadataFields([]string{"order_id", " product_sku ", "order_id"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(metadata, ",") != "order_id,product_sku" {
		t.Fatalf("metadata = %v", metadata)
	}
	if _, err := ParseMetadataFields([]string{"bad-key"}); err == nil {
		t.Fatal("expected unsafe metadata key to fail")
	}
}

func TestCheckoutPageCandidatesPreferFrameworkEntry(t *testing.T) {
	root := t.TempDir()
	writeCheckoutTestFile(t, root, "package.json", `{"dependencies":{"next":"16","react":"19"}}`)
	writeCheckoutTestFile(t, root, "tsconfig.json", `{}`)
	writeCheckoutTestFile(t, root, "src/components/Card.tsx", `export default () => <div />`)
	writeCheckoutTestFile(t, root, "src/app/page.tsx", `export default () => <main />`)

	candidates := CheckoutPageCandidates(Detect(root))
	if len(candidates) < 2 || candidates[0] != "src/app/page.tsx" {
		t.Fatalf("candidates = %v, want root page first", candidates)
	}
}

func TestConfiguredCheckoutRendersFieldsAndAddsManagedReactEdit(t *testing.T) {
	root := t.TempDir()
	writeCheckoutTestFile(t, root, "package.json", `{"dependencies":{"react":"19","vite":"7"},"devDependencies":{"typescript":"5"}}`)
	writeCheckoutTestFile(t, root, "tsconfig.json", `{}`)
	writeCheckoutTestFile(t, root, "src/App.tsx", `export default function App() {
  return (
    <main>
      <h1>Cart</h1>
    </main>
  );
}
`)
	project := Detect(root)
	target := TargetsFor(project)[0]
	target.Checkout = &CheckoutOptions{
		PagePath:       "src/App.tsx",
		Fields:         []CheckoutField{CheckoutFieldAmount, CheckoutFieldName, CheckoutFieldEmail},
		MetadataFields: []string{"order_id", "product_sku"},
	}
	if err := ConfigureCheckoutTarget(project, &target); err != nil {
		t.Fatal(err)
	}

	results, err := Apply(project, []Target{target}, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var managed bool
	for _, result := range results {
		if result.ManagedEdit {
			managed = true
			if result.Edit == nil || result.Edit.Kind != checkoutPlacementMarker {
				t.Fatalf("managed edit = %#v", result.Edit)
			}
		}
	}
	if !managed {
		t.Fatal("checkout page edit was not reported")
	}

	page := readCheckoutTestFile(t, root, "src/App.tsx")
	for _, want := range []string{
		`import { ReevitCheckoutButton } from "./components/ReevitCheckoutButton";`,
		`<ReevitCheckoutButton amount={5000} />`,
		"reevit-checkout:start",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q:\n%s", want, page)
		}
	}
	component := readCheckoutTestFile(t, root, "src/components/ReevitCheckoutButton.tsx")
	for _, want := range []string{
		"amount: true", "name: true", "email: true", "phone: false",
		`"order_id"`, `"product_sku"`, "customer_name", "customer_email", "customer_phone",
	} {
		if !strings.Contains(component, want) {
			t.Fatalf("component missing %q:\n%s", want, component)
		}
	}

	if _, err := Apply(project, []Target{target}, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	page = readCheckoutTestFile(t, root, "src/App.tsx")
	if strings.Count(page, "reevit-checkout:start") != 1 {
		t.Fatalf("checkout placement is not idempotent:\n%s", page)
	}
}

func TestCheckoutPlacementSupportsVueSvelteAndSelfClosingReact(t *testing.T) {
	tests := []struct {
		name, packageJSON, page, source, wantImport, wantButton string
	}{
		{
			name: "vue", packageJSON: `{"dependencies":{"vue":"3","vite":"7"}}`,
			page: "src/App.vue", source: `<template><main>Cart</main></template>`,
			wantImport: `import ReevitCheckoutButton from "./components/ReevitCheckoutButton.vue";`,
			wantButton: `<ReevitCheckoutButton :amount="5000" />`,
		},
		{
			name: "svelte", packageJSON: `{"dependencies":{"svelte":"5","vite":"7"}}`,
			page: "src/App.svelte", source: `<main>Cart</main>`,
			wantImport: `import ReevitCheckoutButton from "./lib/ReevitCheckoutButton.svelte";`,
			wantButton: `<ReevitCheckoutButton amount={5000} />`,
		},
		{
			name: "react self-closing", packageJSON: `{"dependencies":{"react":"19","vite":"7"}}`,
			page: "src/App.jsx", source: `export default function App() { return <main />; }`,
			wantImport: `import { ReevitCheckoutButton } from "./components/ReevitCheckoutButton";`,
			wantButton: `<ReevitCheckoutButton amount={5000} />`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeCheckoutTestFile(t, root, "package.json", test.packageJSON)
			writeCheckoutTestFile(t, root, test.page, test.source)
			project := Detect(root)
			target := TargetsFor(project)[0]
			target.Checkout = &CheckoutOptions{PagePath: test.page}
			if err := ConfigureCheckoutTarget(project, &target); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(project, []Target{target}, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			page := readCheckoutTestFile(t, root, test.page)
			for _, want := range []string{test.wantImport, test.wantButton, "reevit-checkout:start"} {
				if !strings.Contains(page, want) {
					t.Fatalf("page missing %q:\n%s", want, page)
				}
			}
		})
	}
}

func TestConfigureCheckoutRejectsMissingOrEscapingPage(t *testing.T) {
	root := t.TempDir()
	writeCheckoutTestFile(t, root, "package.json", `{"dependencies":{"react":"19","vite":"7"}}`)
	project := Detect(root)
	for _, page := range []string{"missing.jsx", "../outside.jsx"} {
		target := TargetsFor(project)[0]
		target.Checkout = &CheckoutOptions{PagePath: page}
		if err := ConfigureCheckoutTarget(project, &target); err == nil {
			t.Fatalf("page %q should fail", page)
		}
	}
}

func writeCheckoutTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCheckoutTestFile(t *testing.T, root, relative string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
