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

		code := cmd.ExitCode(err)
		if code == 130 {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(code)
	}
}
