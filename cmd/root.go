// Package cmd implements the reevit CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/telemetry"
)

// Version is stamped by goreleaser at build time (-X …/cmd.Version=v1.2.3).
var Version = "dev"

// ExitError lets commands request a conventional process exit code while
// preserving an inspectable cause for tests and embedders.
type ExitError struct {
	Code int
	Err  error
}

func (e ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e ExitError) Unwrap() error { return e.Err }

func ExitCode(err error) int {
	var exitErr ExitError
	if errors.As(err, &exitErr) && exitErr.Code > 0 {
		return exitErr.Code
	}
	return 1
}

var rootCmd = &cobra.Command{
	Use:           "reevit",
	Short:         "Reevit CLI — set up Reevit in your project, test payments, drive the sandbox simulator",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI and reports one anonymous usage event per tracked
// command (see internal/telemetry — opt out with REEVIT_TELEMETRY=0 or
// DO_NOT_TRACK=1).
//
// Interrupts and SIGTERM cancel the command's context, so long-running
// commands (listen, doctor --e2e, the login poll) unwind and report exit 130
// instead of relying on Go's default "kill the process" signal handling.
func Execute() error {
	start := time.Now()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	executed, err := executeWith(
		ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr,
	)

	telemetry.Report(topLevelName(executed), Version, err == nil, time.Since(start), os.Stderr)

	return err
}

// ExecuteWith runs the CLI against explicit stdio. Tests use it to capture
// stdout and stderr separately; Execute wires it to the real process.
//
// cobra resolves every print site's writer through OutOrStdout/ErrOrStderr,
// which falls back to the parent command, so setting the streams on the root
// is enough for the whole tree — provided no subcommand has a writer of its
// own left over from an earlier test (see resetFlags in golden_test.go).
func ExecuteWith(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	_, err := executeWith(ctx, args, in, out, errOut)

	return err
}

// executeWith also returns the command that actually ran, which Execute needs
// for its telemetry event.
func executeWith(
	ctx context.Context, args []string, in io.Reader, out, errOut io.Writer,
) (*cobra.Command, error) {
	rootCmd.SetArgs(args)
	rootCmd.SetIn(in)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)

	return rootCmd.ExecuteContextC(ctx)
}

// renderError formats the error of a failed run the way the process itself
// prints it (main.go), including the trailing newline. It is a package-level
// variable so the golden tests capture exactly what a user sees on stderr and
// a later restyle can replace the presentation in one place.
var renderError = func(err error) string {
	if err == nil {
		return ""
	}

	// A cancelled run is the user's own Ctrl-C: exit 130, print nothing.
	if errors.Is(err, context.Canceled) {
		return ""
	}

	if ExitCode(err) == 130 {
		return err.Error() + "\n"
	}

	return "error: " + err.Error() + "\n"
}

// topLevelName resolves the first-level subcommand a run belongs to, so
// `reevit payments list` reports as "payments".
func topLevelName(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}

	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}

	return cmd.Name()
}

// client loads config and returns an authenticated API client, failing with a
// helpful message when no key is configured.
func client() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key configured — run `reevit login` (opens your browser) or set REEVIT_API_KEY")
	}

	return api.New(cfg), nil
}
