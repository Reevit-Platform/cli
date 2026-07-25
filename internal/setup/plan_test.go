package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func TestResolveRecommendedSetupByStack(t *testing.T) {
	t.Parallel()

	projects := []scaffold.Project{
		{Root: t.TempDir(), Stack: scaffold.StackNext, Framework: scaffold.FrameworkNext, Manager: scaffold.PMPnpm},
		{Root: t.TempDir(), Stack: scaffold.StackReact, Framework: scaffold.FrameworkReact, Manager: scaffold.PMNpm},
		{Root: t.TempDir(), Stack: scaffold.StackVue, Framework: scaffold.FrameworkNuxt, Manager: scaffold.PMYarn},
		{Root: t.TempDir(), Stack: scaffold.StackSvelte, Framework: scaffold.FrameworkSvelteKit, Manager: scaffold.PMBun},
		{Root: t.TempDir(), Stack: scaffold.StackNode, Framework: scaffold.FrameworkExpress, Manager: scaffold.PMNpm},
		{Root: t.TempDir(), Stack: scaffold.StackGo, Framework: scaffold.FrameworkGeneric, Installer: scaffold.InstallerGo},
		{Root: t.TempDir(), Stack: scaffold.StackPython, Framework: scaffold.FrameworkFastAPI, Installer: scaffold.InstallerPip},
		{Root: t.TempDir(), Stack: scaffold.StackPHP, Framework: scaffold.FrameworkLaravel, Installer: scaffold.InstallerComposer},
	}

	for _, project := range projects {
		plan, err := Resolve(ResolveInput{
			Project: project, Goal: GoalAuto,
			Targets: RecommendedTargets(project), LocalOrigin: "http://localhost:3000",
		})
		if err != nil {
			t.Fatalf("%s: %v", project.Framework, err)
		}
		if len(plan.Operations) == 0 {
			t.Fatalf("%s: empty plan", project.Framework)
		}
		for _, operation := range plan.Operations {
			if operation.Reason == "" {
				t.Fatalf("%s: operation has no reason: %#v", project.Framework, operation)
			}
			if strings.Contains(operation.Detail, ".secret") || strings.Contains(operation.Detail, "whsec_") {
				t.Fatalf("%s: plan leaked a secret: %#v", project.Framework, operation)
			}
		}
	}
}

func writePlanFile(t *testing.T, root, relative string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCollectsAllConflicts(t *testing.T) {
	t.Parallel()

	project := scaffold.Project{
		Root: t.TempDir(), Stack: scaffold.StackNext,
		Framework: scaffold.FrameworkNext, Manager: scaffold.PMNpm,
	}
	targets := RecommendedTargets(project)
	for _, target := range targets {
		for _, path := range target.Files {
			writePlanFile(t, project.Root, path)
		}
	}
	plan, err := Resolve(ResolveInput{
		Project: project, Goal: GoalAuto, Targets: targets,
		LocalOrigin: "http://localhost:3000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) < 4 {
		t.Fatalf("conflicts = %v", plan.Conflicts)
	}
}
