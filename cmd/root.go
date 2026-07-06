// Package cmd implements the reevit CLI commands.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

// Version is stamped by goreleaser at build time (-X …/cmd.Version=v1.2.3).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "reevit",
	Short:         "Reevit CLI — test payments, drive the sandbox simulator, inspect your account",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

// client loads config and returns an authenticated API client, failing with a
// helpful message when no key is configured.
func client() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key configured — run `reevit login` or set REEVIT_API_KEY")
	}

	return api.New(cfg), nil
}
