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
	// Anchored at the start of a line, because that is where the only clock
	// value the CLI prints in this shape lives: `listen`'s delivery line
	// opens with time.Now().Format("15:04:05"). Unanchored it also ate the
	// time out of every RFC 3339 created_at in a --json document, which is
	// fixed test data, not a clock — and normalising fixture data away is
	// how a golden stops proving anything about it.
	goldenTimeRe = regexp.MustCompile(`(?m)^\d{2}:\d{2}:\d{2}`)
	// The forwarded-event line prints its round trip as a bare `12ms`
	// column; it used to be parenthesised, and the pattern moved with it.
	goldenMillisRe   = regexp.MustCompile(`\b\d+ms\b`)
	goldenLoopbackRe = regexp.MustCompile(`127\.0\.0\.1:\d+`)
	// Everything after "dial tcp" is the operating system's wording, not
	// ours. The property doctor-api-unreachable exists to pin is that the
	// `request GET /payments: Get "…":` prefix was stripped off the front,
	// so the platform-specific tail is normalised away.
	goldenDialRe = regexp.MustCompile(`dial tcp \S+.*`)
	// SGR sequences, for asserting that colour changed the bytes of a table
	// without changing where its columns fall.
	goldenSGRRe = regexp.MustCompile("\x1b\\[[0-9;]*m")
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
	s = goldenMillisRe.ReplaceAllString(s, "<MS>ms")
	s = goldenLoopbackRe.ReplaceAllString(s, "<ADDR>")
	s = goldenDialRe.ReplaceAllString(s, "dial tcp <DIAL>")

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
		// A misspelled flag is a usage error, not a runtime failure: exit 2.
		{name: "unknown-flag", args: []string{"listen", "--forwardto", "x"}, wantExit: 2},

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
		// The one-time telemetry disclosure. It has to be the FIRST thing on
		// stderr: it used to be printed by telemetry.Report, which runs after
		// the command, so the notice landed underneath the output of the very
		// run it was disclosing. REEVIT_TELEMETRY is unset (the baseline
		// turns telemetry off for every other case) and REEVIT_CONFIG points
		// at a file that does not exist yet, so this is a genuine first run.
		{
			name:     "first-run-notice",
			args:     []string{"payments", "list", "--limit", "2"},
			env:      map[string]string{"REEVIT_API_KEY": testKey, "TZ": "UTC"},
			unsetEnv: []string{"REEVIT_TELEMETRY", "DO_NOT_TRACK"},
			server:   paymentsServer(http.StatusOK, twoPayments),
		},
		// The primary --json case runs against the bare array /payments
		// actually returns today.
		{
			name:   "payments-list-json",
			args:   []string{"payments", "list", "--json", "--limit", "2"},
			env:    map[string]string{"REEVIT_API_KEY": testKey, "TZ": "UTC"},
			server: paymentsServer(http.StatusOK, twoPayments),
		},
		// …and the second against the envelope three other backend list
		// endpoints already use, so the CLI does not have to be changed on
		// the day /payments joins them.
		{
			name: "payments-list-json-envelope",
			args: []string{"payments", "list", "--json", "--limit", "2"},
			env:  map[string]string{"REEVIT_API_KEY": testKey, "TZ": "UTC"},
			server: paymentsServer(http.StatusOK,
				`{"data":`+twoPayments+`,"pagination":{"total":2,"limit":20,"offset":0}}`),
		},
		// An empty list is `"data": []`, never `null`: a consumer piping
		// into `.data[]` should not need a null guard, and the human path's
		// "No payments" hint has no place on a machine-readable stdout.
		{
			name:   "payments-list-empty-json",
			args:   []string{"payments", "list", "--json"},
			env:    map[string]string{"REEVIT_API_KEY": testKey},
			server: paymentsServer(http.StatusOK, `[]`),
		},
		{
			name: "payments-list-forbidden",
			args: []string{"payments", "list"},
			env:  map[string]string{"REEVIT_API_KEY": testKey},
			server: paymentsServer(http.StatusForbidden,
				`{"code":"insufficient_scope","message":"missing payments:read"}`),
			wantExit: 1,
		},

		// --quiet trims the conversation and nothing else. The three cases
		// below pin the three halves of that: a hint disappears, a success
		// confirmation disappears, and a failure does not.
		{
			name:   "payments-list-empty-quiet",
			args:   []string{"payments", "list", "--quiet"},
			env:    map[string]string{"REEVIT_API_KEY": testKey},
			server: paymentsServer(http.StatusOK, `[]`),
		},
		{
			name:   "login-key-saved-quiet",
			args:   []string{"login", "-q", "--key", testKey},
			server: paymentsServer(http.StatusOK, `[]`),
		},
		{
			name: "payments-list-forbidden-quiet",
			args: []string{"payments", "list", "--quiet"},
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
			wantExit: 3,
		},
		// The API is not there at all. The finding and the transport cause get
		// a line each: the wrapped error names a request the user never typed
		// and repeats a URL doctor printed a line earlier, and dragging all of
		// that onto the warning line pushed the real cause off an 80-column
		// terminal.
		{
			name: "doctor-api-unreachable",
			args: []string{"doctor"},
			env: map[string]string{
				"REEVIT_API_KEY": testKey,
				// Port 1 is reserved and never listening.
				"REEVIT_API_URL": "http://127.0.0.1:1",
			},
			dir:      func(t *testing.T) string { return t.TempDir() },
			wantExit: 3,
		},
		// A legacy key shape: config.Load cannot derive the mode from it, so
		// doctor says where the label actually came from.
		{
			name:     "doctor-unkeyed-mode",
			args:     []string{"doctor"},
			env:      map[string]string{"REEVIT_API_KEY": "rk_legacy_key", "REEVIT_MODE": "live"},
			dir:      func(t *testing.T) string { return t.TempDir() },
			server:   paymentsServer(http.StatusOK, `[]`),
			wantExit: 3,
		},
		{
			name:     "doctor-next-project-offline",
			args:     []string{"doctor"},
			env:      map[string]string{"REEVIT_API_KEY": testKey},
			dir:      nextProjectDirUnbootstrapped,
			server:   paymentsServer(http.StatusOK, `[]`),
			wantExit: 3,
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

		// The coloured pair. The harness writes to bytes.Buffers, so nothing is
		// a TTY: FORCE_COLOR is what turns colour on, which is the documented
		// override path. LC_ALL/LC_CTYPE are unset as well as NO_COLOR/TERM,
		// because an ambient LC_ALL=C would otherwise beat LANG and silently
		// snapshot the ASCII glyphs on some machines.
		{
			name: "doctor-next-project-offline-color",
			args: []string{"doctor"},
			env: map[string]string{
				"REEVIT_API_KEY": testKey, "FORCE_COLOR": "1", "LANG": "en_US.UTF-8",
			},
			unsetEnv: []string{"NO_COLOR", "TERM", "LC_ALL", "LC_CTYPE"},
			dir:      nextProjectDirUnbootstrapped,
			server:   paymentsServer(http.StatusOK, `[]`),
			wantExit: 3,
		},
		// The counter-example: `payments list` is data, so turning colour on
		// must change nothing. This golden is byte-identical to
		// payments-list-rows and is here to keep it that way.
		{
			name: "payments-list-rows-color",
			args: []string{"payments", "list", "--limit", "2"},
			env: map[string]string{
				"REEVIT_API_KEY": testKey, "TZ": "UTC",
				"FORCE_COLOR": "1", "LANG": "en_US.UTF-8",
			},
			unsetEnv: []string{"NO_COLOR", "TERM", "LC_ALL", "LC_CTYPE"},
			server:   paymentsServer(http.StatusOK, twoPayments),
		},
		// The pairing code is the one string the user has to read off the
		// screen and match in a browser, so it is the one string that earns
		// both weight and colour. Nothing else in the plain golden can prove
		// that, because the plain golden has no escapes at all.
		{
			name:     "login-browser-approved-color",
			args:     []string{"login", "--no-browser"},
			env:      map[string]string{"FORCE_COLOR": "1", "LANG": "en_US.UTF-8"},
			unsetEnv: []string{"NO_COLOR", "TERM", "LC_ALL", "LC_CTYPE"},
			server: func(t *testing.T) *httptest.Server {
				return pairingServer(t, []string{"pending", "approved"})
			},
		},
	}
}

// TestGoldenStreamsAreSeparate guards the property the shared-buffer tests
// cannot: stdout carries data and stderr carries the conversation.
//
// Plan 030 moved `login`'s confirmation to stderr — `reevit login > key.log`
// is not a thing anyone wants, and the command produces no data — so the
// expectation is inverted here rather than dropped.
// The normaliser is what makes a golden independent of the wall clock, so it
// is tested directly: `listen`'s delivery lines never reach a golden case
// (the command does not exit, and the harness has no cancellable context),
// which would otherwise leave the clock-dependent patterns unexercised.
func TestNormaliserErasesEveryClockValue(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a forwarded delivery line",
			in:   "12:04:07  payment.succeeded          > 200  12ms",
			want: "<TIME>  payment.succeeded          > 200  <MS>ms",
		},
		{
			name: "a payments table timestamp",
			in:   "pay_1  GHS 100.00  succeeded  2026-08-31 09:15",
			want: "pay_1  GHS 100.00  succeeded  <CREATED>",
		},
		{
			// The counter-example: an RFC 3339 timestamp inside a --json
			// document is fixture data the golden exists to pin, and it
			// must survive normalisation intact.
			name: "an RFC 3339 timestamp in a JSON document",
			in:   `{"id":"pmt_1","created_at":"2026-01-02T09:30:00Z"}`,
			want: `{"id":"pmt_1","created_at":"2026-01-02T09:30:00Z"}`,
		},
		{
			name: "an ephemeral listen port",
			in:   `dial tcp 127.0.0.1:54321: connect: connection refused`,
			want: "dial tcp <DIAL>",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := (normaliser{}).apply(test.in); got != test.want {
				t.Errorf("apply(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestGoldenStreamsAreSeparate(t *testing.T) {
	t.Run("login-key-saved writes nothing to stdout", func(t *testing.T) {
		if got := readGolden(t, "login-key-saved.stdout"); got != "" {
			t.Errorf("stdout = %q, want empty", got)
		}

		if got := readGolden(t, "login-key-saved.stderr"); !strings.Contains(got, "saved to <CONFIG>") {
			t.Errorf("stderr = %q, want the saved-to confirmation", got)
		}
	})

	t.Run("payments list keeps its table on stdout", func(t *testing.T) {
		if got := readGolden(t, "payments-list-rows.stdout"); !strings.Contains(got, "pmt_1") {
			t.Errorf("stdout = %q, want the payment rows", got)
		}

		if got := readGolden(t, "payments-list-rows.stderr"); got != "" {
			t.Errorf("stderr = %q, want empty", got)
		}
	})

	// Colour reaches the table now — a `failed` that is not red is a `failed`
	// the eye skips — but it must not move a single column. tabwriter measures
	// cells in bytes, so this is the property that forced the hand-rolled
	// layout, and stripping the escapes back out is how it is checked.
	t.Run("colour changes the payments table's bytes, never its layout", func(t *testing.T) {
		plain := readGolden(t, "payments-list-rows.stdout")
		forced := readGolden(t, "payments-list-rows-color.stdout")

		if strings.Contains(plain, "\x1b") {
			t.Errorf("NO_COLOR table carries escapes:\n%q", plain)
		}

		for _, want := range []string{
			"\x1b[32msucceeded\x1b[0m", // green
			"\x1b[31mfailed\x1b[0m",    // red
			"\x1b[2mStatus\x1b[0m",     // dim header
		} {
			if !strings.Contains(forced, want) {
				t.Errorf("coloured table is missing %q:\n%q", want, forced)
			}
		}

		if stripped := goldenSGRRe.ReplaceAllString(forced, ""); stripped != plain {
			t.Errorf("FORCE_COLOR moved a column:\n%s", lineDiff(plain, stripped))
		}
	})

	// Money is read by comparing digit positions. 9.00 under 125.00 with the
	// decimal points out of line is a column that has to be re-read.
	t.Run("the amount column is right-aligned", func(t *testing.T) {
		lines := strings.Split(strings.TrimRight(readGolden(t, "payments-list-rows.stdout"), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("lines = %q, want a header and two rows", lines)
		}

		columns := make([]int, 0, 2)

		for _, line := range lines[1:] {
			at := strings.Index(line, ".00")
			if at < 0 {
				t.Fatalf("line = %q, want a decimal amount", line)
			}

			columns = append(columns, at)
		}

		if columns[0] != columns[1] {
			t.Errorf("decimal points at %d and %d:\n%s", columns[0], columns[1],
				strings.Join(lines, "\n"))
		}
	})

	t.Run("colour reaches doctor", func(t *testing.T) {
		if got := readGolden(t, "doctor-next-project-offline-color.stderr"); !strings.Contains(got, "\x1b[32m") {
			t.Errorf("stderr = %q, want a green glyph", got)
		}

		if got := readGolden(t, "doctor-next-project-offline.stderr"); strings.Contains(got, "\x1b") {
			t.Errorf("stderr = %q, want no escape sequences with NO_COLOR=1", got)
		}
	})

	t.Run("colour and weight reach the pairing code", func(t *testing.T) {
		got := readGolden(t, "login-browser-approved-color.stderr")

		// Bold (1) then cyan (36), from Bold(Accent(code)) — the code is the
		// only thing on the screen the user has to transcribe.
		if !strings.Contains(got, "\x1b[1m\x1b[36mGX7M-4KP9") {
			t.Errorf("stderr = %q, want the pairing code in bold cyan", got)
		}

		// The confirm link is a URL, so it gets the underline the styler
		// reserves for links rather than plain accent.
		if !strings.Contains(got, "\x1b[36;4mhttps://") {
			t.Errorf("stderr = %q, want the confirm URL underlined", got)
		}

		if plain := readGolden(t, "login-browser-approved.stderr"); strings.Contains(plain, "\x1b") {
			t.Errorf("stderr = %q, want no escape sequences with NO_COLOR=1", plain)
		}
	})

	t.Run("doctor names the cause, not the request that wrapped it", func(t *testing.T) {
		got := readGolden(t, "doctor-api-unreachable.stderr")

		if strings.Contains(got, "request GET /payments") {
			t.Errorf("stderr = %q, want the api.Client wrapper stripped from the cause", got)
		}

		if !strings.Contains(got, "! could not reach the API to verify the key\n    dial tcp") {
			t.Errorf("stderr = %q, want the cause on its own dim line under the warning", got)
		}
	})

	// --quiet is a promise about the conversation, not about the outcome. A
	// quiet flag that also swallows the reason a command failed turns a CI
	// log into an exit code with no explanation.
	t.Run("quiet drops the hints and the confirmations, never the failure", func(t *testing.T) {
		if got := readGolden(t, "payments-list-empty-quiet.stderr"); got != "" {
			t.Errorf("stderr = %q, want the \"No payments\" hint suppressed", got)
		}

		if loud := readGolden(t, "payments-list-empty.stderr"); !strings.Contains(loud, "No payments") {
			t.Errorf("stderr = %q, want the hint without --quiet — otherwise the case above proves nothing", loud)
		}

		if got := readGolden(t, "login-key-saved-quiet.stderr"); got != "" {
			t.Errorf("stderr = %q, want the saved-to confirmation suppressed", got)
		}

		if got := readGolden(t, "payments-list-forbidden-quiet.stderr"); !strings.Contains(got, "insufficient_scope") {
			t.Errorf("stderr = %q, want --quiet to leave the failure intact", got)
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

// TestJSONStdoutIsPureJSON is the whole promise of --json in one assertion:
// a consumer runs `reevit … --json | jq` and every byte on stdout parses.
// One stray progress line, one hint that forgot which stream it was on, and
// the pipeline dies — and a golden that merely "looks right" to a reviewer
// would not catch it, because a human reads past a leading blank line.
//
// It walks the goldens rather than taking a list, so a --json case added
// later is covered without anyone remembering to add it here.
func TestJSONStdoutIsPureJSON(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join(goldenRoot, "*-json*.stdout"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	// Without this the whole test passes vacuously the day someone renames
	// the goldens.
	if len(files) < 2 {
		t.Fatalf("found %d *-json*.stdout goldens, want the --json cases", len(files))
	}

	for _, path := range files {
		name := filepath.Base(path)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			body := strings.TrimSuffix(string(raw), "\n")
			if body == "" {
				t.Fatalf("%s is empty, so --json produced no document at all", name)
			}

			// One document per run. `listen` is the only NDJSON producer
			// and it never exits, so the harness cannot run it — its lines
			// are checked in TestListenEmitsOneJSONObjectPerDelivery
			// instead. Parsing line by line anyway is what would catch a
			// second document appearing here.
			lines := strings.Split(body, "\n")
			if len(lines) != 1 {
				t.Fatalf("%s has %d lines, want exactly one JSON document:\n%s", name, len(lines), body)
			}

			for i, line := range lines {
				var document struct {
					Schema string `json:"schema"`
				}

				if err := json.Unmarshal([]byte(line), &document); err != nil {
					t.Fatalf("%s line %d does not parse as JSON (%v):\n%q", name, i+1, err, line)
				}

				// The version string is what lets a consumer branch on the
				// shape instead of on the CLI's own version number.
				if document.Schema == "" {
					t.Errorf("%s line %d carries no \"schema\":\n%q", name, i+1, line)
				}

				if !strings.HasPrefix(document.Schema, "reevit.cli.") || !strings.Contains(document.Schema, ".v") {
					t.Errorf("%s line %d schema = %q, want reevit.cli.<command>.v<n>", name, i+1, document.Schema)
				}
			}
		})
	}
}
