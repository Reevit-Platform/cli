package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// ExecuteWith, not rootCmd.ExecuteContext: cobra only fills a
	// subcommand's context when it is still nil, so a command that has
	// already run once in this process keeps the context of that first run.
	// TestExecuteWithCancelledContextExits130 runs one with a cancelled
	// context, and under -shuffle=on that can land first — after which every
	// login here failed with "context canceled". ExecuteWith is what pushes
	// this run's context over the whole tree.
	err = ExecuteWith(context.Background(), args, strings.NewReader(stdin), stdout, stderr)

	return configPath, stdout, stderr, err
}

// A key piped without a trailing newline arrives as an EOF-terminated line;
// treating that EOF as a failure is what `printf '%s' "$KEY" | reevit login
// --key -` used to hit.
func TestLoginKeyFromStdinWithoutTrailingNewline(t *testing.T) {
	configPath, _, stderr, err := runLogin(t, "pfk_test_abc.sec", "login", "--key", "-")
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

	// The confirmation is conversation, so it lands on stderr. It has to say
	// both that the key was accepted and which mode it turned out to be:
	// "saved" alone leaves the user guessing whether they pasted a live key.
	if !strings.Contains(stderr.String(), "Key accepted (test mode)") {
		t.Fatalf("stderr = %q, want the mode-carrying acceptance line", stderr)
	}

	if !strings.Contains(stderr.String(), "saved to ") {
		t.Fatalf("stderr = %q, want the config path", stderr)
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

// REEVIT_API_URL is a one-off override. Persisting it made every later run
// talk to whatever host happened to be exported during login.
func TestLoginDoesNotPersistTheEnvironmentBaseURL(t *testing.T) {
	configPath, _, _, err := runLogin(t, "", "login", "--key", "pfk_test_abc.sec")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}

	if strings.Contains(string(raw), "base_url") {
		t.Fatalf("config = %s, want no base_url — it came from REEVIT_API_URL", raw)
	}
}

// A live key must be recorded and reported as live: `(test mode)` on a
// pfk_live key is how people ship test wiring against real money.
func TestLoginRecordsLiveModeForALiveKey(t *testing.T) {
	configPath, _, stderr, err := runLogin(t, "", "login", "--key", "pfk_live_abc.sec")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}

	if !strings.Contains(string(raw), `"mode": "live"`) {
		t.Fatalf("config = %s, want live mode", raw)
	}

	if !strings.Contains(stderr.String(), "(live mode)") {
		t.Fatalf("stderr = %q, want the live label", stderr)
	}
}

// Login must not preserve fields it does not establish.
func TestLoginPreservesUnrelatedConfigFields(t *testing.T) {
	configPath, _, _, err := runLogin(t, "", "login", "--key", "pfk_test_first.sec")
	if err != nil {
		t.Fatalf("first login: %v", err)
	}

	seeded := `{"api_key":"pfk_test_first.sec","mode":"test","org_id":"org_1","org_name":"Acme","telemetry_id":"tid"}`
	if writeErr := os.WriteFile(configPath, []byte(seeded), 0o600); writeErr != nil {
		t.Fatalf("seed config: %v", writeErr)
	}

	// runLogin allocates a fresh config path, so point the second run at this
	// one explicitly.
	resetLoginFlags(t)
	clearLoginCmdStreams()

	server := keyProbeServer(t)

	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_MODE", "")

	execErr := ExecuteWith(
		context.Background(),
		[]string{"login", "--key", "pfk_test_second.sec"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	if execErr != nil {
		t.Fatalf("second login: %v", execErr)
	}

	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}

	for _, want := range []string{`"org_id": "org_1"`, `"org_name": "Acme"`, `"telemetry_id": "tid"`, `"api_key": "pfk_test_second.sec"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("config = %s, want %s", raw, want)
		}
	}
}

// An exported REEVIT_API_KEY is an override for one invocation. Reading it in
// init() turned `reevit login` into "write my shell's key to disk".
func TestLoginIgnoresAnExportedAPIKey(t *testing.T) {
	resetLoginFlags(t)
	clearLoginCmdStreams()

	var (
		mu      sync.Mutex
		started bool
	)

	server := pairingServer(t, []string{"approved"})
	defer server.Close()

	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth" {
			mu.Lock()
			started = true
			mu.Unlock()
		}

		proxy, err := http.NewRequest(r.Method, server.URL+r.URL.Path, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		proxy.Header = r.Header.Clone()

		resp, err := server.Client().Do(proxy)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)

			return
		}
		defer func() { _ = resp.Body.Close() }()

		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer wrapped.Close()

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_URL", wrapped.URL)
	t.Setenv("REEVIT_API_KEY", "pfk_test_env.sec")
	t.Setenv("REEVIT_MODE", "")

	err := ExecuteWith(
		context.Background(),
		[]string{"login", "--no-browser"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("login --no-browser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if !started {
		t.Fatal("login did not start the browser pairing flow — it took REEVIT_API_KEY instead")
	}
}

// The config path is the longest thing either login flow prints
// ("/Users/x/Library/Application Support/reevit/config.json" on macOS), and
// it is the one part of the line the user does not need to read in full. It
// must shorten to "~" only on a real path-component boundary: a sibling
// directory such as /home/alice-backup shares the "/home/alice" prefix and
// must survive intact.
func TestShortenHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"under home", filepath.Join(home, "reevit", "config.json"), "~/reevit/config.json"},
		{"home itself", home, "~"},
		{"outside home", "/etc/reevit/config.json", "/etc/reevit/config.json"},
		{"sibling sharing the prefix", home + "-backup/config.json", home + "-backup/config.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortenHome(tc.in); got != tc.want {
				t.Fatalf("shortenHome(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
