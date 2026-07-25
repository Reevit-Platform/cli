package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func TestApplyRunsOrderedPipelineAndCompletesManifest(t *testing.T) {
	t.Parallel()

	project, targets := setupFixture(t)
	var calls []string
	bootstrapper := fakeBootstrapper{
		run: func(_ context.Context, request api.BootstrapRequest) (api.BootstrapResult, error) {
			manifest, err := scaffold.ReadManifest(project)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Status != "pending" || manifest.ProjectID != request.ProjectID {
				t.Fatalf("bootstrap saw manifest %#v", manifest)
			}
			if !reflect.DeepEqual(request.Capabilities, []string{"server"}) {
				t.Fatalf("bootstrap capabilities = %v, want only credential capabilities", request.Capabilities)
			}
			calls = append(calls, "bootstrap")
			return bootstrapResult(request.ProjectID), nil
		},
	}
	writer := &fakeWriter{calls: &calls}
	verifier := fakeVerifier{run: func(_ context.Context, _ string, _ []scaffold.Target, server, _ string) error {
		calls = append(calls, "verify")
		if server != "pfk_test_project.secret" {
			t.Fatalf("verifier server key = %q", server)
		}
		return nil
	}}
	result, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		LocalOrigin: "http://localhost:8000", LoginKey: "pfk_test_login.secret",
		BaseURL: "https://api.example.test",
	}, Dependencies{
		Bootstrapper: bootstrapper,
		Runner: fakeRunner{run: func(context.Context, string, []string, bool) (string, error) {
			calls = append(calls, "install")
			return "", nil
		}},
		Writer: writer, Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: verifier,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"bootstrap", "env", "install", "source", "verify"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if result.Manifest.Status != "complete" || result.Manifest.ServerKeyID != "pfk_test_project" {
		t.Fatalf("result manifest = %#v", result.Manifest)
	}
	onDisk, err := scaffold.ReadManifest(project)
	if err != nil || onDisk.Status != "complete" {
		t.Fatalf("manifest = %#v, err = %v", onDisk, err)
	}
	if writer.credentials.WebhookSecret != "whsec_project" {
		t.Fatalf("writer secret = %q", writer.credentials.WebhookSecret)
	}
}

func TestApplyInstallFailureLeavesRecoverableCredentialsAndNoSourceFiles(t *testing.T) {
	t.Parallel()

	project, targets := setupFixture(t)
	writer := &fakeWriter{}
	_, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		LoginKey: "pfk_test_login.secret",
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			_ context.Context, request api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			return bootstrapResult(request.ProjectID), nil
		}},
		Runner: fakeRunner{run: func(context.Context, string, []string, bool) (string, error) {
			return ".reevit/logs/init-test.log", errors.New("installer exploded")
		}},
		Writer: writer, Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err == nil || !strings.Contains(err.Error(), ".reevit/logs/init-test.log") {
		t.Fatalf("error = %v", err)
	}
	if writer.envCalls != 1 || writer.applyCalls != 0 {
		t.Fatalf("writer calls after install failure: env=%d apply=%d", writer.envCalls, writer.applyCalls)
	}
	manifest, readErr := scaffold.ReadManifest(project)
	if readErr != nil || manifest.Status != "pending" || manifest.ServerKeyID == "" {
		t.Fatalf("manifest = %#v, err = %v", manifest, readErr)
	}
}

func TestApplyRejectsCredentialThatDoesNotMatchManifestID(t *testing.T) {
	t.Parallel()

	project, targets := setupFixture(t)
	if err := os.WriteFile(
		filepath.Join(project.Root, ".env"),
		[]byte("REEVIT_API_KEY=pfk_test_other.secret\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		Manifest: scaffold.Manifest{
			ProjectID: "rvproj_existing", ServerKeyID: "pfk_test_expected",
		},
		LoginKey: "pfk_test_login.secret",
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			context.Context, api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			t.Fatal("bootstrap must not run for mismatched local credentials")
			return api.BootstrapResult{}, nil
		}},
		Runner: fakeRunner{}, Writer: &fakeWriter{},
		Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err == nil || !strings.Contains(err.Error(), "--rotate-test-keys") {
		t.Fatalf("error = %v", err)
	}
}

func TestInspectProjectKeysRecoversIDsFromPendingEnv(t *testing.T) {
	t.Parallel()

	project, _ := setupFixture(t)
	if err := os.WriteFile(
		filepath.Join(project.Root, ".env"),
		[]byte("REEVIT_API_KEY=pfk_test_recovered.secret\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	keys, err := inspectProjectKeys(Plan{
		Project: project,
		Manifest: scaffold.Manifest{
			ProjectID: "rvproj_interrupted",
			Status:    "pending",
		},
		LoginKey: "pfk_test_login.secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys.existingServerID != "pfk_test_recovered" {
		t.Fatalf("existing server ID = %q", keys.existingServerID)
	}
}

func TestInspectProjectKeysRecoversRotatedIDsFromPendingEnv(t *testing.T) {
	t.Parallel()

	project, _ := setupFixture(t)
	if err := os.WriteFile(
		filepath.Join(project.Root, ".env"),
		[]byte("REEVIT_API_KEY=pfk_test_rotated.secret\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	keys, err := inspectProjectKeys(Plan{
		Project: project,
		Manifest: scaffold.Manifest{
			ProjectID:   "rvproj_interrupted",
			ServerKeyID: "pfk_test_predecessor",
			Status:      "pending",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys.existingServerID != "pfk_test_rotated" {
		t.Fatalf("existing server ID = %q", keys.existingServerID)
	}
}

func TestApplyBootstrapFailureLeavesSecretFreePendingManifest(t *testing.T) {
	t.Parallel()

	project, targets := setupFixture(t)
	_, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		LoginKey: "pfk_test_login.secret",
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			context.Context, api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			return api.BootstrapResult{}, errors.New("platform unavailable")
		}},
		Runner: fakeRunner{}, Writer: &fakeWriter{},
		Secrets: fakeSecrets{secret: "whsec_should_not_exist"}, Verifier: fakeVerifier{},
	})
	if err == nil {
		t.Fatal("bootstrap failure returned nil")
	}
	raw, readErr := os.ReadFile(filepath.Join(project.Root, ".reevit", "manifest.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), `"status": "pending"`) {
		t.Fatalf("pending manifest leaked a secret or has wrong state:\n%s", raw)
	}
	if _, statErr := os.Stat(filepath.Join(project.Root, ".env")); !os.IsNotExist(statErr) {
		t.Fatalf("bootstrap failure wrote env: %v", statErr)
	}
}

func TestApplyHonorsExplicitExistingFileResolution(t *testing.T) {
	t.Parallel()

	project, allTargets := setupFixture(t)
	targets := []scaffold.Target{allTargets[1]}
	if err := os.WriteFile(
		filepath.Join(project.Root, "reevit_client.py"),
		[]byte("existing"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWriter{}

	_, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		LoginKey:      "pfk_test_login.secret",
		ExistingFiles: scaffold.ExistingFilesOverwrite,
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			_ context.Context, request api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			return bootstrapResult(request.ProjectID), nil
		}},
		Runner: fakeRunner{}, Writer: writer,
		Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !writer.overwrite {
		t.Fatal("writer did not receive overwrite decision")
	}
}

func TestApplyBackupFailureHappensBeforeCredentialRotation(t *testing.T) {
	t.Parallel()

	project, allTargets := setupFixture(t)
	targets := []scaffold.Target{allTargets[1]}
	source := filepath.Join(project.Root, "reevit_client.py")
	if err := os.WriteFile(source, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, ".reevit"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	bootstrapCalled := false

	_, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets,
		ExistingFiles: scaffold.ExistingFilesFresh, RotateCredentials: true,
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			_ context.Context, request api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			bootstrapCalled = true
			return bootstrapResult(request.ProjectID), nil
		}},
		Runner: fakeRunner{}, Writer: &fakeWriter{},
		Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("Apply error = %v", err)
	}
	if bootstrapCalled {
		t.Fatal("credential rotation started before backups completed")
	}
	if got, readErr := os.ReadFile(source); readErr != nil || string(got) != "existing" {
		t.Fatalf("source changed before backup completed: %q, %v", got, readErr)
	}
}

func TestApplyKeepDoesNotClaimSkippedUnmanagedFile(t *testing.T) {
	t.Parallel()

	project, allTargets := setupFixture(t)
	targets := []scaffold.Target{allTargets[1]}
	path := "reevit_client.py"
	if err := os.WriteFile(filepath.Join(project.Root, path), []byte("user file"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWriter{
		results: []scaffold.FileResult{{Path: path, Skipped: true}},
	}

	result, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, CLIVersion: "test",
		LoginKey:      "pfk_test_login.secret",
		ExistingFiles: scaffold.ExistingFilesKeep,
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			_ context.Context, request api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			return bootstrapResult(request.ProjectID), nil
		}},
		Runner: fakeRunner{}, Writer: writer,
		Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Manifest.GeneratedFiles) != 0 {
		t.Fatalf("generated files = %v; skipped user file must remain unmanaged", result.Manifest.GeneratedFiles)
	}
}

func TestApplyFreshReconcilesPriorGeneratedFilesAndManifest(t *testing.T) {
	t.Parallel()

	project, allTargets := setupFixture(t)
	targets := []scaffold.Target{allTargets[1]}
	for path, content := range map[string]string{
		"reevit_client.py":  "old client",
		"reevit_webhook.py": "stale webhook",
	} {
		if err := os.WriteFile(filepath.Join(project.Root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := scaffold.Manifest{
		ProjectID:      "rvproj_existing",
		ServerKeyID:    "pfk_test_old",
		GeneratedFiles: []string{"reevit_client.py", "reevit_webhook.py"},
		Status:         "complete",
	}

	result, err := Apply(context.Background(), Plan{
		Project: project, Targets: targets, Manifest: manifest, CLIVersion: "test",
		LoginKey: "pfk_test_login.secret", RotateCredentials: true,
		ExistingFiles: scaffold.ExistingFilesFresh,
	}, Dependencies{
		Bootstrapper: fakeBootstrapper{run: func(
			_ context.Context, request api.BootstrapRequest,
		) (api.BootstrapResult, error) {
			return bootstrapResult(request.ProjectID), nil
		}},
		Runner: fakeRunner{}, Writer: FileWriter{},
		Secrets: fakeSecrets{secret: "whsec_project"}, Verifier: fakeVerifier{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project.Root, "reevit_webhook.py")); !os.IsNotExist(err) {
		t.Fatalf("stale webhook still exists: %v", err)
	}
	if !reflect.DeepEqual(result.Manifest.GeneratedFiles, []string{"reevit_client.py"}) {
		t.Fatalf("generated files = %v", result.Manifest.GeneratedFiles)
	}
}

func TestReconcileGeneratedOwnershipSeparatesFilesAndHostEdits(t *testing.T) {
	results := []scaffold.FileResult{
		{Path: "reevit_webhook.py"},
		{
			Path: "main.py", ManagedEdit: true,
			Edit: &scaffold.GeneratedEdit{
				Path: "main.py", Kind: "webhook", Fragments: []string{"generated"},
			},
		},
		{Path: "old.py", Removed: true},
		{Path: "old-main.py", ManagedEdit: true, Removed: true},
	}

	files := reconcileGeneratedFiles([]string{"old.py"}, results)
	if !reflect.DeepEqual(files, []string{"reevit_webhook.py"}) {
		t.Fatalf("generated files = %v", files)
	}
	edits := reconcileGeneratedEdits(
		[]scaffold.GeneratedEdit{{
			Path: "old-main.py", Kind: "webhook", Fragments: []string{"old"},
		}},
		results,
	)
	if len(edits) != 1 || edits[0].Path != "main.py" {
		t.Fatalf("generated edits = %v", edits)
	}
}

func setupFixture(t *testing.T) (scaffold.Project, []scaffold.Target) {
	t.Helper()
	project := scaffold.Project{
		Root: t.TempDir(), Stack: scaffold.StackPython,
		Framework: scaffold.FrameworkFastAPI, Installer: scaffold.InstallerPip,
	}
	targets := []scaffold.Target{
		{
			Key: scaffold.TargetWebhook, Label: "webhook",
			Files: map[string]string{"python-fastapi-webhook.py.tmpl": "reevit_webhook.py"},
		},
		{
			Key: scaffold.TargetClient, Label: "client",
			Files:       map[string]string{"python-client.py.tmpl": "reevit_client.py"},
			InstallCmds: [][]string{{"sh", "-c", "true"}}, Run: true,
		},
	}
	return project, targets
}

func bootstrapResult(projectID string) api.BootstrapResult {
	var result api.BootstrapResult
	result.Project.ID = projectID
	result.Project.OrganizationID = "org_test"
	result.Credentials.Server = &api.BootstrapCredential{
		ID: "pfk_test_project", Raw: "pfk_test_project.secret",
		Scopes: []string{"payments:read", "payments:write"},
	}
	return result
}

type fakeBootstrapper struct {
	run func(context.Context, api.BootstrapRequest) (api.BootstrapResult, error)
}

func (fake fakeBootstrapper) BootstrapProject(
	ctx context.Context,
	request api.BootstrapRequest,
) (api.BootstrapResult, error) {
	return fake.run(ctx, request)
}

type fakeRunner struct {
	run func(context.Context, string, []string, bool) (string, error)
}

func (fake fakeRunner) Run(
	ctx context.Context,
	dir string,
	command []string,
	verbose bool,
) (string, error) {
	if fake.run == nil {
		return "", nil
	}
	return fake.run(ctx, dir, command, verbose)
}

type fakeWriter struct {
	calls       *[]string
	envCalls    int
	applyCalls  int
	overwrite   bool
	credentials scaffold.ProjectCredentials
	results     []scaffold.FileResult
}

func (fake *fakeWriter) WriteEnv(
	_ scaffold.Project,
	credentials scaffold.ProjectCredentials,
) (scaffold.EnvResult, error) {
	fake.envCalls++
	fake.credentials = credentials
	if fake.calls != nil {
		*fake.calls = append(*fake.calls, "env")
	}
	return scaffold.EnvResult{EnvFile: ".env"}, nil
}

func (fake *fakeWriter) Apply(
	_ scaffold.Project,
	_ []scaffold.Target,
	options scaffold.ApplyOptions,
) ([]scaffold.FileResult, error) {
	fake.applyCalls++
	fake.overwrite = options.ExistingFiles == scaffold.ExistingFilesOverwrite ||
		options.ExistingFiles == scaffold.ExistingFilesFresh
	if fake.calls != nil {
		*fake.calls = append(*fake.calls, "source")
	}
	if fake.results != nil {
		return fake.results, nil
	}
	return []scaffold.FileResult{{Path: "reevit_client.py"}}, nil
}

type fakeSecrets struct {
	secret string
}

func (fake fakeSecrets) WebhookSigningSecret() (string, error) {
	return fake.secret, nil
}

type fakeVerifier struct {
	run func(context.Context, string, []scaffold.Target, string, string) error
}

func (fake fakeVerifier) Verify(
	ctx context.Context,
	baseURL string,
	targets []scaffold.Target,
	serverKey string,
	checkoutKey string,
) error {
	if fake.run == nil {
		return nil
	}
	return fake.run(ctx, baseURL, targets, serverKey, checkoutKey)
}
