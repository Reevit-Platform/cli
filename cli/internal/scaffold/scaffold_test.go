package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderAllTemplates renders every embedded template in both TS and JS
// mode — a syntax error in any template fails here, not at a user's keyboard.
func TestRenderAllTemplates(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) == 0 {
		t.Fatal("no templates embedded")
	}

	for _, entry := range entries {
		for _, ts := range []bool{true, false} {
			out, err := render(entry.Name(), templateData{TS: ts})
			if err != nil {
				t.Errorf("render %s (ts=%v): %v", entry.Name(), ts, err)

				continue
			}

			if strings.Contains(out, "{{") {
				t.Errorf("render %s (ts=%v): unexpanded template syntax in output", entry.Name(), ts)
			}
		}
	}
}

// TestRenderTSConditionals spot-checks the TS/JS switch actually changes output.
func TestRenderTSConditionals(t *testing.T) {
	tsOut, err := render("next-webhook.ts.tmpl", templateData{TS: true})
	if err != nil {
		t.Fatal(err)
	}

	jsOut, err := render("next-webhook.ts.tmpl", templateData{TS: false})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(tsOut, "request: Request") {
		t.Error("TS render must include the type annotation")
	}

	if strings.Contains(jsOut, ": Request") {
		t.Error("JS render must not include type annotations")
	}
}

func TestApplyWritesAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "tsconfig.json", "{}")

	project := Detect(dir)
	targets := TargetsFor(project)

	if len(targets) != 3 {
		t.Fatalf("next targets = %d, want 3", len(targets))
	}

	results, err := Apply(project, targets)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wrote := map[string]bool{}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("unexpected skip on fresh project: %s", r.Path)
		}

		wrote[r.Path] = true
	}

	if !wrote["app/api/webhooks/reevit/route.ts"] {
		t.Errorf("webhook route not written; wrote %v", wrote)
	}

	// Second apply: everything must be skipped, contents untouched.
	marker := filepath.Join(dir, "app/api/webhooks/reevit/route.ts")
	if err := os.WriteFile(marker, []byte("user edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err = Apply(project, targets)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	for _, r := range results {
		if !r.Skipped {
			t.Errorf("second apply must skip %s", r.Path)
		}
	}

	raw, _ := os.ReadFile(marker)
	if string(raw) != "user edited" {
		t.Error("Apply overwrote a user-edited file")
	}
}

func TestApplyHonorsSrcDirAndJS(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "src/app/layout.jsx", "")

	project := Detect(dir)

	if project.TypeScript {
		t.Fatal("project must detect as JS")
	}

	results, err := Apply(project, TargetsFor(project))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var paths []string
	for _, r := range results {
		paths = append(paths, r.Path)
	}

	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "src/app/api/webhooks/reevit/route.js") {
		t.Errorf("JS src-dir project paths wrong: %v", paths)
	}
}

func TestApplyAddsConfiguredCheckoutToExistingReactPage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "src/App.tsx", `export default function App() {
  return (
    <main>
      <h1>Store</h1>
    </main>
  );
}
`)

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{
		PagePath:       "src/App.tsx",
		Fields:         []CheckoutField{CheckoutFieldAmount, CheckoutFieldName, CheckoutFieldEmail, CheckoutFieldPhone},
		MetadataFields: []string{"order_id", "product_sku"},
	}

	results, err := Apply(project, targets)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	page, err := os.ReadFile(filepath.Join(dir, "src/App.tsx"))
	if err != nil {
		t.Fatal(err)
	}

	pageText := string(page)
	for _, want := range []string{
		`import { ReevitCheckoutButton } from "./components/ReevitCheckoutButton";`,
		`<ReevitCheckoutButton amount={5000} />`,
		"reevit-checkout:start",
	} {
		if !strings.Contains(pageText, want) {
			t.Errorf("page missing %q:\n%s", want, pageText)
		}
	}

	component, err := os.ReadFile(filepath.Join(dir, "src/components/ReevitCheckoutButton.tsx"))
	if err != nil {
		t.Fatal(err)
	}

	componentText := string(component)
	for _, want := range []string{
		"amount: true",
		"name: true",
		"email: true",
		"phone: true",
		`"order_id"`,
		`"product_sku"`,
		"customer_name",
		"customer_email",
		"customer_phone",
	} {
		if !strings.Contains(componentText, want) {
			t.Errorf("component missing %q", want)
		}
	}

	var pageUpdated bool
	for _, result := range results {
		if result.Path == "src/App.tsx" && result.Updated {
			pageUpdated = true
		}
	}
	if !pageUpdated {
		t.Errorf("page update not reported: %+v", results)
	}

	// Re-running is safe: neither the import nor the button is duplicated.
	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	page, _ = os.ReadFile(filepath.Join(dir, "src/App.tsx"))
	if strings.Count(string(page), "reevit-checkout:start") != 1 {
		t.Errorf("checkout insertion duplicated:\n%s", page)
	}
}

func TestApplyCheckoutPageMustExistInsideProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "../outside.tsx"}

	if _, err := Apply(project, targets); err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Fatalf("Apply error = %v, want safe page-path error", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "src/components/ReevitCheckoutButton.jsx")); !os.IsNotExist(err) {
		t.Fatal("Apply wrote the component before validating the page path")
	}
}

func TestCheckoutPageCandidatesPrefersFrameworkEntryPage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16","react":"19"}}`)
	write(t, dir, "src/app/page.tsx", "export default function Page() { return <main /> }")
	write(t, dir, "src/app/account/page.tsx", "export default function Page() { return <main /> }")
	write(t, dir, "src/components/Card.tsx", "export function Card() { return <div /> }")

	got := CheckoutPageCandidates(Detect(dir))
	if len(got) < 2 || got[0] != "src/app/page.tsx" {
		t.Fatalf("CheckoutPageCandidates = %v, want root page first", got)
	}
}

func TestApplyAddsCheckoutToVueAndSveltePages(t *testing.T) {
	cases := []struct {
		name        string
		packageJSON string
		page        string
		pageContent string
		wantImport  string
		wantButton  string
	}{
		{
			name:        "vue",
			packageJSON: `{"dependencies":{"vue":"3"}}`,
			page:        "src/App.vue",
			pageContent: "<script setup>\n</script>\n<template><main>Store</main></template>\n",
			wantImport:  `import ReevitCheckoutButton from "./components/ReevitCheckoutButton.vue";`,
			wantButton:  `<ReevitCheckoutButton :amount="5000" />`,
		},
		{
			name:        "svelte",
			packageJSON: `{"dependencies":{"svelte":"5"}}`,
			page:        "src/routes/+page.svelte",
			pageContent: "<script>\n</script>\n<main>Store</main>\n",
			wantImport:  `import ReevitCheckoutButton from "../lib/ReevitCheckoutButton.svelte";`,
			wantButton:  `<ReevitCheckoutButton amount={5000} />`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "package.json", tc.packageJSON)
			write(t, dir, tc.page, tc.pageContent)
			project := Detect(dir)
			targets := TargetsFor(project)
			targets[0].Checkout = &CheckoutOptions{
				PagePath: tc.page,
				Fields:   []CheckoutField{CheckoutFieldEmail},
			}

			if _, err := Apply(project, targets); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, tc.page))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{tc.wantImport, tc.wantButton, "reevit-checkout:start"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("page missing %q:\n%s", want, raw)
				}
			}
		})
	}
}

func TestApplyWrapsSelfClosingReactPageToAddCheckout(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", "export default function App() {\n  return <main />;\n}\n")

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"return <>", "<main />", "<ReevitCheckoutButton amount={5000} />", "</>;"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("page missing %q:\n%s", want, raw)
		}
	}
}

func TestApplyAddsCheckoutToExportedPageNotLaterHelper(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", `export default function App() {
  return <main><h1>Store</h1></main>;
}

function Helper() {
  return <aside><p>Help</p></aside>;
}
`)

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	text := string(raw)
	buttonAt := strings.Index(text, "<ReevitCheckoutButton")
	mainCloseAt := strings.Index(text, "</main>")
	helperAt := strings.Index(text, "function Helper")
	if buttonAt < 0 || mainCloseAt < 0 || helperAt < 0 || !(buttonAt < mainCloseAt && mainCloseAt < helperAt) {
		t.Fatalf("checkout was not inserted into exported page root:\n%s", text)
	}
}

func TestApplyAddsCheckoutToSeparatelyExportedReactComponent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", `function App() {
  return <main><h1>Store</h1></main>;
}

export default App;
`)

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	if !strings.Contains(string(raw), "<ReevitCheckoutButton amount={5000} />") {
		t.Fatalf("checkout missing from separately exported component:\n%s", raw)
	}
}

func TestApplyRegistersCheckoutInVueOptionsAPIPage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"vue":"3"}}`)
	write(t, dir, "src/App.vue", `<script>
export default { name: "App" }
</script>
<template><main>Store</main></template>
`)

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.vue"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.vue"))
	if !strings.HasPrefix(string(raw), "<script setup>") {
		t.Fatalf("Options API page did not receive a template-visible script setup import:\n%s", raw)
	}
}

func TestApplyKeepsOneLineVueAndSvelteScriptsValid(t *testing.T) {
	cases := []struct {
		name        string
		packageJSON string
		page        string
		content     string
	}{
		{"vue", `{"dependencies":{"vue":"3"}}`, "src/App.vue", "<script setup>const x = 1;</script>\n<template><main /></template>"},
		{"svelte", `{"dependencies":{"svelte":"5"}}`, "src/routes/+page.svelte", "<script>const x = 1;</script>\n<main />"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "package.json", tc.packageJSON)
			write(t, dir, tc.page, tc.content)
			project := Detect(dir)
			targets := TargetsFor(project)
			targets[0].Checkout = &CheckoutOptions{PagePath: tc.page}

			if _, err := Apply(project, targets); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			raw, _ := os.ReadFile(filepath.Join(dir, tc.page))
			if strings.Contains(string(raw), "1;import") {
				t.Fatalf("import was concatenated with existing statement:\n%s", raw)
			}
		})
	}
}

func TestApplyUsesVueScriptSetupRegardlessOfAttributeOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"vue":"3"}}`)
	write(t, dir, "src/App.vue", `<script lang="ts" setup>const x = 1;</script>
<template><main /></template>`)
	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.vue"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.vue"))
	if strings.Count(string(raw), "<script") != 1 {
		t.Fatalf("a duplicate script setup block was added:\n%s", raw)
	}
}

func TestApplyPreservesOldCheckoutAndGeneratesConfiguredSibling(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", "export default () => <main />")
	oldPath := "src/components/ReevitCheckoutButton.jsx"
	write(t, dir, oldPath, "// developer-edited checkout from an earlier run\n")

	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{
		PagePath:       "src/App.jsx",
		Fields:         []CheckoutField{CheckoutFieldEmail},
		MetadataFields: []string{"order_id"},
	}

	results, err := Apply(project, targets)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	old, _ := os.ReadFile(filepath.Join(dir, oldPath))
	if string(old) != "// developer-edited checkout from an earlier run\n" {
		t.Fatal("existing checkout component was overwritten")
	}

	var configuredPath string
	for _, result := range results {
		if strings.Contains(result.Path, ".reevit-") && !result.Skipped {
			configuredPath = result.Path
		}
	}
	if configuredPath == "" {
		t.Fatalf("configured sibling was not generated: %+v", results)
	}
	configured, _ := os.ReadFile(filepath.Join(dir, configuredPath))
	for _, want := range []string{"email: true", `"order_id"`} {
		if !strings.Contains(string(configured), want) {
			t.Errorf("configured sibling missing %q", want)
		}
	}
	page, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	if !strings.Contains(string(page), strings.TrimSuffix(filepath.Base(configuredPath), filepath.Ext(configuredPath))) {
		t.Fatalf("page does not import configured sibling:\n%s", page)
	}
}

func TestApplyHandlesJSXEventHandlerArrows(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", `export default function App() {
  return <main><button onClick={() => submit()}>Pay</button></main>;
}`)
	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	if !strings.Contains(string(raw), "<ReevitCheckoutButton amount={5000} />") {
		t.Fatalf("checkout missing from JSX with arrow attribute:\n%s", raw)
	}
}

func TestApplyRewiresMarkedPageWhenCheckoutConfigurationChanges(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", "export default () => <main />")
	project := Detect(dir)

	first := TargetsFor(project)
	first[0].Checkout = &CheckoutOptions{
		PagePath: "src/App.jsx",
		Fields:   []CheckoutField{CheckoutFieldEmail},
	}
	if _, err := Apply(project, first); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	second := TargetsFor(project)
	second[0].Checkout = &CheckoutOptions{
		PagePath: "src/App.jsx",
		Fields:   []CheckoutField{CheckoutFieldPhone},
	}
	if _, err := Apply(project, second); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	page, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	pageText := string(page)
	if strings.Count(pageText, "reevit-checkout:start") != 1 || !strings.Contains(pageText, ".reevit-") {
		t.Fatalf("marked page was not rewired idempotently:\n%s", pageText)
	}
	importStart := strings.Index(pageText, `from "`)
	importEnd := strings.Index(pageText[importStart+6:], `"`)
	if importStart < 0 || importEnd < 0 {
		t.Fatalf("could not read configured import:\n%s", pageText)
	}
	importRel := pageText[importStart+6 : importStart+6+importEnd]
	configuredPath := filepath.Join(dir, "src", strings.TrimPrefix(importRel, "./")+".jsx")
	configured, err := os.ReadFile(configuredPath)
	if err != nil {
		t.Fatalf("read rewired component: %v", err)
	}
	if !strings.Contains(string(configured), "phone: true") {
		t.Fatalf("rewired component does not use new fields:\n%s", configured)
	}
}

func TestApplyIgnoresNestedCallbackReturnInPageFunction(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "src/App.jsx", `export default function App() {
  const renderItem = () => {
    return <li>Item</li>;
  };
  return <main><ul>{renderItem()}</ul></main>;
}`)
	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.jsx"))
	text := string(raw)
	buttonAt := strings.Index(text, "<ReevitCheckoutButton")
	listCloseAt := strings.Index(text, "</li>")
	mainCloseAt := strings.Index(text, "</main>")
	if !(listCloseAt < buttonAt && buttonAt < mainCloseAt) {
		t.Fatalf("checkout was inserted into nested callback instead of page root:\n%s", text)
	}
}

func TestApplyHandlesDestructuredTypedPageProps(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"react":"19"}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "src/App.tsx", `export default function App(
  { params }: { params: { slug: string } }
): JSX.Element {
  return <main>{params.slug}</main>;
}`)
	project := Detect(dir)
	targets := TargetsFor(project)
	targets[0].Checkout = &CheckoutOptions{PagePath: "src/App.tsx"}

	if _, err := Apply(project, targets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "src/App.tsx"))
	if !strings.Contains(string(raw), "<ReevitCheckoutButton amount={5000} />") {
		t.Fatalf("checkout missing from typed/destructured page:\n%s", raw)
	}
}

func TestCheckoutFieldParsingRejectsUnsafeOrUnknownValues(t *testing.T) {
	fields, err := ParseCheckoutFields([]string{"price"})
	if err != nil || len(fields) != 1 || fields[0] != CheckoutFieldAmount {
		t.Fatalf("price alias = %v, %v; want amount", fields, err)
	}
	if _, err := ParseCheckoutFields([]string{"email", "card_number"}); err == nil {
		t.Fatal("unknown standard checkout field accepted")
	}
	if _, err := ParseMetadataFields([]string{"order_id", "bad-key"}); err == nil {
		t.Fatal("unsafe metadata field accepted")
	}
}

func TestNpmInstallPlansCoversEveryLockfile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "bun.lock", "")
	write(t, dir, "pnpm-lock.yaml", "")

	project := Detect(dir)
	plans := NpmInstallPlans(project, TargetsFor(project))

	if len(plans) != 2 {
		t.Fatalf("plans = %v, want one per lockfile manager", plans)
	}

	// @reevit/node appears in two targets but must be installed once per plan.
	for _, plan := range plans {
		joined := strings.Join(plan, " ")
		if strings.Count(joined, "@reevit/node") != 1 {
			t.Errorf("deduplication failed: %v", plan)
		}

		if !strings.Contains(joined, "@reevit/react") {
			t.Errorf("missing checkout package: %v", plan)
		}
	}
}

func TestGoTargetsNeedNoNpm(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x")

	project := Detect(dir)
	targets := TargetsFor(project)

	if plans := NpmInstallPlans(project, targets); plans != nil {
		t.Errorf("go project must have no npm plans, got %v", plans)
	}

	run, show := OtherInstallCmds(targets)
	if len(run) != 1 || !strings.Contains(strings.Join(run[0], " "), "go get") {
		t.Errorf("go get must be runnable, got run=%v show=%v", run, show)
	}
}
