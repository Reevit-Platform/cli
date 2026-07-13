// Package cmd implements the reevit CLI commands.
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/telemetry"
)

// Version is stamped by goreleaser at build time (-X …/cmd.Version=v1.2.3).
var Version = "dev"

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
func Execute() error {
	start := time.Now()

	executed, err := rootCmd.ExecuteC()

	telemetry.Report(topLevelName(executed), Version, err == nil, time.Since(start), os.Stderr)

	return err
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
