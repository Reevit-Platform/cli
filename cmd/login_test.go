package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// resetLoginFlags restores the package-level flag state cobra mutates during a
// run. The values live in globals and pflag also remembers `Changed`, so a
// test that skips this leaks its arguments into every later test.
func resetLoginFlags(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		loginKey, loginManual, loginNoBrowser = "", false, false

		loginCmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})

		rootCmd.SetArgs(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)

		clearLoginCmdStreams()
	})
}

// clearLoginCmdStreams drops any writer set directly on loginCmd. Cobra
// resolves OutOrStdout on the command before falling back to its parent, so a
// sibling test that calls loginCmd.SetOut (login_browser_test.go does) would
// otherwise swallow everything a root-driven run writes.
func clearLoginCmdStreams() {
	loginCmd.SetIn(nil)
	loginCmd.SetOut(nil)
	loginCmd.SetErr(nil)
}

// keyProbeServer answers the post-login verification read so `login` reaches
// the save step without a real backend.
func keyProbeServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/payments" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	t.Cleanup(server.Close)

	return server
}

// runLogin drives the command through the root: cobra's ExecuteC always
// re-enters c.Root(), so SetArgs on a subcommand is discarded.
func runLogin(t *testing.T, stdin string, args ...string) (configPath string, stdout, stderr *bytes.Buffer, err error) {
	t.Helper()

	resetLoginFlags(t)
	clearLoginCmdStreams()

	server := keyProbeServer(t)

	configPath = filepath.Join(t.TempDir(), "config.json")
	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_MODE", "")

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}

	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)

	err = rootCmd.ExecuteContext(context.Background())

	return configPath, stdout, stderr, err
}

// A key piped without a trailing newline arrives as an EOF-terminated line;
// treating that EOF as a failure is what `printf '%s' "$KEY" | reevit login
// --key -` used to hit.
func TestLoginKeyFromStdinWithoutTrailingNewline(t *testing.T) {
	configPath, stdout, _, err := runLogin(t, "pfk_test_abc.sec", "login", "--key", "-")
	if err != nil {
		t.Fatalf("login --key -: %v", err)
	}

	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}

	if !strings.Contains(string(raw), `"api_key": "pfk_test_abc.sec"`) {
		t.Fatalf("config = %s, want the key read from stdin", raw)
	}

	if !strings.Contains(stdout.String(), "Saved to") {
		t.Fatalf("stdout = %q, want a save confirmation", stdout)
	}
}

// The prompt is interactive chrome, so it belongs on stderr — otherwise
// piping stdout to a file captures "API key: " alongside the real output.
func TestLoginKeyPromptGoesToStderr(t *testing.T) {
	_, stdout, stderr, err := runLogin(t, "pfk_test_abc.sec\n", "login", "--manual")
	if err != nil {
		t.Fatalf("login --manual: %v", err)
	}

	if !strings.Contains(stderr.String(), "API key: ") {
		t.Fatalf("stderr = %q, want the prompt", stderr)
	}

	if strings.Contains(stdout.String(), "API key: ") {
		t.Fatalf("stdout = %q, want no prompt on stdout", stdout)
	}
}
