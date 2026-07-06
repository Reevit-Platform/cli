package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

var loginKey string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Store a scoped Reevit API key for the CLI",
	Long: `Stores an API key in your user config (owner-only permissions).
Create a key in the dashboard under Developers → API keys — give it only the
scopes you want the CLI to have. Test-mode keys are strongly recommended.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		key := strings.TrimSpace(loginKey)

		if key == "" {
			fmt.Fprint(cmd.OutOrStdout(), "API key: ")

			reader := bufio.NewReader(cmd.InOrStdin())

			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read key: %w", err)
			}

			key = strings.TrimSpace(line)
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

func isScopeError(err error) bool {
	apiErr, ok := err.(*api.APIError)

	return ok && apiErr.Status == 403
}

func init() {
	loginCmd.Flags().StringVar(&loginKey, "key", "", "API key (omit to be prompted)")

	if v := os.Getenv("REEVIT_API_KEY"); v != "" && loginKey == "" {
		loginKey = v
	}

	rootCmd.AddCommand(loginCmd)
}
