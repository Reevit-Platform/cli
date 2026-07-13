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
