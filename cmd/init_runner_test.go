package cmd

import (
	"reflect"
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func TestValidateInitGoal(t *testing.T) {
	t.Parallel()

	for _, goal := range []string{"auto", "full", "checkout", "webhook", "server"} {
		if err := validateInitGoal(goal); err != nil {
			t.Errorf("validateInitGoal(%q): %v", goal, err)
		}
	}
	if err := validateInitGoal("everything"); err == nil {
		t.Fatal("invalid goal accepted")
	}
}

func TestApplyPathOverridesPreservesRunnableDemoFiles(t *testing.T) {
	previous := initCheckoutPath
	initCheckoutPath = "custom/checkout.tsx"
	t.Cleanup(func() { initCheckoutPath = previous })

	targets := []scaffold.Target{{
		Key: scaffold.TargetCheckout,
		Files: map[string]string{
			"next-checkout.tsx.tmpl":      "components/checkout.tsx",
			"next-app-demo-page.tsx.tmpl": "app/reevit-demo/page.tsx",
		},
	}}
	applyPathOverrides(targets)

	want := map[string]string{
		"next-checkout.tsx.tmpl":      "custom/checkout.tsx",
		"next-app-demo-page.tsx.tmpl": "app/reevit-demo/page.tsx",
	}
	if !reflect.DeepEqual(targets[0].Files, want) {
		t.Fatalf("overridden files = %#v, want %#v", targets[0].Files, want)
	}
}
