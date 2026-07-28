package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
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
		}

		// Default path: browser pairing. Manual key entry stays available for
		// live keys, CI, and air-gapped setups.
		if key == "" && !loginManual {
			return browserLogin(cmd, !loginNoBrowser)
		}

		if key == "" {
			fmt.Fprint(cmd.OutOrStdout(), "API key: ")

			var err error

			key, err = readKeyFromStdin(cmd)
			if err != nil {
				return fmt.Errorf("read key: %w", err)
			}
		}

		if key == "" {
			return fmt.Errorf("an API key is required")
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		cfg.APIKey = key

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

		p, err := config.Save(cfg)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Saved to %s (%s mode)\n", p, cfg.Mode)

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

	if v := os.Getenv("REEVIT_API_KEY"); v != "" && loginKey == "" {
		loginKey = v
	}

	rootCmd.AddCommand(loginCmd)
}
