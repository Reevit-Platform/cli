package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// find walks the command tree the way a user does: by typing the words.
func find(t *testing.T, path string) *cobra.Command {
	t.Helper()

	cmd := rootCmd
	if path == "" {
		return cmd
	}

	for _, word := range strings.Fields(path) {
		var next *cobra.Command
		for _, child := range cmd.Commands() {
			if child.Name() == word {
				next = child
				break
			}
		}

		if next == nil {
			t.Fatalf("no command %q under %q", word, cmd.CommandPath())
		}

		cmd = next
	}

	return cmd
}

// Every command a person actually runs shows a worked invocation. The parent
// `payments` is deliberately absent: it has no RunE, so an example would be a
// command you cannot type.
func TestEveryRunnableCommandShowsAnExample(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", "login", "init", "doctor", "listen", "trigger", "payments list"} {
		cmd := find(t, path)
		if strings.TrimSpace(cmd.Example) == "" {
			t.Errorf("%q has no Example", cmd.CommandPath())
		}
	}

	if example := find(t, "payments").Example; example != "" {
		t.Errorf("the payments group is not runnable but carries an example:\n%s", example)
	}
}

// An example that names a flag the binary does not have is worse than no
// example: it fails in the user's terminal, with our words in their history.
// Flags are resolved against the command the line actually invokes, which is
// not always the command the example is attached to — the root example runs
// `reevit listen`.
func TestExamplesOnlyUseFlagsTheCommandActuallyHas(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", "login", "init", "doctor", "listen", "trigger", "payments list"} {
		owner := find(t, path)

		for _, line := range strings.Split(owner.Example, "\n") {
			line, _, _ = strings.Cut(line, "#")
			if line = strings.TrimSpace(line); line == "" {
				continue
			}

			words := strings.Fields(line)
			if words[0] != "reevit" {
				t.Errorf("%s: example line does not invoke reevit: %q", owner.CommandPath(), line)
				continue
			}

			invoked, _, err := rootCmd.Find(words[1:])
			if err != nil {
				t.Errorf("%s: %q resolves to no command: %v", owner.CommandPath(), line, err)
				continue
			}

			for _, word := range words[1:] {
				if !strings.HasPrefix(word, "--") {
					continue
				}

				name := strings.TrimPrefix(strings.SplitN(word, "=", 2)[0], "--")
				if invoked.Flags().Lookup(name) == nil && invoked.InheritedFlags().Lookup(name) == nil {
					t.Errorf("%s documents --%s, which %s does not accept:\n  %s",
						owner.CommandPath(), name, invoked.CommandPath(), line)
				}
			}
		}
	}
}

// The root help is the only page a lost user is guaranteed to see, so it has
// to name the three commands that get them from nothing to working.
func TestRootHelpNamesTheHappyPathInOrder(t *testing.T) {
	t.Parallel()

	long := rootCmd.Long
	if long == "" {
		t.Fatal("rootCmd has no Long")
	}

	previous := -1
	for _, step := range []string{"reevit login", "reevit init", "reevit doctor"} {
		at := strings.Index(long, step)
		if at < 0 {
			t.Fatalf("root help never mentions %q:\n%s", step, long)
		}

		if at <= previous {
			t.Fatalf("root help puts %q out of order:\n%s", step, long)
		}

		previous = at
	}
}
