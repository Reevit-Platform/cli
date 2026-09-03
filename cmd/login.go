package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

var (
	loginKey       string
	loginManual    bool
	loginNoBrowser bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in via your browser, or store an API key with --key",
	Long: `Logs the CLI in. By default this opens the Reevit dashboard in your
browser: you confirm a pairing code there and the CLI receives a freshly
minted TEST-MODE API key scoped to what the CLI needs — no copy-pasting.

To use an existing key instead (e.g. a live key), pass --key or --manual.
Keys are stored in your user config with owner-only permissions.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		key := strings.TrimSpace(loginKey)

		// `--key -` reads the key from stdin, keeping it out of shell history
		// and process listings — the recommended way to pass live keys.
		if key == "-" {
			fmt.Fprint(cmd.ErrOrStderr(), "API key: ")

			var err error

			key, err = readKeyFromStdin(cmd)
			if err != nil {
				return fmt.Errorf("read key from stdin: %w", err)
			}
		} else if key == "" && !loginManual {
			// Default path: browser pairing. Manual key entry stays available
			// for live keys, CI, and air-gapped setups.
			return browserLogin(cmd, !loginNoBrowser)
		} else if key == "" {
			// The prompt is interactive chrome, not output: keep it on stderr
			// so `reevit login --manual` stays pipe-friendly.
			fmt.Fprint(cmd.ErrOrStderr(), "API key: ")

			var err error

			key, err = readKeyFromStdin(cmd)
			if err != nil {
				return fmt.Errorf("read key from stdin: %w", err)
			}
		}

		if key == "" {
			return fmt.Errorf("an API key is required")
		}

		// The verification probe needs the env overlay — REEVIT_API_URL may
		// point at a local or staging backend for this one invocation.
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		cfg.APIKey = key

		// The key decides the mode, not the ambient REEVIT_MODE that Load
		// overlaid: `login --key pfk_live_…` used to save and print "test".
		mode, keyed := config.ModeFromKey(key)
		if keyed {
			cfg.Mode = mode
		}

		// Verify before saving — a cheap read that any scope can perform is
		// not guaranteed, so tolerate 403 (valid key, narrow scopes) and only
		// reject definite auth failures.
		// GET /payments returns an array; decode into any so the probe only
		// cares about auth, not shape.
		var probe any

		err = api.New(cfg).Do(cmd.Context(), api.Request{Path: "/payments", Query: nil}, &probe)
		if apiErr, ok := err.(*api.APIError); ok && (apiErr.Status == 401) {
			return fmt.Errorf("that key was rejected by the API: %w", err)
		} else if err != nil && !isScopeError(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not verify key (%v) — saving anyway\n", err)
		}

		// Persist from the file, never from Load: a one-off REEVIT_API_URL or
		// REEVIT_MODE would otherwise be written to disk and outlive the shell
		// that set it. Only the fields login actually establishes are touched;
		// org_id, org_name and telemetry_id survive untouched.
		saved, err := config.LoadFile()
		if err != nil {
			return err
		}

		saved.APIKey = cfg.APIKey
		saved.Mode = cfg.Mode

		p, err := config.Save(saved)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Saved to %s (%s mode)\n", p, saved.Mode)

		return nil
	},
}

// readKeyFromStdin reads one line (the API key) from the command's stdin.
// A trailing newline is optional (piped input may end at EOF).
func readKeyFromStdin(cmd *cobra.Command) (string, error) {
	reader := bufio.NewReader(cmd.InOrStdin())

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

func isScopeError(err error) bool {
	apiErr, ok := err.(*api.APIError)

	return ok && apiErr.Status == 403
}

func init() {
	loginCmd.Flags().StringVar(&loginKey, "key", "", "API key (use '-' to read from stdin; skips the browser flow)")
	loginCmd.Flags().BoolVar(&loginManual, "manual", false, "prompt for an API key instead of using the browser")
	loginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false, "print the pairing link instead of opening a browser")

	// No REEVIT_API_KEY fallback here: an exported key is an override for a
	// single invocation, and reading it in init() turned `reevit login` into
	// "write my shell's key to disk" without ever saying so. To persist a key,
	// pass it: `reevit login --key -`.

	rootCmd.AddCommand(loginCmd)
}
