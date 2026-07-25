package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"

	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/scaffold"
	"github.com/Reevit-Platform/cli/internal/setup"
)

func TestInitFreshNextProjectAndIdempotentRerun(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{
		"packageManager": "pnpm@10.0.0",
		"scripts": {"dev": "next dev"},
		"dependencies": {"next": "16.0.0", "react": "19.0.0"}
	}`)
	writeAcceptanceFile(t, root, "tsconfig.json", `{}`)

	binDir := filepath.Join(root, ".test-bin")
	writeAcceptanceFile(t, binDir, "pnpm", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(binDir, "pnpm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REEVIT_CONFIG", filepath.Join(root, "cli-config.json"))
	t.Setenv("REEVIT_TELEMETRY", "0")

	var bootstraps []map[string]any
	serverID, serverRaw := "pfk_test_server", "pfk_test_server.secret"
	checkoutID, checkoutRawValue := "pfk_test_checkout", "pfk_test_checkout.secret"
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reevit-Key") == "pfk_test_login.secret" && r.URL.Path == "/v1/cli/bootstrap" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bootstraps = append(bootstraps, body)
			existing := body["existing_server_key_id"] != nil && body["existing_server_key_id"] != ""
			serverResponseRaw, checkoutResponseRaw := "", ""
			if rotate, _ := body["rotate_credentials"].(bool); rotate {
				serverID, serverRaw = "pfk_test_server_rotated", "pfk_test_server_rotated.secret"
				checkoutID, checkoutRawValue = "pfk_test_checkout_rotated", "pfk_test_checkout_rotated.secret"
				serverResponseRaw, checkoutResponseRaw = serverRaw, checkoutRawValue
			} else if !existing {
				serverResponseRaw, checkoutResponseRaw = serverRaw, checkoutRawValue
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"project": map[string]any{
					"id": body["project_id"], "name": "shop", "organization_id": "org_123",
				},
				"mode": "test",
				"credentials": map[string]any{
					"server": map[string]any{
						"id": serverID, "raw": serverResponseRaw,
						"scopes": []string{"payments:read", "payments:write"},
					},
					"checkout": map[string]any{
						"id": checkoutID, "raw": checkoutResponseRaw,
						"scopes": []string{"checkout:write"},
					},
				},
				"simulator": map[string]any{"connection_id": "conn_sim", "ready": true},
				"checkout":  map[string]any{"origin": "http://localhost:3000", "origin_allowed": true},
			})
			return
		}
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			if r.Header.Get("X-Reevit-Key") != checkoutRawValue {
				t.Errorf("checkout used wrong credential")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_checkout", "session_secret": "cs_test"})
		case "/v1/payments/intents":
			if r.Header.Get("X-Reevit-Key") != serverRaw {
				t.Errorf("payment used wrong credential")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pmt_verify", "status": "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	if _, err := config.Save(config.Config{
		APIKey: "pfk_test_login.secret", BaseURL: apiServer.URL, Mode: "test",
	}); err != nil {
		t.Fatal(err)
	}

	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(func() {
		resetInitTestFlags()
		initCmd.SetIn(os.Stdin)
		initCmd.SetOut(os.Stdout)
		initCmd.SetErr(os.Stderr)
	})
	initYes = true
	var out bytes.Buffer
	initCmd.SetOut(&out)
	initCmd.SetErr(&out)
	initCmd.SetContext(context.Background())
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("first init: %v\n%s", err, out.String())
	}

	env := acceptanceRead(t, root, ".env.local")
	if strings.Contains(env, "pfk_test_login.secret") ||
		!strings.Contains(env, "REEVIT_API_KEY=pfk_test_server.secret") ||
		!strings.Contains(env, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_checkout.secret") {
		t.Fatalf("credential separation failed:\n%s", env)
	}
	for _, rel := range []string{
		"app/api/webhooks/reevit/route.ts",
		"app/reevit-demo/page.tsx",
		"components/reevit-checkout-button.tsx",
		"lib/reevit.ts",
		"app/api/reevit/checkout/route.ts",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("missing generated file %s: %v", rel, err)
		}
	}
	manifest, err := scaffold.ReadManifest(scaffold.Detect(root))
	if err != nil || manifest.Status != "complete" || manifest.ServerKeyID == "" ||
		manifest.CheckoutKeyID == "" || manifest.CLIVersion == "" {
		t.Fatalf("manifest = %#v, err = %v", manifest, err)
	}

	out.Reset()
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
	if len(bootstraps) != 2 ||
		bootstraps[1]["existing_server_key_id"] != "pfk_test_server" ||
		bootstraps[1]["existing_checkout_key_id"] != "pfk_test_checkout" {
		t.Fatalf("rerun did not reuse credential ids: %#v", bootstraps)
	}

	componentPath := filepath.Join(root, "components/reevit-checkout-button.tsx")
	if err := os.WriteFile(componentPath, []byte("custom checkout"), 0o644); err != nil {
		t.Fatal(err)
	}
	initOverwrite = true
	out.Reset()
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("overwrite rerun: %v\n%s", err, out.String())
	}
	if got := acceptanceRead(t, root, "components/reevit-checkout-button.tsx"); got == "custom checkout" {
		t.Fatal("--overwrite did not replace the generated checkout")
	}
	backups, err := filepath.Glob(filepath.Join(
		root, ".reevit", "backups", "*", "components", "reevit-checkout-button.tsx",
	))
	if err != nil || len(backups) == 0 {
		t.Fatalf("--overwrite backup = %v, err = %v", backups, err)
	}
	if got := acceptanceRead(t, root, filepath.ToSlash(strings.TrimPrefix(backups[0], root+string(os.PathSeparator)))); got != "custom checkout" {
		t.Fatalf("backup = %q", got)
	}

	initOverwrite = false
	initFresh = true
	out.Reset()
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("fresh rerun: %v\n%s", err, out.String())
	}
	env = acceptanceRead(t, root, ".env.local")
	if strings.Contains(env, "pfk_test_server.secret") ||
		strings.Contains(env, "pfk_test_checkout.secret") ||
		!strings.Contains(env, "REEVIT_API_KEY=pfk_test_server_rotated.secret") ||
		!strings.Contains(env, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_checkout_rotated.secret") {
		t.Fatalf("fresh credentials were not applied safely:\n%s", env)
	}
	if initRotateTestKeys {
		t.Fatal("--fresh leaked credential rotation into later command invocations")
	}
}

func TestInitDryRunNeedsNoLoginAndMakesNoChanges(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{
		"packageManager":"npm@11.0.0",
		"dependencies":{"react":"19.0.0"}
	}`)
	t.Setenv("REEVIT_CONFIG", filepath.Join(root, "missing-config.json"))
	t.Setenv("REEVIT_API_KEY", "")

	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(func() {
		resetInitTestFlags()
		initCmd.SetIn(os.Stdin)
		initCmd.SetOut(os.Stdout)
		initCmd.SetErr(os.Stderr)
	})
	initDryRun = true
	var out bytes.Buffer
	initCmd.SetOut(&out)
	initCmd.SetErr(&out)
	initCmd.SetIn(bytes.NewBuffer(nil))
	initCmd.SetContext(context.Background())

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Dry run") || !strings.Contains(out.String(), "reevit-demo.html") {
		t.Fatalf("dry-run plan is incomplete:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".reevit")); !os.IsNotExist(err) {
		t.Fatalf("dry run mutated the project: %v", err)
	}
}

func TestFreshPlanShowsBackupsRemovalAndCredentialRotation(t *testing.T) {
	var out bytes.Buffer
	plan := setup.Plan{}
	configureExistingSetupPlan(&plan, scaffold.ExistingFilesFresh, true)

	if err := printPlan(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"back up prior generated files",
		"remove stale outputs",
		"rotate project test credentials",
	} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("plan does not show %q:\n%s", expected, out.String())
		}
	}
	if !plan.RotateCredentials {
		t.Fatal("fresh plan did not enable credential rotation")
	}
}

func TestRotateTestKeysIsAlwaysVisibleInPlan(t *testing.T) {
	var out bytes.Buffer
	plan := setup.Plan{}
	configureExistingSetupPlan(&plan, scaffold.ExistingFilesOverwrite, true)

	if err := printPlan(&out, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rotate project test credentials") {
		t.Fatalf("rotation missing from plan:\n%s", out.String())
	}
}

func TestInitDryRunFreshPreviewsDestructiveActions(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{
		"packageManager":"npm@11.0.0",
		"dependencies":{"react":"19.0.0"}
	}`)
	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(resetInitTestFlags)
	initDryRun = true
	initFresh = true
	var out bytes.Buffer
	initCmd.SetIn(bytes.NewBuffer(nil))
	initCmd.SetOut(&out)
	initCmd.SetContext(context.Background())

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("dry-run fresh: %v\n%s", err, out.String())
	}
	for _, expected := range []string{
		"back up prior generated files",
		"remove stale outputs",
		"rotate project test credentials",
	} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("dry-run does not show %q:\n%s", expected, out.String())
		}
	}
}

func TestInitNonTTYRequiresDeterministicFlag(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{"dependencies":{"react":"19.0.0"}}`)

	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(func() {
		resetInitTestFlags()
		initCmd.SetIn(os.Stdin)
		initCmd.SetOut(os.Stdout)
		initCmd.SetErr(os.Stderr)
	})
	initCmd.SetIn(bytes.NewBuffer(nil))
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetContext(context.Background())

	err := initCmd.RunE(initCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v, want deterministic non-TTY guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".reevit")); !os.IsNotExist(statErr) {
		t.Fatalf("non-TTY rejection mutated project: %v", statErr)
	}
}

func TestInitRejectsOverwriteAndFreshTogether(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{"dependencies":{"react":"19.0.0"}}`)

	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(resetInitTestFlags)
	initDryRun = true
	initOverwrite = true
	initFresh = true
	initCmd.SetIn(bytes.NewBuffer(nil))
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetContext(context.Background())

	err := initCmd.RunE(initCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("error = %v", err)
	}
}

func TestInitInteractiveExistingSetupChoicesReachPlan(t *testing.T) {
	tests := []struct {
		name     string
		choice   string
		expected []string
	}{
		{
			name: "keep", choice: "1",
			expected: []string{"existing integration files will be kept"},
		},
		{
			name: "overwrite", choice: "2",
			expected: []string{"back up and replace existing generated integration files"},
		},
		{
			name: "fresh", choice: "3",
			expected: []string{"back up prior generated files", "rotate project test credentials"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeAcceptanceFile(t, root, "package.json", `{
				"packageManager":"npm@11.0.0",
				"dependencies":{"next":"16.0.0","react":"19.0.0"}
			}`)
			writeAcceptanceFile(t, root, "tsconfig.json", `{}`)
			project := scaffold.Detect(root)
			if err := scaffold.WriteManifest(project, scaffold.Manifest{
				Status: "complete", ProjectID: "rvproj_existing",
				GeneratedFiles: []string{"components/reevit-checkout-button.tsx"},
			}); err != nil {
				t.Fatal(err)
			}

			t.Setenv("REEVIT_CONFIG", filepath.Join(root, "cli-config.json"))
			t.Setenv("REEVIT_ACCESSIBLE", "1")
			t.Setenv("NO_COLOR", "1")
			if _, err := config.Save(config.Config{
				APIKey: "pfk_test_login.secret", BaseURL: "http://127.0.0.1:1",
				Mode: "test", OrgName: "Test organization",
			}); err != nil {
				t.Fatal(err)
			}

			oldCWD, _ := os.Getwd()
			if err := os.Chdir(root); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(oldCWD) }()

			input, terminal, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = input.Close()
				_ = terminal.Close()
			}()
			resetInitTestFlags()
			defer resetInitTestFlags()
			initTargets = []string{"checkout"}
			initAccessible = true
			initOrigin = "http://localhost:3000"
			var out bytes.Buffer
			initCmd.SetIn(terminal)
			initCmd.SetOut(&out)
			initCmd.SetContext(context.Background())

			go func() {
				_, _ = input.Write([]byte(test.choice + "\nn\n"))
			}()
			runErr := initCmd.RunE(initCmd, nil)
			if runErr == nil || ExitCode(runErr) != 130 {
				t.Fatalf("error = %v, output:\n%s", runErr, out.String())
			}
			for _, expected := range test.expected {
				if !strings.Contains(out.String(), expected) {
					t.Fatalf("plan does not contain %q:\n%s", expected, out.String())
				}
			}
		})
	}
}

func TestInitCancellationReturns130WithoutProjectMutation(t *testing.T) {
	root := t.TempDir()
	writeAcceptanceFile(t, root, "package.json", `{"dependencies":{"next":"16","react":"19"}}`)
	t.Setenv("REEVIT_CONFIG", filepath.Join(root, "cli-config.json"))
	t.Setenv("REEVIT_ACCESSIBLE", "1")
	if _, err := config.Save(config.Config{
		APIKey: "pfk_test_login.secret", BaseURL: "http://127.0.0.1:1", Mode: "test",
	}); err != nil {
		t.Fatal(err)
	}

	oldCWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	t.Cleanup(func() {
		resetInitTestFlags()
		initCmd.SetIn(os.Stdin)
		initCmd.SetOut(os.Stdout)
		initCmd.SetErr(os.Stderr)
	})
	initTargets = []string{"checkout"}
	initCmd.SetIn(strings.NewReader("n\n"))
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetContext(context.Background())

	err := initCmd.RunE(initCmd, nil)
	if err == nil || ExitCode(err) != 130 {
		t.Fatalf("error = %v, exit = %d; want cancellation exit 130", err, ExitCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(root, ".reevit")); !os.IsNotExist(statErr) {
		t.Fatalf("cancellation mutated project: %v", statErr)
	}
}

func resetInitTestFlags() {
	initTargets = nil
	initYes = false
	initDryRun = false
	initWebhookPath = ""
	initCheckoutPath = ""
	initClientPath = ""
	initRegisterWebhook = ""
	initRotateTestKeys = false
	initOverwrite = false
	initFresh = false
	initVerbose = false
	initGoal = "auto"
	initOrigin = ""
	initKeepLogs = false
	initAccessible = false
}

func writeAcceptanceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func acceptanceRead(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
