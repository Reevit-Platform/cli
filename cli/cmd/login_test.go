package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadKeyFromStdin(t *testing.T) {
	t.Run("reads a newline-terminated key", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader("pfk_live_abc123.secret\n"))

		key, err := readKeyFromStdin(cmd)
		if err != nil {
			t.Fatalf("readKeyFromStdin: %v", err)
		}

		if key != "pfk_live_abc123.secret" {
			t.Fatalf("key = %q", key)
		}
	})

	t.Run("reads a key at EOF without a trailing newline", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader("pfk_test_xyz.parts"))

		key, err := readKeyFromStdin(cmd)
		if err != nil {
			t.Fatalf("readKeyFromStdin: %v", err)
		}

		if key != "pfk_test_xyz.parts" {
			t.Fatalf("key = %q", key)
		}
	})

	t.Run("empty stdin yields an empty key (caller rejects)", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(""))

		key, err := readKeyFromStdin(cmd)
		if err != nil {
			t.Fatalf("readKeyFromStdin: %v", err)
		}

		if key != "" {
			t.Fatalf("key = %q, want empty", key)
		}
	})
}
