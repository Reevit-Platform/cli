package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightReportsAllUnmanagedConflicts(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext, TypeScript: true}
	targets := TargetsFor(project)
	write(t, project.Root, "app/api/webhooks/reevit/route.ts", "user code")
	write(t, project.Root, "components/reevit-checkout-button.tsx", "user code")

	err := Preflight(project, targets, Manifest{})
	var conflicts *ConflictError
	if !errors.As(err, &conflicts) {
		t.Fatalf("Preflight() error = %v, want ConflictError", err)
	}
	message := err.Error()
	if !strings.Contains(message, "app/api/webhooks/reevit/route.ts") ||
		!strings.Contains(message, "components/reevit-checkout-button.tsx") {
		t.Fatalf("error does not contain every conflict: %s", message)
	}
}

func TestPreflightAllowsExistingOutputsAfterExplicitResolution(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext, TypeScript: true}
	targets := TargetsFor(project)
	write(t, project.Root, "components/reevit-checkout-button.tsx", "user code")

	err := PreflightWithOptions(
		project,
		targets,
		Manifest{},
		PreflightOptions{ExistingFiles: ExistingFilesKeep},
	)
	if err != nil {
		t.Fatalf("PreflightWithOptions() error = %v", err)
	}
}

func TestPreflightRejectsOutputOutsideProject(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext, TypeScript: true}
	targets := []Target{{
		Key: TargetCheckout,
		Files: map[string]string{
			"next-checkout.tsx.tmpl": "../outside.tsx",
		},
	}}

	err := Preflight(project, targets, Manifest{})
	if err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Fatalf("Preflight() error = %v", err)
	}
}

func TestPreflightRejectsOutputThroughSymlink(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext, TypeScript: true}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project.Root, "components")); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{
		Key: TargetCheckout,
		Files: map[string]string{
			"next-checkout.tsx.tmpl": "components/checkout.tsx",
		},
	}}

	err := Preflight(project, targets, Manifest{})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Preflight() error = %v", err)
	}
}
