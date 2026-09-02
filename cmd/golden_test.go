package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// update rewrites the golden files instead of comparing against them:
//
//	go test ./cmd -run TestGolden -update
//
// The resulting diff is the review artefact for any output change — read it,
// never regenerate blindly.
var update = flag.Bool("update", false, "rewrite golden files")

// goldenCase is one full CLI invocation captured as two files:
// testdata/golden/<name>.stdout and <name>.stderr.
type goldenCase struct {
	name string
	args []string
	// stdin is fed to the command as a non-TTY reader, which is also what
	// makes the interactive guards fire (cmd/init.go:87).
	stdin string
	env   map[string]string
	// unsetEnv removes variables for the duration of the case. t.Setenv can
	// only set, and cases that snapshot the coloured or telemetry-notice
	// variants need NO_COLOR / REEVIT_TELEMETRY genuinely absent.
	unsetEnv []string
	// dir makes the case run from a project fixture; doctor and init resolve
	// the project from os.Getwd().
	dir func(t *testing.T) string
	// server stands in for the Reevit API; its URL becomes REEVIT_API_URL.
	server   func(t *testing.T) *httptest.Server
	wantExit int
}

const goldenDir = "testdata/golden"

// goldenRoot is the absolute path to goldenDir, captured before any case can
// os.Chdir into a project fixture — a relative path would write the files
// into the fixture's temp directory instead.
var goldenRoot = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic("golden tests: resolve working directory: " + err.Error())
	}

	return filepath.Join(wd, goldenDir)
}()

func TestGolden(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			runGolden(t, c)
		})
	}
}

// runGolden runs one case and compares both streams and the exit code.
//
// It must never call t.Parallel: cases change the process working directory
// and process environment.
func runGolden(t *testing.T, c goldenCase) {
	t.Helper()

	// Reset before as well as after: another test in this package may have
	// left a flag set or a writer bound to a subcommand.
	resetFlags()
	t.Cleanup(resetFlags)

	configPath := filepath.Join(t.TempDir(), "config.json")
	home := t.TempDir()

	t.Setenv("REEVIT_CONFIG", configPath)
	t.Setenv("REEVIT_TELEMETRY", "0")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	t.Setenv("HOME", home)
	// The payments table renders created_at in the local zone, and doctor
	// turns strict when CI is set — pin both.
	t.Setenv("TZ", "UTC")
	t.Setenv("CI", "")
	t.Setenv("REEVIT_API_KEY", "")
	t.Setenv("REEVIT_API_URL", "")
	t.Setenv("REEVIT_MODE", "")

	apiURL := ""

	if c.server != nil {
		srv := c.server(t)
		t.Cleanup(srv.Close)

		apiURL = srv.URL

		t.Setenv("REEVIT_API_URL", apiURL)
	}

	for k, v := range c.env {
		t.Setenv(k, v)
	}

	// Applied last so "unset" really means unset, whatever the baseline did.
	for _, name := range c.unsetEnv {
		previous, had := os.LookupEnv(name)

		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}

		if had {
			t.Cleanup(func() { _ = os.Setenv(name, previous) })
		}
	}

	// rootCmd.Version is copied from Version at package init, so both have to
	// move for `--version` to print the test value.
	previousVersion, previousRootVersion := Version, rootCmd.Version
	Version, rootCmd.Version = "v0.0.0-test", "v0.0.0-test"

	t.Cleanup(func() { Version, rootCmd.Version = previousVersion, previousRootVersion })

	workDir := ""

	if c.dir != nil {
		workDir = c.dir(t)

		previousDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}

		if err := os.Chdir(workDir); err != nil {
			t.Fatalf("chdir %s: %v", workDir, err)
		}

		t.Cleanup(func() { _ = os.Chdir(previousDir) })
	}

	var stdout, stderr bytes.Buffer

	err := ExecuteWith(context.Background(), c.args, strings.NewReader(c.stdin), &stdout, &stderr)

	gotExit := 0

	if err != nil {
		gotExit = ExitCode(err)
		// The process, not cobra, prints the error — mirror it so the golden
		// shows every byte the user sees.
		stderr.WriteString(renderError(err))
	}

	if gotExit != c.wantExit {
		t.Errorf("exit code = %d, want %d\nstderr:\n%s", gotExit, c.wantExit, stderr.String())
	}

	norm := normaliser{configPath: configPath, home: home, apiURL: apiURL, workDir: workDir}

	compareGolden(t, c.name+".stdout", norm.apply(stdout.String()))
	compareGolden(t, c.name+".stderr", norm.apply(stderr.String()))
}

// normaliser replaces everything that legitimately varies between runs and
// machines: temp paths, the ephemeral API server port, clock values.
type normaliser struct {
	configPath string
	home       string
	apiURL     string
	workDir    string
}

var (
	// The payments table prints created_at as "2006-01-02 15:04" in the
	// local zone. TZ=UTC is set for every case, but Go resolves time.Local
	// once per process and another test may have resolved it first, so the
	// rendered value is normalised rather than trusted.
	goldenDateTimeRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)
	goldenTimeRe     = regexp.MustCompile(`\d{2}:\d{2}:\d{2}`)
	goldenMillisRe   = regexp.MustCompile(`\(\d+ms\)`)
	goldenLoopbackRe = regexp.MustCompile(`127\.0\.0\.1:\d+`)
)

func (n normaliser) apply(s string) string {
	// Longest first: the config file lives inside its own temp directory.
	for _, pair := range [][2]string{
		{n.apiURL, "<API>"},
		{n.configPath, "<CONFIG>"},
		{n.workDir, "<DIR>"},
		{n.home, "<HOME>"},
	} {
		if pair[0] == "" {
			continue
		}

		s = strings.ReplaceAll(s, pair[0], pair[1])
		// macOS hands out /var/folders/… but resolves it to /private/var/….
		s = strings.ReplaceAll(s, "/private"+pair[0], pair[1])
	}

	s = goldenDateTimeRe.ReplaceAllString(s, "<CREATED>")
	s = goldenTimeRe.ReplaceAllString(s, "<TIME>")
	s = goldenMillisRe.ReplaceAllString(s, "(<MS>ms)")
	s = goldenLoopbackRe.ReplaceAllString(s, "<ADDR>")

	return s
}

func compareGolden(t *testing.T, file, got string) {
	t.Helper()

	path := filepath.Join(goldenRoot, file)

	if *update {
		if err := os.MkdirAll(goldenRoot, 0o755); err != nil {
			t.Fatalf("create %s: %v", goldenRoot, err)
		}

		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}

		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\nrun `go test ./cmd -run TestGolden -update` to create it\n--- got ---\n%s",
			path, err, got)
	}

	if want := string(raw); want != got {
		t.Errorf("%s does not match\n%s", path, lineDiff(want, got))
	}
}

// lineDiff prints the first differing lines with context. Deliberately
// simple — a golden mismatch is read by a human, not machine-parsed.
func lineDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	var b strings.Builder

	b.WriteString("--- want\n+++ got\n")

	shown := 0

	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		w, g := "", ""
		if i < len(wantLines) {
			w = wantLines[i]
		}

		if i < len(gotLines) {
			g = gotLines[i]
		}

		if w == g {
			continue
		}

		if shown >= 20 {
			fmt.Fprintf(&b, "… further differences suppressed\n")

			break
		}

		shown++

		fmt.Fprintf(&b, "line %d:\n-%q\n+%q\n", i+1, w, g)
	}

	return b.String()
}

// resetFlags returns the package's cobra state to what it looks like at
// process start. Without it, cases interfere: the flag variables are package
// globals, pflag remembers Changed across runs (cmd/init.go:87 branches on
// it), and a stream writer left on a *subcommand* wins over the root's,
// because cobra's OutOrStdout checks the command before its parent — which is
// how cmd/login_browser_test.go can silently swallow a root-driven run's
// output when the whole package runs in one process.
func resetFlags() {
	loginKey, loginManual, loginNoBrowser = "", false, false
	paymentsStatus, paymentsLimit = "", 20
	doctorWebhookURL, doctorAppURL, doctorE2E, doctorStrict = "", "", false, false
	listenForwardTo, listenSecret = "", ""
	triggerCurrency, triggerAmountOv = "GHS", 0

	initTargets, initYes, initDryRun = nil, false, false
	initWebhookPath, initCheckoutPath, initCheckoutPage = "", "", ""
	initCheckoutFields, initCheckoutMeta = nil, nil
	initClientPath, initRegisterWebhook = "", ""
	initRotateTestKeys, initOverwrite, initFresh, initVerbose = false, false, false, false
	initGoal, initOrigin = "auto", ""
	initKeepLogs, initAccessible = false, false

	forEachCommand(rootCmd, func(cmd *cobra.Command) {
		reset := func(f *pflag.Flag) {
			// Slice flags cannot round-trip through Set(DefValue): pflag
			// renders an empty default as the literal "[]", and Set on an
			// already-set slice appends instead of replacing.
			if slice, ok := f.Value.(pflag.SliceValue); ok {
				_ = slice.Replace(defaultSliceValue(f.DefValue))
			} else {
				_ = f.Value.Set(f.DefValue)
			}

			f.Changed = false
		}

		cmd.Flags().VisitAll(reset)
		cmd.PersistentFlags().VisitAll(reset)

		cmd.SetIn(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
	})

	rootCmd.SetArgs(nil)
	rootCmd.SetContext(context.Background())
}

// defaultSliceValue parses pflag's rendering of a slice default ("[]" or
// "[a,b]") back into the slice it came from.
func defaultSliceValue(def string) []string {
	trimmed := strings.Trim(def, "[]")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, ",")
}

func forEachCommand(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)

	for _, child := range cmd.Commands() {
		forEachCommand(child, fn)
	}
}

// jsonServer answers a fixed body and status for exact paths, and 404s
// everything else, so an unexpected request shows up as a golden diff instead
// of silently succeeding.
func jsonServer(t *testing.T, routes map[string]jsonRoute) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"no route"}`))

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(route.status)
		_, _ = w.Write([]byte(route.body))
	}))
}

type jsonRoute struct {
	status int
	body   string
}

// paymentsServer answers GET /v1/payments — the probe every authenticated
// command makes first — and 404s anything else.
func paymentsServer(status int, body string) func(*testing.T) *httptest.Server {
	return func(t *testing.T) *httptest.Server {
		return jsonServer(t, map[string]jsonRoute{
			"/v1/payments": {status: status, body: body},
		})
	}
}

// nextProjectDir writes the smallest tree scaffold.Detect calls a Next.js
// app: a package.json that declares its package manager (so no lockfile walk
// can reach outside the fixture) plus a tsconfig.
func nextProjectDir(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	writeGoldenFile(t, root, "package.json", `{
  "name": "shop",
  "packageManager": "pnpm@10.0.0",
  "scripts": {"dev": "next dev"},
  "dependencies": {"next": "16.0.0", "react": "19.0.0"}
}
`)
	writeGoldenFile(t, root, "tsconfig.json", "{}\n")

	return root
}

// nextProjectDirUnbootstrapped is the doctor fixture: the same project, plus
// an empty manifest whose only job is to pin the checkout demo origin at a
// port nothing can be listening on. Without it doctor probes
// http://localhost:3000, and whether a dev server happens to be running
// decides the output.
func nextProjectDirUnbootstrapped(t *testing.T) string {
	t.Helper()

	root := nextProjectDir(t)

	manifest, err := json.Marshal(map[string]string{"origin": "http://localhost:1"})
	if err != nil {
		t.Fatal(err)
	}

	writeGoldenFile(t, filepath.Join(root, ".reevit"), "manifest.json", string(manifest))

	return root
}

func writeGoldenFile(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goldenCases() []goldenCase {
	const testKey = "pfk_test_ok.sec"

	twoPayments := `[
	  {"id":"pmt_1","provider":"paystack","method":"card","status":"succeeded",
	   "amount":12500,"currency":"GHS","created_at":"2026-01-02T09:30:00Z"},
	  {"id":"pmt_2","provider":"hubtel","method":"mobile_money","status":"failed",
	   "amount":900,"currency":"GHS","created_at":"2026-01-02T10:15:00Z"}
	]`

	return []goldenCase{
		{name: "help-root", args: []string{"--help"}},
		{name: "help-login", args: []string{"login", "--help"}},
		{name: "help-init", args: []string{"init", "--help"}},
		{name: "help-doctor", args: []string{"doctor", "--help"}},
		{name: "help-listen", args: []string{"listen", "--help"}},
		{name: "help-trigger", args: []string{"trigger", "--help"}},
		{name: "help-payments", args: []string{"payments", "--help"}},
		{name: "help-payments-list", args: []string{"payments", "list", "--help"}},
		{name: "version", args: []string{"--version"}},
		// The colour baseline: NO_COLOR and TERM genuinely absent, FORCE_COLOR
		// set. Identical to help-root today because nothing colours anything
		// yet — which is exactly what makes it the reference for plan 030.
		{
			name:     "help-root-forced-color",
			args:     []string{"--help"},
			unsetEnv: []string{"NO_COLOR", "TERM"},
			env:      map[string]string{"FORCE_COLOR": "1"},
		},

		{name: "unknown-command", args: []string{"doctro"}, wantExit: 1},
		{name: "unknown-flag", args: []string{"listen", "--forwardto", "x"}, wantExit: 1},

		{
			name: "login-key-rejected",
			args: []string{"login", "--key", "pfk_test_bad"},
			server: paymentsServer(http.StatusUnauthorized,
				`{"code":"unauthorized","message":"invalid key"}`),
			wantExit: 1,
		},
		{
			name:   "login-key-saved",
			args:   []string{"login", "--key", testKey},
			server: paymentsServer(http.StatusOK, `[]`),
		},
		{
			name: "login-browser-approved",
			args: []string{"login", "--no-browser"},
			server: func(t *testing.T) *httptest.Server {
				return pairingServer(t, []string{"pending", "approved"})
			},
		},
		{
			name: "login-browser-denied",
			args: []string{"login", "--no-browser"},
			server: func(t *testing.T) *httptest.Server {
				return pairingServer(t, []string{"denied"})
			},
			wantExit: 1,
		},

		{
			name:   "payments-list-empty",
			args:   []string{"payments", "list"},
			env:    map[string]string{"REEVIT_API_KEY": testKey},
			server: paymentsServer(http.StatusOK, `[]`),
		},
		{
			name:   "payments-list-rows",
			args:   []string{"payments", "list", "--limit", "2"},
			env:    map[string]string{"REEVIT_API_KEY": testKey, "TZ": "UTC"},
			server: paymentsServer(http.StatusOK, twoPayments),
		},
		{
			name: "payments-list-forbidden",
			args: []string{"payments", "list"},
			env:  map[string]string{"REEVIT_API_KEY": testKey},
			server: paymentsServer(http.StatusForbidden,
				`{"code":"insufficient_scope","message":"missing payments:read"}`),
			wantExit: 1,
		},

		{name: "trigger-unknown-event", args: []string{"trigger", "payment.bogus"}, wantExit: 1},
		{name: "trigger-not-logged-in", args: []string{"trigger", "payment.succeeded"}, wantExit: 1},
		{name: "listen-missing-forward-to", args: []string{"listen"}, wantExit: 1},

		{
			name:     "doctor-no-project",
			args:     []string{"doctor"},
			env:      map[string]string{"REEVIT_API_KEY": testKey},
			dir:      func(t *testing.T) string { return t.TempDir() },
			server:   paymentsServer(http.StatusOK, `[]`),
			wantExit: 1,
		},
		{
			name:     "doctor-next-project-offline",
			args:     []string{"doctor"},
			env:      map[string]string{"REEVIT_API_KEY": testKey},
			dir:      nextProjectDirUnbootstrapped,
			server:   paymentsServer(http.StatusOK, `[]`),
			wantExit: 1,
		},

		// init-non-tty must stay ahead of init-dry-run: it is what proves
		// resetFlags clears pflag's Changed("goal") between cases.
		{name: "init-non-tty", args: []string{"init"}, dir: nextProjectDir, wantExit: 1},
		// --goal webhook, not full: the dry-run plan lists a target's files by
		// ranging over scaffold.Target.Files, a map (internal/setup/plan.go:117),
		// so any goal whose targets contribute more than one file prints them
		// in a random order. webhook is the one single-file target. See
		// AGENTS.md — fixing that ordering is production work, not this plan's.
		{name: "init-dry-run", args: []string{"init", "--dry-run", "--goal", "webhook"}, dir: nextProjectDir},
	}
}

// TestGoldenStreamsAreSeparate guards the property the shared-buffer tests
// cannot: that a success writes nothing to stderr and a usage failure writes
// nothing to stdout. Plan 030 will edit these expectations when it moves
// diagnostics, not delete them.
func TestGoldenStreamsAreSeparate(t *testing.T) {
	t.Run("login-key-saved writes nothing to stderr", func(t *testing.T) {
		if got := readGolden(t, "login-key-saved.stderr"); got != "" {
			t.Errorf("stderr = %q, want empty", got)
		}

		if got := readGolden(t, "login-key-saved.stdout"); !strings.Contains(got, "Saved to <CONFIG>") {
			t.Errorf("stdout = %q, want the saved-to confirmation", got)
		}
	})

	t.Run("unknown-flag writes nothing to stdout", func(t *testing.T) {
		if got := readGolden(t, "unknown-flag.stdout"); got != "" {
			t.Errorf("stdout = %q, want empty", got)
		}

		if got := readGolden(t, "unknown-flag.stderr"); got == "" {
			t.Error("stderr is empty, want the flag error")
		}
	})
}

func readGolden(t *testing.T, file string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(goldenRoot, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	return string(raw)
}
