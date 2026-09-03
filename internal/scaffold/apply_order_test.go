package scaffold

import (
	"fmt"
	"testing"
)

// Apply returns the FileResult list that `reevit init` prints back as the
// "created" summary. Target.Files is a map, so building that list by ranging
// it directly reordered the summary on every run — two identical runs of init
// produced two differently ordered receipts, which makes them impossible to
// diff or paste into a bug report. Apply writes real files, so each run needs
// a fresh root; the assertion is on the order of the reported paths.
func TestApplyReportsFilesInTheSameOrderOnEveryRun(t *testing.T) {
	projects := []Project{
		{Stack: StackNext, TypeScript: true},
		{Stack: StackReact, TypeScript: true},
		{Stack: StackVue, TypeScript: true},
		{Stack: StackSvelte, TypeScript: true},
	}
	for _, base := range projects {
		t.Run(string(base.Stack), func(t *testing.T) {
			var first []string
			for i := 0; i < 60; i++ {
				project := base
				project.Root = t.TempDir()
				targets := TargetsFor(project)

				multi := false
				for _, target := range targets {
					if len(target.Files) > 1 {
						multi = true
					}
				}
				if !multi {
					t.Fatalf("%s has no multi-file target, so this cannot detect a reordering", base.Stack)
				}

				results, err := Apply(project, targets, ApplyOptions{})
				if err != nil {
					t.Fatalf("apply run %d: %v", i, err)
				}
				var paths []string
				for _, r := range results {
					paths = append(paths, r.Path)
				}
				if first == nil {
					first = paths
					continue
				}
				if fmt.Sprint(paths) != fmt.Sprint(first) {
					t.Fatalf("run %d reported files in a different order\nfirst=%v\ngot  =%v",
						i, first, paths)
				}
			}
		})
	}
}
