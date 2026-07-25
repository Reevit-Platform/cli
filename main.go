// reevit is the Reevit command-line tool: log in with a scoped API key,
// inspect payments, and drive the sandbox simulator to test integrations.
package main

import (
	"fmt"
	"os"

	"github.com/Reevit-Platform/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		code := cmd.ExitCode(err)
		if code == 130 {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(code)
	}
}
