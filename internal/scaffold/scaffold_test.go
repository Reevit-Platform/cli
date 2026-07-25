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

func TestNextCheckoutCreatesRunnableDemoRoute(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext, TypeScript: true}
	var checkout Target
	for _, target := range TargetsFor(project) {
		if target.Key == TargetCheckout {
			checkout = target
		}
	}
	if _, ok := checkout.Files["next-app-demo-page.tsx.tmpl"]; !ok {
		t.Fatalf("checkout files = %#v; missing runnable demo page", checkout.Files)
	}
	if _, err := Apply(project, []Target{checkout}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	page := readFile(t, project.Root, "app/reevit-demo/page.tsx")
	if !strings.Contains(page, "ReevitCheckoutButton") {
		t.Fatalf("demo page does not mount checkout: %s", page)
	}
}

func TestSPAAdaptersCreateRoutableDemoEntries(t *testing.T) {
	t.Parallel()

	for _, stack := range []Stack{StackReact, StackVue, StackSvelte} {
		t.Run(string(stack), func(t *testing.T) {
			project := Project{Root: t.TempDir(), Stack: stack, TypeScript: true}
			if _, err := Apply(project, TargetsFor(project)); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			html := readFile(t, project.Root, "reevit-demo.html")
			if !strings.Contains(html, "src/reevit-demo") {
				t.Fatalf("demo HTML is not wired to an entry: %s", html)
			}
		})
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

func TestApplyCanReplaceGeneratedFilesWithBackup(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "tsconfig.json", "{}")

	project := Detect(dir)
	targets := TargetsFor(project)
	checkout := []Target{targets[1]}
	component := "components/reevit-checkout-button.tsx"
	write(t, dir, component, "user edited checkout")

	results, err := Apply(project, checkout, ApplyOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("Apply overwrite: %v", err)
	}

	var backupPath string
	for _, result := range results {
		if result.Path == component {
			backupPath = result.BackupPath
		}
	}
	if backupPath == "" {
		t.Fatal("overwritten component did not report a backup")
	}
	if got := readFile(t, dir, backupPath); got != "user edited checkout" {
		t.Fatalf("backup = %q", got)
	}
	if got := readFile(t, dir, component); got == "user edited checkout" {
		t.Fatal("component was not replaced")
	}
	if gitignore := readFile(t, dir, ".gitignore"); !strings.Contains(gitignore, ".reevit/backups/") {
		t.Fatalf("backup directory is not gitignored:\n%s", gitignore)
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

func TestNpmInstallPlansUsesOneDetectedManager(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "bun.lock", "")
	write(t, dir, "pnpm-lock.yaml", "")

	project := Detect(dir)
	plans := NpmInstallPlans(project, TargetsFor(project))

	if len(plans) != 1 || plans[0][0] != "bun" {
		t.Fatalf("plans = %v, want one command for the selected manager", plans)
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

func TestRuntimeHintsUseFrameworkAndPackageManager(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{
		"packageManager": "pnpm@10.0.0",
		"scripts": {"dev": "vite"},
		"dependencies": {"react": "19.0.0"}
	}`)

	project := Detect(root)
	if got := DefaultPort(project); got != 5173 {
		t.Fatalf("DefaultPort() = %d, want 5173", got)
	}
	if got := strings.Join(DevCommand(project), " "); got != "pnpm dev" {
		t.Fatalf("DevCommand() = %q, want pnpm dev", got)
	}
	if got := DemoPath(project); got != "/reevit-demo.html" {
		t.Fatalf("DemoPath() = %q, want /reevit-demo.html", got)
	}
}

func TestPythonWebhookUsesDetectedFramework(t *testing.T) {
	t.Parallel()

	tests := []struct {
		framework Framework
		template  string
		needle    string
	}{
		{FrameworkFastAPI, "python-fastapi-webhook.py.tmpl", "APIRouter"},
		{FrameworkFlask, "python-flask-webhook.py.tmpl", "Blueprint"},
		{FrameworkDjango, "python-django-webhook.py.tmpl", "HttpResponse"},
		{FrameworkGeneric, "python-webhook.py.tmpl", "handle_reevit_event"},
	}

	for _, test := range tests {
		t.Run(string(test.framework), func(t *testing.T) {
			project := Project{
				Root: t.TempDir(), Stack: StackPython,
				Framework: test.framework, Installer: InstallerPip,
			}
			var webhook Target
			for _, target := range TargetsFor(project) {
				if target.Key == TargetWebhook {
					webhook = target
				}
			}
			if _, ok := webhook.Files[test.template]; !ok {
				t.Fatalf("webhook files = %#v, want template %s", webhook.Files, test.template)
			}
			if _, err := Apply(project, []Target{webhook}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, project.Root, "reevit_webhook.py"); !strings.Contains(got, test.needle) {
				t.Fatalf("generated webhook missing %q:\n%s", test.needle, got)
			}
		})
	}
}

func TestServerAdaptersExposeExactWebhookMountingInstructions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		project Project
		needle  string
	}{
		{Project{Stack: StackNode, Framework: FrameworkExpress}, "mountReevitWebhook(app)"},
		{Project{Stack: StackGo, Framework: FrameworkGeneric}, `http.HandleFunc("/webhooks/reevit"`},
		{Project{Stack: StackPython, Framework: FrameworkFastAPI}, "app.include_router(router)"},
		{Project{Stack: StackPython, Framework: FrameworkFlask}, "app.register_blueprint(reevit_webhooks)"},
		{Project{Stack: StackPython, Framework: FrameworkDjango}, "urlpatterns"},
		{Project{Stack: StackPHP, Framework: FrameworkLaravel}, "routes/reevit.php"},
		{Project{Stack: StackPHP, Framework: FrameworkGeneric}, "standalone"},
	}

	for _, test := range tests {
		if got := WebhookMountInstruction(test.project); !strings.Contains(got, test.needle) {
			t.Errorf("%s instruction = %q, want %q", test.project.Framework, got, test.needle)
		}
	}
}

func TestLaravelGetsFrameworkRouteInsteadOfStandalonePHPHandler(t *testing.T) {
	t.Parallel()

	project := Project{
		Root: t.TempDir(), Stack: StackPHP,
		Framework: FrameworkLaravel, Installer: InstallerComposer,
	}
	var webhook Target
	for _, target := range TargetsFor(project) {
		if target.Key == TargetWebhook {
			webhook = target
		}
	}
	if got := webhook.Files["laravel-webhook.php.tmpl"]; got != "routes/reevit.php" {
		t.Fatalf("Laravel webhook files = %#v", webhook.Files)
	}
}
