// Package cmd implements the reevit CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/telemetry"
	"github.com/Reevit-Platform/cli/internal/ui"
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

// Exit codes are part of the CLI's contract with CI:
//
//	0   success
//	1   runtime error
//	2   usage error (unknown flag, wrong number of arguments)
//	3   doctor found problems
//	130 cancelled (Ctrl-C)
const (
	exitUsage  = 2
	exitDoctor = 3
)

func ExitCode(err error) int {
	var exitErr ExitError
	if errors.As(err, &exitErr) && exitErr.Code > 0 {
		return exitErr.Code
	}
	return 1
}

// usageErr marks an error as the user having called the command wrongly, which
// exits 2 — distinguishable in a pipeline from a command that ran and failed.
func usageErr(err error) error {
	return ExitError{Code: exitUsage, Err: err}
}

// exactArgs is cobra.ExactArgs with the exit code a usage error deserves.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageErr(err)
		}

		return nil
	}
}

var rootCmd = &cobra.Command{
	Use:   "reevit",
	Short: "Reevit CLI — set up Reevit in your project, test payments, drive the sandbox simulator",
	Long: `Sets Reevit up in your project and gives you a real sandbox to test it
against — signed webhooks, simulated payment outcomes, and a check that says
whether any of it actually works.

Start here: reevit login → reevit init → reevit doctor.`,
	Example: `  reevit init                      # set up Reevit in the current project
  reevit listen --forward-to http://localhost:3000/api/webhooks/reevit
  reevit trigger payment.succeeded`,
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	// The first-run telemetry notice belongs before the run it is disclosing,
	// not after it: Report fires once the command has finished, so the
	// disclosure used to appear below the output of the very run it covered.
	//
	// No subcommand defines PreRun or PersistentPreRun, so nothing shadows
	// this. cobra only runs the closest PersistentPreRunE it finds.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Before anything else, and deliberately not behind the telemetry
		// guard below: a misspelled config key is worth saying on every
		// command, not only on the tracked ones.
		warnUnknownConfigKeys(cmd)

		if !telemetry.Tracked(topLevelName(cmd)) {
			return nil
		}

		// Do NOT gate this on --json (032). It looks tempting and it is a
		// silent-tracking hole: telemetry.Report mints and persists the
		// machine id on its own, so skipping the notice skips the
		// disclosure without skipping the collection. Worse, the notice is
		// keyed on the id being empty, so a user whose first-ever run
		// carried --json would be tracked from that run onwards and never
		// be told, on any later run either.
		//
		// There is also nothing to fix: --json is a promise about stdout,
		// and this writes to stderr. A --json consumer parses stdout and
		// never sees these four lines.
		sty := styleOf(cmd).err
		telemetry.EnsureNotice(cmd.ErrOrStderr(), sty.Note, sty.Dim)

		return nil
	},
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

	telemetry.Report(topLevelName(executed), Version, err == nil, time.Since(start))

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
	// cobra only pushes the root's context down to a subcommand that has none
	// yet (command.go:1113), so a subcommand run twice in one process keeps
	// the first run's context and never observes the second one's
	// cancellation. Push this run's context over the whole tree instead.
	applyContext(rootCmd, ctx)

	return rootCmd.ExecuteContextC(ctx)
}

func applyContext(cmd *cobra.Command, ctx context.Context) {
	cmd.SetContext(ctx)

	for _, sub := range cmd.Commands() {
		applyContext(sub, ctx)
	}
}

// renderError formats the error of a failed run the way the process itself
// prints it (main.go), including the trailing newline. It is a package-level
// variable so the golden tests capture exactly what a user sees on stderr.
//
// The plain Styler is deliberate: goldens compare bytes, and the coloured
// variants are covered by their own cases.
var renderError = func(err error) string {
	rendered := RenderError(err, ui.Styler{})
	if rendered == "" {
		return ""
	}

	return rendered + "\n"
}

// hinter is implemented by errors that can name the user's next step.
type hinter interface{ Hint() string }

// RenderError turns a failed run into the block the user reads: what went
// wrong and, when we know it, what to do about it.
//
// It returns "" for the errors that have already said their piece — a Ctrl-C,
// and doctor's own verdict, which the command printed itself.
func RenderError(err error, sty ui.Styler) string {
	if err == nil {
		return ""
	}

	// A cancelled run is the user's own Ctrl-C: exit 130, print nothing.
	if errors.Is(err, context.Canceled) {
		return ""
	}

	if errors.Is(err, errDoctorFailed) {
		return ""
	}

	// A cancelled wizard already printed its own message; it is not a failure
	// to explain.
	if ExitCode(err) == 130 {
		return err.Error()
	}

	// ExitError is a carrier for the exit code, not part of the message.
	cause := err

	var exitErr ExitError
	if errors.As(err, &exitErr) && exitErr.Err != nil {
		cause = exitErr.Err
	}

	return sty.Errorf(cause, hintFor(err))
}

// hintFor finds the one-line next step for an error: the error's own Hint if
// it has one, otherwise the network advice that every unreachable-API failure
// shares.
func hintFor(err error) string {
	var withHint hinter
	if errors.As(err, &withHint) {
		if hint := withHint.Hint(); hint != "" {
			return hint
		}
	}

	return networkHint(err)
}

// networkHint recognises "the request never got an answer" — a DNS failure, a
// refused connection, a timeout — and points at the two things the user
// controls.
func networkHint(err error) string {
	var (
		urlErr *url.Error
		opErr  *net.OpError
	)

	if !errors.As(err, &urlErr) && !errors.As(err, &opErr) &&
		!errors.Is(err, context.DeadlineExceeded) {
		return ""
	}

	target := ""
	if cfg, cfgErr := config.Load(); cfgErr == nil && cfg.BaseURL != "" {
		target = " " + cfg.BaseURL
	}

	return "could not reach" + target + " — check your connection or REEVIT_API_URL"
}

// warnUnknownConfigKeys tells the user about config-file keys the CLI ignored.
//
// An ignored key is indistinguishable from an absent one, which is how a
// mistyped base_url ends up silently pointing test traffic at the production
// API. This is a warning rather than an error on purpose: encoding/json's
// DisallowUnknownFields would turn a config written by a newer CLI into a hard
// failure for an older one, trading a silent misroute for a forward
// compatibility break. It survives --quiet for the same reason doctor's
// failures do — that flag trims narration, not findings.
//
// Errors are swallowed: if the file is unreadable or malformed the command's
// own RunE will say so properly, and a second, vaguer complaint here would
// only get in the way.
func warnUnknownConfigKeys(cmd *cobra.Command) {
	cfg, err := config.LoadFile()
	if err != nil || len(cfg.UnknownKeys) == 0 {
		return
	}

	sty := styleOf(cmd).err
	out := cmd.ErrOrStderr()

	fmt.Fprintln(out, sty.Warning(fmt.Sprintf(
		"ignoring %s in the config file: %s",
		plural(len(cfg.UnknownKeys), "unrecognised key"),
		strings.Join(cfg.UnknownKeys, ", "))))
	fmt.Fprintln(out, sty.Dim("  these are not settings — check the spelling, or remove them"))
}

// styles carries one Styler per stream. Their TTY-ness genuinely differs:
// `reevit payments list | less` has a piped stdout and a terminal stderr, and
// the table should lose its colour while the conversation keeps it.
type styles struct {
	out ui.Styler
	err ui.Styler
}

// styleOf resolves the stylers for one command run. It must not be called
// before cobra has parsed flags — --no-color is not readable until then —
// which is why every command calls it inside RunE rather than at construction.
func styleOf(cmd *cobra.Command) styles {
	noColor, _ := cmd.Flags().GetBool("no-color")
	env := os.Environ()

	return styles{
		out: ui.New(ui.Options{
			NoColorFlag: noColor, Env: env,
			IsTerminal: isTerminalWriter(cmd.OutOrStdout()), GOOS: runtime.GOOS,
		}),
		err: ui.New(ui.Options{
			NoColorFlag: noColor, Env: env,
			IsTerminal: isTerminalWriter(cmd.ErrOrStderr()), GOOS: runtime.GOOS,
		}),
	}
}

// StderrStyler builds the styler for the real process stderr. main.go needs it
// to render an error that happened before — or instead of — a command run, so
// the --no-color flag may not have been parsed; NO_COLOR and TERM still apply.
func StderrStyler() ui.Styler {
	return ui.New(ui.Options{
		Env:        os.Environ(),
		IsTerminal: isTerminalWriter(os.Stderr),
		GOOS:       runtime.GOOS,
	})
}

// marker extracts the bare glyph from a glyph method, for the few places that
// need the symbol inside a sentence or a line rather than in front of one.
func marker(rendered string) string { return strings.TrimSpace(rendered) }

// isTerminalWriter reports whether a writer is a character device — the same
// check cmd/init.go makes of stdin, and the reason this package needs no
// terminal dependency.
func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
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

func init() {
	rootCmd.PersistentFlags().Bool("no-color", false, "disable colour and glyphs")
	// Both are persistent so `reevit payments list --json` and
	// `reevit --json payments list` mean the same thing, and so a command
	// added later cannot forget to offer them.
	//
	// Not mutually exclusive: --json says what stdout carries, --quiet says
	// whether stderr says anything alongside it, and `--json --quiet` (pure
	// data, no commentary) is the combination CI actually wants.
	rootCmd.PersistentFlags().Bool("json", false, "print machine-readable JSON to stdout")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress progress and hints; errors still print")

	// Inherited by every subcommand: a mistyped flag is a usage error, and it
	// should say where to look. CommandPath is used whole — for the root it is
	// just "reevit", and slicing it would panic.
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageErr(fmt.Errorf("%w\n\nRun '%s --help' for usage", err, cmd.CommandPath()))
	})
}
