package scaffold

import (
	"errors"
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
