package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandRunnerHidesSuccessAndPersistsFailureLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out bytes.Buffer
	runner := CommandRunner{Output: &out}
	if _, err := runner.Run(context.Background(), dir, []string{"sh", "-c", "echo noisy-success"}, false); err != nil {
		t.Fatalf("success command: %v", err)
	}
	if strings.Contains(out.String(), "noisy-success") {
		t.Fatalf("successful installer output leaked: %q", out.String())
	}

	logPath, err := runner.Run(
		context.Background(), dir,
		[]string{"sh", "-c", "echo useful-failure >&2; exit 7"}, false,
	)
	if err == nil {
		t.Fatal("failure command returned nil error")
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, logPath))
	if readErr != nil {
		t.Fatalf("read failure log: %v", readErr)
	}
	if !strings.Contains(string(raw), "useful-failure") {
		t.Fatalf("failure log = %q", raw)
	}
}

func TestCommandRunnerKeepsSuccessfulLogWhenRequested(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out bytes.Buffer
	runner := CommandRunner{Output: &out, KeepLogs: true}
	logPath, err := runner.Run(
		context.Background(), dir,
		[]string{"sh", "-c", "echo retained-success"}, false,
	)
	if err != nil {
		t.Fatalf("success command: %v", err)
	}
	if logPath == "" {
		t.Fatal("successful installer did not return a retained log path")
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, logPath))
	if readErr != nil {
		t.Fatalf("read retained log: %v", readErr)
	}
	if !strings.Contains(string(raw), "retained-success") {
		t.Fatalf("retained log = %q", raw)
	}
}

func TestCryptoSecretGeneratorProducesUniqueSecrets(t *testing.T) {
	t.Parallel()

	generator := CryptoSecretGenerator{}
	first, err := generator.WebhookSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.WebhookSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "whsec_") || len(first) < 40 || first == second {
		t.Fatal("generated secrets are not usable and unique")
	}
}
