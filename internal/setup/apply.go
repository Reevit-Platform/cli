// Package setup applies a resolved Reevit project setup plan.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/sandbox"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

type Runner interface {
	Run(ctx context.Context, dir string, command []string, verbose bool) (logPath string, err error)
}

type Bootstrapper interface {
	BootstrapProject(context.Context, api.BootstrapRequest) (api.BootstrapResult, error)
}

type Writer interface {
	WriteEnv(scaffold.Project, scaffold.ProjectCredentials) (scaffold.EnvResult, error)
	Apply(scaffold.Project, []scaffold.Target, scaffold.ApplyOptions) ([]scaffold.FileResult, error)
}

type SecretGenerator interface {
	WebhookSigningSecret() (string, error)
}

type Verifier interface {
	Verify(context.Context, string, []scaffold.Target, string, string) error
}

type Event struct {
	Stage   string
	Status  string
	Detail  string
	LogPath string
}

type Plan struct {
	Project           scaffold.Project
	Targets           []scaffold.Target
	Manifest          scaffold.Manifest
	Goal              Goal
	Operations        []Operation
	Warnings          []string
	Conflicts         []string
	CLIVersion        string
	LocalOrigin       string
	LoginKey          string
	BaseURL           string
	RotateCredentials bool
	ExistingFiles     scaffold.ExistingFilesPolicy
	Verbose           bool
}

type Result struct {
	Env      scaffold.EnvResult
	Files    []scaffold.FileResult
	ShowCmds [][]string
	Manifest scaffold.Manifest
}

type Dependencies struct {
	Bootstrapper Bootstrapper
	Runner       Runner
	Writer       Writer
	Secrets      SecretGenerator
	Verifier     Verifier
	Emit         func(Event)
}

func Apply(ctx context.Context, plan Plan, deps Dependencies) (Result, error) {
	var result Result
	if err := validateDependencies(deps); err != nil {
		return result, err
	}
	if err := scaffold.PreflightWithOptions(
		plan.Project,
		plan.Targets,
		plan.Manifest,
		scaffold.PreflightOptions{ExistingFiles: plan.ExistingFiles},
	); err != nil {
		return result, err
	}
	preparation, err := scaffold.PrepareApply(
		plan.Project,
		plan.Targets,
		plan.Manifest,
		plan.ExistingFiles,
	)
	if err != nil {
		return result, err
	}

	runCmds, showCmds := installCommands(plan.Project, plan.Targets)
	result.ShowCmds = showCmds
	if err := preflightExecutables(runCmds); err != nil {
		return result, err
	}

	keys, err := inspectProjectKeys(plan)
	if err != nil {
		return result, err
	}

	manifest := plan.Manifest
	if manifest.ProjectID == "" {
		manifest.ProjectID = "rvproj_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	manifest.Status = "pending"
	manifest.CLIVersion = plan.CLIVersion
	manifest.Adapter = string(plan.Project.Framework)
	if manifest.Adapter == "" {
		manifest.Adapter = string(plan.Project.Stack)
	}
	manifest.Capabilities = manifestCapabilities(plan.Targets)
	manifest.Origin = strings.TrimRight(strings.TrimSpace(plan.LocalOrigin), "/")
	if err := scaffold.WriteManifest(plan.Project, manifest); err != nil {
		return result, err
	}
	emit(deps, Event{Stage: "manifest", Status: "complete", Detail: "pending setup recorded"})

	request := api.BootstrapRequest{
		ProjectID:             manifest.ProjectID,
		ProjectName:           filepath.Base(plan.Project.Root),
		Capabilities:          capabilities(plan.Targets),
		Origin:                manifest.Origin,
		ExistingServerKeyID:   keys.existingServerID,
		ExistingCheckoutKeyID: keys.existingCheckoutID,
		RotateCredentials:     plan.RotateCredentials,
	}
	emit(deps, Event{Stage: "bootstrap", Status: "running", Detail: "configuring Reevit test mode"})
	bootstrap, err := deps.Bootstrapper.BootstrapProject(ctx, request)
	if err != nil {
		return result, fmt.Errorf("configure Reevit project: %w", err)
	}
	keys.mergeBootstrap(&manifest, bootstrap)
	if err := requireProjectKeys(plan.Targets, keys); err != nil {
		return result, err
	}

	webhookSecret := scaffold.ReadEnvValue(plan.Project, "REEVIT_WEBHOOK_SECRET")
	if webhookSecret == "" && hasTarget(plan.Targets, scaffold.TargetWebhook) {
		webhookSecret, err = deps.Secrets.WebhookSigningSecret()
		if err != nil {
			return result, err
		}
	}
	// Persist one-time raw credentials before running package managers. If an
	// installer fails, the pending manifest and env can be safely resumed
	// without forcing a key rotation.
	result.Env, err = deps.Writer.WriteEnv(plan.Project, scaffold.ProjectCredentials{
		ServerKey: keys.server, CheckoutKey: keys.checkout,
		PreviousServerKey: keys.previousServer, PreviousCheckoutKey: keys.previousCheckout,
		OrgID: bootstrap.Project.OrganizationID, WebhookSecret: webhookSecret,
	})
	if err != nil {
		return result, markIncomplete(plan.Project, manifest, err)
	}
	emit(deps, Event{Stage: "write_env", Status: "complete", Detail: "project credentials safely stored"})
	// Persist identifiers only after the one-time raw credentials are safe in
	// the env file. A pending manifest can recover those IDs from the keys if
	// this write is interrupted.
	if err := scaffold.WriteManifest(plan.Project, manifest); err != nil {
		return result, err
	}
	emit(deps, Event{Stage: "bootstrap", Status: "complete", Detail: "test project ready"})

	for _, command := range runCmds {
		emit(deps, Event{Stage: "install", Status: "running", Detail: strings.Join(command, " ")})
		logPath, runErr := deps.Runner.Run(ctx, plan.Project.Root, command, plan.Verbose)
		if runErr != nil {
			emit(deps, Event{
				Stage: "install", Status: "failed",
				Detail: strings.Join(command, " "), LogPath: logPath,
			})
			return result, fmt.Errorf(
				"install failed (%s): %w; details: %s",
				strings.Join(command, " "), runErr, logPath,
			)
		}
		emit(deps, Event{
			Stage: "install", Status: "complete",
			Detail: strings.Join(command, " "), LogPath: logPath,
		})
	}

	result.Files, err = deps.Writer.Apply(
		plan.Project,
		plan.Targets,
		scaffold.ApplyOptions{
			ExistingFiles: plan.ExistingFiles,
			Preparation:   preparation,
		},
	)
	if err != nil {
		return result, markIncomplete(plan.Project, manifest, err)
	}
	manifest.GeneratedFiles = reconcileGeneratedFiles(manifest.GeneratedFiles, result.Files)
	manifest.GeneratedEdits = reconcileGeneratedEdits(manifest.GeneratedEdits, result.Files)
	if err := scaffold.WriteManifest(plan.Project, manifest); err != nil {
		return result, err
	}
	emit(deps, Event{Stage: "write", Status: "complete", Detail: "environment and integration files written"})

	emit(deps, Event{Stage: "verify", Status: "running", Detail: "checking project credentials against the sandbox"})
	if err := deps.Verifier.Verify(ctx, plan.BaseURL, plan.Targets, keys.server, keys.checkout); err != nil {
		return result, markIncomplete(plan.Project, manifest, err)
	}

	manifest.Status = "complete"
	if err := scaffold.WriteManifest(plan.Project, manifest); err != nil {
		return result, err
	}
	result.Manifest = manifest
	emit(deps, Event{Stage: "verify", Status: "complete", Detail: "sandbox verification passed"})

	return result, nil
}

type projectKeys struct {
	server             string
	checkout           string
	previousServer     string
	previousCheckout   string
	existingServerID   string
	existingCheckoutID string
}

func inspectProjectKeys(plan Plan) (projectKeys, error) {
	keys := projectKeys{
		server:             scaffold.ReadEnvValue(plan.Project, "REEVIT_API_KEY"),
		checkout:           scaffold.ReadEnvValue(plan.Project, scaffold.ClientKeyVar(plan.Project.Stack)),
		existingServerID:   plan.Manifest.ServerKeyID,
		existingCheckoutID: plan.Manifest.CheckoutKeyID,
	}
	if !plan.RotateCredentials && keys.server != "" && plan.Manifest.ServerKeyID != "" &&
		credentialID(keys.server) != plan.Manifest.ServerKeyID {
		if plan.Manifest.Status == "pending" {
			keys.existingServerID = credentialID(keys.server)
		} else {
			return keys, fmt.Errorf(
				"%s REEVIT_API_KEY does not match project credential %s; restore the matching key or rerun with `--rotate-test-keys`",
				scaffold.EnvFileName(plan.Project), plan.Manifest.ServerKeyID,
			)
		}
	}
	if keys.server != "" && plan.Manifest.ServerKeyID == "" {
		if plan.Manifest.Status == "pending" && plan.Manifest.ProjectID != "" {
			keys.existingServerID = credentialID(keys.server)
		} else if keys.server != plan.LoginKey {
			return keys, fmt.Errorf(
				"%s contains an unmanaged REEVIT_API_KEY; move or remove it before init so Reevit does not overwrite an unknown credential",
				scaffold.EnvFileName(plan.Project),
			)
		} else {
			keys.previousServer, keys.server = keys.server, ""
		}
	}
	checkoutVar := scaffold.ClientKeyVar(plan.Project.Stack)
	if !plan.RotateCredentials && keys.checkout != "" && plan.Manifest.CheckoutKeyID != "" &&
		credentialID(keys.checkout) != plan.Manifest.CheckoutKeyID {
		if plan.Manifest.Status == "pending" {
			keys.existingCheckoutID = credentialID(keys.checkout)
		} else {
			return keys, fmt.Errorf(
				"%s %s does not match project credential %s; restore the matching key or rerun with `--rotate-test-keys`",
				scaffold.EnvFileName(plan.Project), checkoutVar, plan.Manifest.CheckoutKeyID,
			)
		}
	}
	if keys.checkout != "" && plan.Manifest.CheckoutKeyID == "" {
		if plan.Manifest.Status == "pending" && plan.Manifest.ProjectID != "" {
			keys.existingCheckoutID = credentialID(keys.checkout)
		} else if keys.checkout != plan.LoginKey {
			return keys, fmt.Errorf(
				"%s contains an unmanaged %s; move or remove it before init so Reevit does not overwrite an unknown credential",
				scaffold.EnvFileName(plan.Project), checkoutVar,
			)
		} else {
			keys.previousCheckout, keys.checkout = keys.checkout, ""
		}
	}
	if keys.server == "" {
		keys.existingServerID = ""
	}
	if keys.checkout == "" {
		keys.existingCheckoutID = ""
	}
	return keys, nil
}

func credentialID(raw string) string {
	id, _, _ := strings.Cut(strings.TrimSpace(raw), ".")
	return id
}

func (keys *projectKeys) mergeBootstrap(manifest *scaffold.Manifest, bootstrap api.BootstrapResult) {
	if credential := bootstrap.Credentials.Server; credential != nil {
		if credential.ID != manifest.ServerKeyID && credential.Raw != "" && keys.server != "" {
			keys.previousServer = keys.server
		}
		manifest.ServerKeyID = credential.ID
		if credential.Raw != "" {
			keys.server = credential.Raw
		}
	}
	if credential := bootstrap.Credentials.Checkout; credential != nil {
		if credential.ID != manifest.CheckoutKeyID && credential.Raw != "" && keys.checkout != "" {
			keys.previousCheckout = keys.checkout
		}
		manifest.CheckoutKeyID = credential.ID
		if credential.Raw != "" {
			keys.checkout = credential.Raw
		}
	}
}

func requireProjectKeys(targets []scaffold.Target, keys projectKeys) error {
	if hasTarget(targets, scaffold.TargetClient) && keys.server == "" {
		return fmt.Errorf("platform did not return a usable server credential; rerun with `--rotate-test-keys` if the local key was lost")
	}
	if hasTarget(targets, scaffold.TargetCheckout) && keys.checkout == "" {
		return fmt.Errorf("platform did not return a usable checkout credential; rerun with `--rotate-test-keys` if the local key was lost")
	}
	return nil
}

func installCommands(project scaffold.Project, targets []scaffold.Target) ([][]string, [][]string) {
	run := scaffold.NpmInstallPlans(project, targets)
	otherRun, show := scaffold.OtherInstallCmds(targets)
	return append(run, otherRun...), show
}

func preflightExecutables(commands [][]string) error {
	var missing []string
	for _, command := range commands {
		if len(command) == 0 {
			continue
		}
		if _, err := exec.LookPath(command[0]); err != nil {
			missing = append(missing, command[0])
		}
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	if len(missing) > 0 {
		return fmt.Errorf("missing required executable(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func capabilities(targets []scaffold.Target) []string {
	var result []string
	if hasTarget(targets, scaffold.TargetClient) {
		result = append(result, "server")
	}
	if hasTarget(targets, scaffold.TargetCheckout) {
		result = append(result, "checkout")
	}
	return result
}

func manifestCapabilities(targets []scaffold.Target) []string {
	var result []string
	if hasTarget(targets, scaffold.TargetWebhook) {
		result = append(result, "webhook")
	}
	if hasTarget(targets, scaffold.TargetClient) {
		result = append(result, "server")
	}
	if hasTarget(targets, scaffold.TargetCheckout) {
		result = append(result, "checkout")
	}
	return result
}

func reconcileGeneratedFiles(previous []string, results []scaffold.FileResult) []string {
	owned := make(map[string]bool, len(previous)+len(results))
	for _, path := range previous {
		owned[filepath.ToSlash(filepath.Clean(path))] = true
	}
	for _, result := range results {
		if result.ManagedEdit {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(result.Path))
		switch {
		case result.Removed:
			delete(owned, path)
		case !result.Skipped:
			owned[path] = true
		}
	}
	files := make([]string, 0, len(owned))
	for path := range owned {
		files = append(files, path)
	}
	slices.Sort(files)
	return files
}

func reconcileGeneratedEdits(previous []string, results []scaffold.FileResult) []string {
	owned := make(map[string]bool, len(previous)+len(results))
	for _, path := range previous {
		owned[filepath.ToSlash(filepath.Clean(path))] = true
	}
	for _, result := range results {
		if !result.ManagedEdit {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(result.Path))
		if result.Removed {
			delete(owned, path)
		} else {
			owned[path] = true
		}
	}
	edits := make([]string, 0, len(owned))
	for path := range owned {
		edits = append(edits, path)
	}
	slices.Sort(edits)
	return edits
}

func hasTarget(targets []scaffold.Target, key scaffold.TargetKey) bool {
	for _, target := range targets {
		if target.Key == key {
			return true
		}
	}
	return false
}

func markIncomplete(project scaffold.Project, manifest scaffold.Manifest, cause error) error {
	manifest.Status = "incomplete"
	if err := scaffold.WriteManifest(project, manifest); err != nil {
		return fmt.Errorf("%w (also failed to record incomplete setup: %v)", cause, err)
	}
	return cause
}

func validateDependencies(deps Dependencies) error {
	switch {
	case deps.Bootstrapper == nil:
		return fmt.Errorf("setup bootstrapper is required")
	case deps.Runner == nil:
		return fmt.Errorf("setup runner is required")
	case deps.Writer == nil:
		return fmt.Errorf("setup writer is required")
	case deps.Secrets == nil:
		return fmt.Errorf("setup secret generator is required")
	case deps.Verifier == nil:
		return fmt.Errorf("setup verifier is required")
	default:
		return nil
	}
}

func emit(deps Dependencies, event Event) {
	if deps.Emit != nil {
		deps.Emit(event)
	}
}

type FileWriter struct{}

func (FileWriter) WriteEnv(
	project scaffold.Project,
	credentials scaffold.ProjectCredentials,
) (scaffold.EnvResult, error) {
	return scaffold.WriteEnv(project, credentials)
}

func (FileWriter) Apply(
	project scaffold.Project,
	targets []scaffold.Target,
	options scaffold.ApplyOptions,
) ([]scaffold.FileResult, error) {
	return scaffold.Apply(project, targets, options)
}

type CryptoSecretGenerator struct{}

func (CryptoSecretGenerator) WebhookSigningSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate webhook signing secret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(raw), nil
}

type SandboxVerifier struct{}

func (SandboxVerifier) Verify(
	ctx context.Context,
	baseURL string,
	targets []scaffold.Target,
	serverKey string,
	checkoutKey string,
) error {
	if hasTarget(targets, scaffold.TargetCheckout) {
		if _, err := sandbox.VerifyCheckout(ctx, baseURL, checkoutKey); err != nil {
			return err
		}
	}
	if hasTarget(targets, scaffold.TargetClient) {
		result, err := sandbox.VerifyServerPayment(ctx, baseURL, serverKey)
		if err != nil {
			return err
		}
		if result.PaymentState != "" && result.PaymentState != "succeeded" {
			return fmt.Errorf("verify server payment: expected succeeded, got %s", result.PaymentState)
		}
	}
	return nil
}

type CommandRunner struct {
	Output   io.Writer
	KeepLogs bool
}

func (runner CommandRunner) Run(
	ctx context.Context,
	root string,
	command []string,
	verbose bool,
) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("empty installer command")
	}
	install := exec.CommandContext(ctx, command[0], command[1:]...)
	install.Dir = root
	raw, err := install.CombinedOutput()
	if verbose && len(raw) > 0 && runner.Output != nil {
		_, _ = runner.Output.Write(raw)
	}
	if err == nil && !runner.KeepLogs {
		return "", nil
	}
	logPath, logErr := writeLog(root, raw)
	if logErr != nil {
		if err != nil {
			return "", fmt.Errorf("%w (also failed to write log: %v)", err, logErr)
		}
		return "", logErr
	}
	return logPath, err
}

func writeLog(root string, raw []byte) (string, error) {
	logDir := filepath.Join(root, ".reevit", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("create log directory: %w", err)
	}
	name := "init-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".log"
	path := filepath.Join(logDir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write log: %w", err)
	}
	return filepath.ToSlash(filepath.Join(".reevit", "logs", name)), nil
}
