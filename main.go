// reevit is the Reevit command-line tool: log in with a scoped API key,
// inspect payments, and drive the sandbox simulator to test integrations.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Reevit-Platform/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		// Ctrl-C: the shell prints its own ^C, we add nothing.
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}

		// Some errors have already had their say on screen — a cancelled
		// wizard, doctor's own verdict — and render as nothing.
		if rendered := cmd.RenderError(err, cmd.StderrStyler()); rendered != "" {
			fmt.Fprintln(os.Stderr, rendered)
		}

		os.Exit(cmd.ExitCode(err))
	}
}
