package setup

import (
	"fmt"
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

// stacksUnderTest covers every stack TargetsFor can return targets for. Each
// entry is a project whose targets include at least one multi-file target,
// which is where map iteration order becomes observable.
func stacksUnderTest(root string) []scaffold.Project {
	return []scaffold.Project{
		{Stack: scaffold.StackNext, TypeScript: true, Root: root},
		{Stack: scaffold.StackNext, TypeScript: true, Root: root, NextRouter: scaffold.NextRouterPages},
		{Stack: scaffold.StackReact, TypeScript: true, Root: root},
		{Stack: scaffold.StackVue, TypeScript: true, Root: root},
		{Stack: scaffold.StackVue, TypeScript: true, Root: root, Framework: scaffold.FrameworkNuxt},
		{Stack: scaffold.StackSvelte, TypeScript: true, Root: root},
		{Stack: scaffold.StackSvelte, TypeScript: true, Root: root, Framework: scaffold.FrameworkSvelteKit},
		{Stack: scaffold.StackNode, TypeScript: true, Root: root},
		{Stack: scaffold.StackGo, Root: root},
	}
}

// A target's Files is a map, so any loop that ranges it directly hands the
// user a different order on every run. `reevit init` prints the plan it is
// about to apply; a plan that reshuffles itself between two identical runs is
// unreviewable. This drives Resolve, so it fails for any ordering-sensitive
// map range anywhere beneath it, not only the ones known today.
func TestResolveOrdersItsOperationsIdenticallyOnEveryRun(t *testing.T) {
	root := t.TempDir()
	for _, project := range stacksUnderTest(root) {
		name := fmt.Sprintf("%s/%s/%s", project.Stack, project.Framework, project.NextRouter)
		t.Run(name, func(t *testing.T) {
			targets := scaffold.TargetsFor(project)
			if len(targets) == 0 {
				t.Fatalf("no targets for %s", name)
			}
			input := ResolveInput{
				Project: project, Goal: GoalFull, Targets: targets,
				LocalOrigin: "http://localhost:3000",
			}
			first, err := Resolve(input)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			// 200 runs: Go randomises map iteration per range statement, so a
			// single repeat would pass by luck on a two-entry map.
			for i := 1; i < 200; i++ {
				got, err := Resolve(input)
				if err != nil {
					t.Fatalf("resolve run %d: %v", i, err)
				}
				if len(got.Operations) != len(first.Operations) {
					t.Fatalf("run %d has %d operations, run 0 had %d",
						i, len(got.Operations), len(first.Operations))
				}
				for j := range got.Operations {
					if got.Operations[j] != first.Operations[j] {
						t.Fatalf("run %d operation %d is %+v, run 0 had %+v",
							i, j, got.Operations[j], first.Operations[j])
					}
				}
			}
		})
	}
}
