package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/scaffold"
	"github.com/Reevit-Platform/cli/internal/setup"
	"github.com/Reevit-Platform/cli/internal/telemetry"
	"github.com/Reevit-Platform/cli/internal/ui"
)

var (
	initTargets         []string
	initYes             bool
	initDryRun          bool
	initWebhookPath     string
	initCheckoutPath    string
	initCheckoutPage    string
	initCheckoutFields  []string
	initCheckoutMeta    []string
	initClientPath      string
	initRegisterWebhook string
	initRotateTestKeys  bool
	initOverwrite       bool
	initFresh           bool
	initVerbose         bool
	initGoal            string
	initOrigin          string
	initKeepLogs        bool
	initAccessible      bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up Reevit in the current project",
	Long: `Detects your stack, logs you in if needed (browser pairing, test-mode
key), installs the matching Reevit SDK, wires REEVIT_* environment variables,
and writes integration starter files — a webhook handler, a checkout
component, or a server-side client, depending on the project.

Existing files are preserved by default. Interactive setup can replace
generated integration files after creating a backup. Checkout can optionally
be inserted into an existing page using an idempotent marked block.`,
	Example: `  reevit init
  reevit init --goal checkout      # scaffold the checkout page, not just the client
  reevit init --overwrite          # replace generated files, keeping backups`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Setup narrates on stderr; only the --dry-run plan is data.
		sty := styleOf(cmd)
		out := cmd.ErrOrStderr()

		// Detect before authentication so unsupported projects never cause a
		// login or any other mutation.
		root, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}

		project := scaffold.Detect(root)
		if project.Stack == scaffold.StackUnknown {
			return fmt.Errorf(
				"couldn't detect a supported project here — use Next.js, React, Nuxt, Vue, SvelteKit, Svelte, Express/Node, Go modules, FastAPI, Flask, Django, Laravel/Composer, or Python with pyproject.toml, requirements.txt, setup.py, or Pipfile",
			)
		}
		if strings.TrimSpace(initOrigin) != "" {
			if err := validateInitOrigin(initOrigin); err != nil {
				return err
			}
		}
		if err := validateInitGoal(initGoal); err != nil {
			return err
		}
		if initOverwrite && initFresh {
			return fmt.Errorf("--overwrite and --fresh cannot be used together")
		}

		printDetectedProject(out, sty.err, project)

		telemetry.SetContext(string(project.Stack), nil)

		available := setup.RecommendedTargets(project)
		if !initYes && !initDryRun && len(initTargets) == 0 && !cmd.Flags().Changed("goal") && !isInteractiveInput(cmd.InOrStdin()) {
			return fmt.Errorf("interactive setup needs a terminal — rerun with `reevit init --yes` (recommended) or an explicit --goal/--target")
		}

		var cfg config.Config
		if !initDryRun {
			didLogin := false
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			if cfg.APIKey == "" {
				fmt.Fprintln(out, "\nSign in to Reevit to connect this project.")
				if err := browserLogin(cmd, true); err != nil {
					return err
				}
				didLogin = true
				if cfg, err = config.Load(); err != nil {
					return err
				}
			}
			if !didLogin {
				accountName, accountID := cfg.OrgName, cfg.OrgID
				if account, accountErr := api.New(cfg).AccountSummary(
					cmd.Context(),
				); accountErr == nil {
					accountName = account.OrganizationName
					accountID = account.OrganizationID
				}
				switch {
				case accountName != "":
					fmt.Fprintf(out, "\n%s\n", sty.err.Success("Signed in to "+accountName))
				case accountID != "":
					fmt.Fprintf(out, "\n%s\n", sty.err.Success("Signed in to organization "+accountID))
				default:
					fmt.Fprintf(out, "\n%s\n", sty.err.Success(fmt.Sprintf("Signed in to Reevit (%s mode)", cfg.Mode)))
				}
			}
		}

		// Resolve the adapter's complete recommendation.
		targets, err := pickTargets(cmd, available)
		if err != nil {
			return err
		}

		if len(targets) == 0 {
			return fmt.Errorf("nothing selected — nothing to do")
		}

		applyPathOverrides(targets)
		if err := configureCheckout(
			cmd,
			project,
			targets,
			!initYes && !initDryRun && isInteractiveInput(cmd.InOrStdin()),
		); err != nil {
			return err
		}

		chosen := make([]string, len(targets))
		for i, t := range targets {
			chosen[i] = string(t.Key)
		}

		telemetry.SetContext(string(project.Stack), chosen)

		manifest, err := scaffold.ReadManifest(project)
		if err != nil {
			return err
		}

		origin := ""
		if hasTarget(targets, scaffold.TargetCheckout) {
			origin = localOrigin(project)
		}
		if strings.TrimSpace(initOrigin) != "" {
			origin = strings.TrimRight(strings.TrimSpace(initOrigin), "/")
		} else if !initYes && !initDryRun && isInteractiveInput(cmd.InOrStdin()) &&
			hasTarget(targets, scaffold.TargetCheckout) {
			origin, err = ui.PromptOrigin(
				cmd.Context(), cmd.InOrStdin(), out, origin,
				ui.Accessible(initAccessible), validateInitOrigin,
			)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return ExitError{Code: 130, Err: err}
				}
				return err
			}
		}

		resolved, err := setup.Resolve(setup.ResolveInput{
			Project: project, Goal: setup.Goal(initGoal), Targets: targets,
			LocalOrigin: origin, Manifest: manifest,
		})
		if err != nil {
			return err
		}

		hasExistingSetup := manifest.ProjectID != "" || len(manifest.GeneratedFiles) > 0
		existingFiles := scaffold.ExistingFilesReject
		rotateCredentials := initRotateTestKeys
		switch {
		case initFresh:
			existingFiles = scaffold.ExistingFilesFresh
			rotateCredentials = true
		case initOverwrite:
			existingFiles = scaffold.ExistingFilesOverwrite
		case !initDryRun && !initYes && isInteractiveInput(cmd.InOrStdin()) &&
			(hasExistingSetup || len(resolved.Conflicts) > 0):
			action, promptErr := ui.ResolveExistingSetup(
				cmd.Context(),
				cmd.InOrStdin(),
				out,
				ui.Accessible(initAccessible),
				resolved.Conflicts,
				hasExistingSetup,
			)
			if promptErr != nil {
				if errors.Is(promptErr, ui.ErrCancelled) {
					return ExitError{Code: 130, Err: promptErr}
				}
				return promptErr
			}
			switch action {
			case ui.ExistingSetupKeep:
				existingFiles = scaffold.ExistingFilesKeep
			case ui.ExistingSetupOverwrite:
				existingFiles = scaffold.ExistingFilesOverwrite
			case ui.ExistingSetupFresh:
				existingFiles = scaffold.ExistingFilesFresh
				rotateCredentials = true
			}
		case len(resolved.Conflicts) > 0:
			return &scaffold.ConflictError{Paths: resolved.Conflicts}
		}
		configureExistingSetupPlan(&resolved, existingFiles, rotateCredentials)
		if initDryRun {
			return printPlan(cmd.OutOrStdout(), sty.out, resolved)
		}
		if err := printMutationPlan(out, sty.err, resolved); err != nil {
			return err
		}

		if !initYes {
			if confirmErr := ui.ConfirmApply(
				cmd.Context(), cmd.InOrStdin(), out, ui.Accessible(initAccessible),
			); confirmErr != nil {
				if errors.Is(confirmErr, ui.ErrCancelled) {
					return ExitError{Code: 130, Err: confirmErr}
				}
				return confirmErr
			}
		}

		client := api.New(cfg)
		resolved.CLIVersion = Version
		resolved.LoginKey = cfg.APIKey
		resolved.BaseURL = cfg.BaseURL
		resolved.Verbose = initVerbose
		result, err := setup.Apply(cmd.Context(), resolved, setup.Dependencies{
			Bootstrapper: client,
			Runner: setup.CommandRunner{
				Output: out, KeepLogs: initKeepLogs,
			},
			Writer:   setup.FileWriter{},
			Secrets:  setup.CryptoSecretGenerator{},
			Verifier: setup.SandboxVerifier{},
			Emit: func(event setup.Event) {
				printSetupEvent(out, sty.err, event)
			},
		})
		if err != nil {
			return err
		}

		// --- 7. Summary + next steps ---
		// The file list is the answer to "what did that just do to my repo?",
		// so it gets a heading of its own rather than trailing the progress
		// lines as an unlabelled block.
		fmt.Fprintln(out, sty.err.Heading("Files"))

		if hasTarget(targets, scaffold.TargetClient) {
			if result.Env.KeyAlreadySet {
				fmt.Fprintln(out, sty.err.Note(result.Env.EnvFile+" already had REEVIT_API_KEY — left untouched"))
			} else {
				fmt.Fprintln(out, sty.err.Success(result.Env.EnvFile+" — REEVIT_API_KEY (test mode) + REEVIT_ORG_ID"))
			}
		} else {
			fmt.Fprintln(out, sty.err.Success(result.Env.EnvFile+" — REEVIT_ORG_ID"))
		}

		if result.Env.EnvExample != "" {
			fmt.Fprintln(out, sty.err.Success(result.Env.EnvExample+" — placeholders added"))
		}

		if result.Env.ClientKeyVar != "" {
			fmt.Fprintln(out, sty.err.Success(fmt.Sprintf(
				"%s — %s (browser-exposed, test mode)", result.Env.EnvFile, result.Env.ClientKeyVar,
			)))
		}

		if result.Env.GitignoreNoted {
			fmt.Fprintln(out, sty.err.Success(".gitignore — "+result.Env.EnvFile+" added"))
		}

		for _, f := range result.Files {
			if f.Removed {
				fmt.Fprintln(out, sty.err.Note(f.Path+" — removed stale generated file"))
				printBackup(out, sty.err, f.BackupPath)
			} else if f.Skipped {
				fmt.Fprintln(out, sty.err.Note(f.Path+" exists — skipped"))
			} else {
				fmt.Fprintln(out, sty.err.Success(f.Path))
				printBackup(out, sty.err, f.BackupPath)
			}
		}

		for _, plan := range result.ShowCmds {
			fmt.Fprintf(out, "\nInstall the SDK in your environment:\n%s\n", sty.err.Command(strings.Join(plan, " ")))
		}

		registerWebhookEndpoint(cmd, out, sty.err, targets)

		printNextSteps(out, sty.err, project, targets)

		return nil
	},
}

// printBackup notes where the previous contents of an overwritten file went.
// It is reassurance, not news — dim and indented under the file it belongs to,
// so scanning the list for what changed is not interrupted by paths.
func printBackup(out io.Writer, sty ui.Styler, path string) {
	if path == "" {
		return
	}

	fmt.Fprintln(out, "  "+sty.Dim("backup: "+path))
}

// printSetupEvent narrates setup.Apply. Every "running" line it prints is
// answered by a "complete" line: an install of a large dependency tree can sit
// for a minute, and a `→ Installing dependencies (pnpm)…` with nothing after it
// is indistinguishable from a hang. There is no spinner and no cursor
// rewriting — a scrollback that reads correctly in a CI log is worth more than
// an animation, and stderr here is frequently redirected.
func printSetupEvent(out io.Writer, sty ui.Styler, event setup.Event) {
	switch {
	case event.Stage == "bootstrap" && event.Status == "running":
		fmt.Fprintln(out)
		fmt.Fprintln(out, sty.Step("Configuring Reevit test mode…"))
	case event.Stage == "bootstrap" && event.Status == "complete":
		fmt.Fprintln(out, sty.Success("Reevit test mode configured"))
	case event.Stage == "install" && event.Status == "running":
		fmt.Fprintf(out, "%s\n", sty.Step(fmt.Sprintf(
			"Installing dependencies (%s)…", installerName(event.Detail))))
	case event.Stage == "install" && event.Status == "complete":
		fmt.Fprintln(out, sty.Success(fmt.Sprintf(
			"Dependencies installed (%s)", installerName(event.Detail))))

		if event.LogPath != "" {
			fmt.Fprintln(out, "  "+sty.Note("log retained at "+event.LogPath))
		}
	case event.Stage == "verify" && event.Status == "running":
		fmt.Fprintln(out, sty.Step("Verifying project credentials against the sandbox…"))
	case event.Stage == "verify" && event.Status == "complete":
		fmt.Fprintln(out, sty.Success("Project credentials verified against the sandbox"))
	}
}

// installerName reduces an install command to the tool that runs it. The event
// Detail is the whole argv ("pnpm add @reevit/node"), which is more than the
// progress line needs and long enough to wrap on a narrow terminal.
func installerName(detail string) string {
	if fields := strings.Fields(detail); len(fields) > 0 {
		return fields[0]
	}

	return detail
}

// pickTargets resolves --target flags or prompts interactively.
func pickTargets(cmd *cobra.Command, available []scaffold.Target) ([]scaffold.Target, error) {
	if len(available) == 0 {
		return nil, fmt.Errorf("no Reevit integrations available for this stack yet")
	}

	if len(initTargets) > 0 {
		byKey := map[string]scaffold.Target{}
		for _, t := range available {
			byKey[string(t.Key)] = t
		}

		var picked []scaffold.Target

		for _, key := range initTargets {
			t, ok := byKey[strings.TrimSpace(key)]
			if !ok {
				return nil, fmt.Errorf("unknown --target %q — available: %s", key, availableKeys(available))
			}

			picked = append(picked, t)
		}

		return picked, nil
	}

	if initGoal != "" && initGoal != "auto" {
		key := map[string]scaffold.TargetKey{
			"checkout": scaffold.TargetCheckout,
			"webhook":  scaffold.TargetWebhook,
			"server":   scaffold.TargetClient,
		}[initGoal]
		if initGoal == "full" {
			return available, nil
		}
		for _, target := range available {
			if target.Key == key {
				return []scaffold.Target{target}, nil
			}
		}
		return nil, fmt.Errorf("--goal %s is not available for this project", initGoal)
	}

	if initYes || initDryRun || len(available) == 1 {
		return available, nil
	}

	sty := styleOf(cmd).err
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, sty.Heading("Recommended setup:"))
	for _, target := range available {
		fmt.Fprintln(out, "  "+sty.Success(target.Label))
	}
	picked, err := ui.Customize(
		cmd.Context(), cmd.InOrStdin(), out,
		available, ui.Accessible(initAccessible),
	)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			return nil, ExitError{Code: 130, Err: err}
		}
		return nil, err
	}

	return picked, nil
}

func validateInitGoal(goal string) error {
	if slices.Contains([]string{"", "auto", "full", "checkout", "webhook", "server"}, goal) {
		return nil
	}

	return fmt.Errorf("invalid --goal %q — use auto, full, checkout, webhook, or server", goal)
}

func isInteractiveInput(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func validateInitOrigin(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("invalid --origin: provide an origin only, such as http://localhost:3000")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" && host != "localhost" && host != "127.0.0.1" {
		return fmt.Errorf("invalid --origin: HTTP is allowed only for localhost; use HTTPS otherwise")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid --origin: scheme must be http or https")
	}
	return nil
}

func localOrigin(project scaffold.Project) string {
	if port := scaffold.DefaultPort(project); port != 0 {
		return fmt.Sprintf("http://localhost:%d", port)
	}

	return ""
}

func printDetectedProject(out io.Writer, sty ui.Styler, project scaffold.Project) {
	parts := []string{displayFramework(project)}
	if project.TypeScript {
		parts = append(parts, "TypeScript")
	} else if project.Stack == scaffold.StackNext || project.Stack == scaffold.StackReact ||
		project.Stack == scaffold.StackVue || project.Stack == scaffold.StackSvelte ||
		project.Stack == scaffold.StackNode {
		parts = append(parts, "JavaScript")
	}
	if project.Stack == scaffold.StackNext {
		if project.NextRouter == scaffold.NextRouterPages {
			parts = append(parts, "Pages Router")
		} else {
			parts = append(parts, "App Router")
		}
	}
	installer := string(project.Installer)
	if installer == "" {
		installer = string(project.Manager)
	}
	if installer != "" {
		parts = append(parts, installer)
	}
	fmt.Fprintf(out, "%s\n\nFound %s\n", sty.Heading("Reevit setup"), strings.Join(parts, " · "))
}

func displayFramework(project scaffold.Project) string {
	switch project.Framework {
	case scaffold.FrameworkNext:
		return "Next.js"
	case scaffold.FrameworkSvelteKit:
		return "SvelteKit"
	case scaffold.FrameworkFastAPI:
		return "FastAPI"
	default:
		if project.Framework != "" && project.Framework != scaffold.FrameworkGeneric {
			name := string(project.Framework)
			return strings.ToUpper(name[:1]) + name[1:]
		}
		name := string(project.Stack)
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// applyPathOverrides remaps each target's single output file when the
// developer specified where the code should live.
func applyPathOverrides(targets []scaffold.Target) {
	overrides := map[scaffold.TargetKey]string{
		scaffold.TargetWebhook:  initWebhookPath,
		scaffold.TargetCheckout: initCheckoutPath,
		scaffold.TargetClient:   initClientPath,
	}

	for i := range targets {
		override := strings.TrimSpace(overrides[targets[i].Key])
		if override == "" {
			continue
		}

		remapped := make(map[string]string, len(targets[i].Files))
		overrideTemplate := customizableTemplate(targets[i])
		for tmpl, path := range targets[i].Files {
			if tmpl == overrideTemplate {
				path = override
			}
			remapped[tmpl] = path
		}

		targets[i].Files = remapped
	}
}

func customizableTemplate(target scaffold.Target) string {
	if len(target.Files) == 1 {
		for name := range target.Files {
			return name
		}
	}
	if target.Key == scaffold.TargetCheckout {
		for name := range target.Files {
			if strings.Contains(name, "checkout") {
				return name
			}
		}
	}
	if target.Key == scaffold.TargetClient {
		for name := range target.Files {
			if strings.Contains(name, "client") {
				return name
			}
		}
	}
	return ""
}

func configureCheckout(
	cmd *cobra.Command,
	project scaffold.Project,
	targets []scaffold.Target,
	interactive bool,
) error {
	checkoutIndex := -1
	for i := range targets {
		if targets[i].Key == scaffold.TargetCheckout {
			checkoutIndex = i
			break
		}
	}
	pageFlagSet := strings.TrimSpace(initCheckoutPage) != ""
	fieldsFlagSet := len(initCheckoutFields) > 0
	metadataFlagSet := len(initCheckoutMeta) > 0
	hasFlags := pageFlagSet || fieldsFlagSet || metadataFlagSet
	if checkoutIndex < 0 {
		if hasFlags {
			return fmt.Errorf("--checkout-page, --checkout-fields, and --checkout-metadata require a checkout target")
		}
		return nil
	}
	if !interactive && !hasFlags {
		return nil
	}

	page := strings.TrimSpace(initCheckoutPage)
	if page == "-" {
		page = ""
	}
	fieldValues := append([]string(nil), initCheckoutFields...)
	metadataValues := append([]string(nil), initCheckoutMeta...)

	if interactive && !pageFlagSet {
		candidates := scaffold.CheckoutPageCandidates(project)
		defaultPage := ""
		if len(candidates) > 0 {
			defaultPage = candidates[0]
		}
		value, err := promptString(
			cmd.ErrOrStderr(),
			cmd.InOrStdin(),
			"Which existing page should receive checkout? (Type - to keep the component standalone)",
			defaultPage,
		)
		if err != nil {
			return err
		}
		if value == "-" {
			page = ""
		} else {
			page = value
		}
	}

	if interactive && !fieldsFlagSet {
		labels := []string{
			"Amount / price",
			"Customer name",
			"Customer email",
			"Customer phone number",
			"Payment reference",
		}
		picks, err := choose(
			cmd.ErrOrStderr(),
			cmd.InOrStdin(),
			"What should the checkout form collect?",
			labels,
			true,
		)
		if err != nil {
			return err
		}
		allFields := []string{"amount", "name", "email", "phone", "reference"}
		fieldValues = fieldValues[:0]
		for _, pick := range picks {
			fieldValues = append(fieldValues, allFields[pick])
		}
	}

	if interactive && !metadataFlagSet {
		value, err := promptString(
			cmd.ErrOrStderr(),
			cmd.InOrStdin(),
			"Extra workflow metadata fields (comma-separated, e.g. order_id,product_sku; Enter for none):",
			"",
		)
		if err != nil {
			return err
		}
		if value != "" {
			metadataValues = strings.Split(value, ",")
		}
	}

	fields, err := scaffold.ParseCheckoutFields(splitCommaValues(fieldValues))
	if err != nil {
		return err
	}
	metadata, err := scaffold.ParseMetadataFields(splitCommaValues(metadataValues))
	if err != nil {
		return err
	}
	targets[checkoutIndex].Checkout = &scaffold.CheckoutOptions{
		PagePath: page, Fields: fields, MetadataFields: metadata,
	}
	return scaffold.ConfigureCheckoutTarget(project, &targets[checkoutIndex])
}

func splitCommaValues(values []string) []string {
	var split []string
	for _, value := range values {
		split = append(split, strings.Split(value, ",")...)
	}
	return split
}

// registerWebhookEndpoint optionally registers a production webhook endpoint
// in the dashboard. Needs webhooks:write — keys minted before that scope
// joined the pairing defaults get a pointer to re-login instead of an error.
func registerWebhookEndpoint(
	cmd *cobra.Command,
	out io.Writer,
	sty ui.Styler,
	targets []scaffold.Target,
) {
	if !hasTarget(targets, scaffold.TargetWebhook) {
		return
	}

	endpoint := strings.TrimSpace(initRegisterWebhook)

	if endpoint == "" {
		if initYes || len(initTargets) > 0 {
			return // non-interactive without the flag → skip silently
		}

		yes, err := confirm(out, cmd.InOrStdin(), "\nRegister a production webhook endpoint in your dashboard now?", false)
		if err != nil || !yes {
			return
		}

		endpoint, err = promptString(out, cmd.InOrStdin(), "Endpoint URL (https://…):", "")
		if err != nil || endpoint == "" {
			return
		}
	}

	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		fmt.Fprintln(out, sty.Note(fmt.Sprintf("skipping webhook registration — %q is not a URL", endpoint)))

		return
	}

	c, err := client()
	if err != nil {
		fmt.Fprintln(out, sty.Note(fmt.Sprintf("skipping webhook registration — %v", err)))

		return
	}

	err = c.Do(cmd.Context(), api.Request{
		Method:     "POST",
		Path:       "/webhooks/config",
		Idempotent: true,
		Body:       map[string]any{"url": endpoint},
	}, nil)

	switch {
	case err == nil:
		fmt.Fprintln(out, sty.Success("webhook endpoint registered: "+endpoint))
	default:
		if apiErr, ok := err.(*api.APIError); ok && apiErr.Status == 403 {
			fmt.Fprintln(out, sty.Note("couldn't register the endpoint — your CLI key lacks webhooks:write."))
			fmt.Fprintln(out, "  Run `reevit login` again for a fresh key, or register it in Dashboard → Developers → Webhooks.")

			return
		}

		fmt.Fprintln(out, sty.Note(fmt.Sprintf(
			"webhook registration failed (%v) — you can register it in Dashboard → Developers → Webhooks", err,
		)))
	}
}

func configureExistingSetupPlan(
	plan *setup.Plan,
	policy scaffold.ExistingFilesPolicy,
	rotateCredentials bool,
) {
	plan.ExistingFiles = policy
	plan.RotateCredentials = rotateCredentials
	switch policy {
	case scaffold.ExistingFilesKeep:
		plan.Warnings = append(
			plan.Warnings,
			"existing integration files will be kept; only missing outputs will be created",
		)
	case scaffold.ExistingFilesOverwrite:
		plan.Operations = append(plan.Operations, setup.Operation{
			Kind:   setup.WriteFile,
			Detail: "back up and replace existing generated integration files",
			Reason: "apply the developer's explicit overwrite choice recoverably",
		})
	case scaffold.ExistingFilesFresh:
		plan.Operations = append(plan.Operations, setup.Operation{
			Kind:   setup.WriteFile,
			Detail: "back up prior generated files, remove stale outputs, and regenerate the selection",
			Reason: "start the local Reevit integration from a clean generated state",
		})
	}
	if rotateCredentials {
		plan.Operations = append(plan.Operations, setup.Operation{
			Kind:   setup.BootstrapPlatform,
			Detail: "rotate project test credentials",
			Reason: "replace the project's managed test credentials explicitly",
		})
	}
}

func printPlan(out io.Writer, sty ui.Styler, plan setup.Plan) error {
	fmt.Fprintln(out, sty.Heading("Dry run — would do the following:"))
	return printPlanOperations(out, sty, plan)
}

func printMutationPlan(out io.Writer, sty ui.Styler, plan setup.Plan) error {
	fmt.Fprintln(out, sty.Heading("Setup plan:"))
	return printPlanOperations(out, sty, plan)
}

func printPlanOperations(out io.Writer, sty ui.Styler, plan setup.Plan) error {
	for _, warning := range plan.Warnings {
		fmt.Fprintln(out, "  "+sty.Warning(warning))
	}
	for _, operation := range plan.Operations {
		fmt.Fprintf(out, "  %s\n    %s\n", sty.Step(operation.Detail), sty.Dim(operation.Reason))
	}
	return nil
}

func printNextSteps(
	out io.Writer,
	sty ui.Styler,
	project scaffold.Project,
	targets []scaffold.Target,
) {
	fmt.Fprintln(out, sty.Heading("Next"))

	if command := scaffold.DevCommand(project); len(command) > 0 {
		fmt.Fprintf(out, "  1. Start your app:\n%s\n", sty.Command(strings.Join(command, " ")))
	}
	if path, port := scaffold.DemoPath(project), scaffold.DefaultPort(project); path != "" && port != 0 {
		fmt.Fprintf(out, "  2. Open the runnable checkout: %s\n", sty.URL(fmt.Sprintf("http://localhost:%d%s", port, path)))
	}

	for _, t := range targets {
		switch t.Key {
		case scaffold.TargetWebhook:
			if instruction := scaffold.WebhookMountInstruction(project); instruction != "" {
				fmt.Fprintln(out, "  "+sty.Step("Mount the generated webhook: "+instruction))
			}
			path := "/<your webhook path>"
			if _, handlerPath := scaffold.WebhookHandler(project); handlerPath != "" {
				path = handlerPath
			}

			fmt.Fprintln(out, "  "+sty.Step("Forward signed test events to your webhook handler:"))
			port := scaffold.DefaultPort(project)
			if port == 0 {
				fmt.Fprintln(out, sty.Command("reevit listen --forward-to http://localhost:<port>"+path))
			} else {
				fmt.Fprintln(out, sty.Command(fmt.Sprintf(
					"reevit listen --forward-to http://localhost:%d%s", port, path,
				)))
			}
			fmt.Fprintln(out, "    It automatically uses REEVIT_WEBHOOK_SECRET from your project env.")
			fmt.Fprintln(out, "  "+sty.Step("Then verify the whole setup (signature check included):"))
			if port == 0 {
				fmt.Fprintln(out, sty.Command("reevit doctor --webhook-url http://localhost:<port>"+path))
			} else {
				fmt.Fprintln(out, sty.Command(fmt.Sprintf(
					"reevit doctor --webhook-url http://localhost:%d%s", port, path,
				)))
			}
		case scaffold.TargetCheckout:
			fmt.Fprintln(out, "  "+sty.Step("Render the checkout component with an amount in the smallest currency unit."))
		case scaffold.TargetClient:
			fmt.Fprintln(out, "  "+sty.Step("Run another real simulator payment whenever you need one:"))
			fmt.Fprintln(out, sty.Command("reevit trigger payment.succeeded"))
			fmt.Fprintln(out, "    Then inspect it with `reevit payments list`.")
		}
	}

	if !hasTarget(targets, scaffold.TargetWebhook) {
		fmt.Fprintln(out, "  "+sty.Step("Run `reevit doctor` any time to check the setup."))
	}

	// The live-key instructions used to close init as a three-line paragraph,
	// which put reference material where the next command belongs. They now
	// live in `reevit login --help`; what stays here is the one fact the user
	// needs at this moment — that nothing they do next will move real money.
	fmt.Fprintln(out)
	fmt.Fprintln(out, sty.Note("You are in test mode. Live keys: reevit login --help"))
}

func hasTarget(targets []scaffold.Target, key scaffold.TargetKey) bool {
	for _, t := range targets {
		if t.Key == key {
			return true
		}
	}

	return false
}

func availableKeys(targets []scaffold.Target) string {
	keys := make([]string, len(targets))
	for i, t := range targets {
		keys[i] = string(t.Key)
	}

	return strings.Join(keys, ", ")
}

func init() {
	initCmd.Flags().StringSliceVar(&initTargets, "target", nil, "what to scaffold (webhook, checkout, client) — skips the prompt")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "scaffold everything available without prompting")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "print what would happen without writing anything")
	initCmd.Flags().StringVar(&initWebhookPath, "webhook-path", "", "custom output path for the webhook handler")
	initCmd.Flags().StringVar(&initCheckoutPath, "checkout-path", "", "custom output path for the checkout component")
	initCmd.Flags().StringVar(&initCheckoutPage, "checkout-page", "", "existing page where checkout should be added; use - for standalone")
	initCmd.Flags().StringSliceVar(&initCheckoutFields, "checkout-fields", nil, "fields to collect: amount (or price), name, email, phone, reference")
	initCmd.Flags().StringSliceVar(&initCheckoutMeta, "checkout-metadata", nil, "custom metadata fields to collect for payments and workflows")
	initCmd.Flags().StringVar(&initClientPath, "client-path", "", "custom output path for the server client")
	initCmd.Flags().StringVar(&initRegisterWebhook, "register-webhook", "", "register this production webhook endpoint in your dashboard")
	initCmd.Flags().BoolVar(&initRotateTestKeys, "rotate-test-keys", false, "replace project test credentials after local secrets were lost")
	initCmd.Flags().BoolVar(&initOverwrite, "overwrite", false, "replace generated integration files after backing them up")
	initCmd.Flags().BoolVar(&initFresh, "fresh", false, "replace generated integration files and rotate project test credentials")
	initCmd.Flags().BoolVar(&initVerbose, "verbose", false, "stream package-manager output")
	initCmd.Flags().StringVar(&initGoal, "goal", "auto", "setup goal: auto, full, checkout, webhook, or server")
	initCmd.Flags().StringVar(&initOrigin, "origin", "", "local checkout origin (defaults to the detected framework port)")
	initCmd.Flags().BoolVar(&initKeepLogs, "keep-logs", false, "keep successful setup logs")
	initCmd.Flags().BoolVar(&initAccessible, "accessible", false, "use screen-reader-friendly prompts")
	initCmd.MarkFlagsMutuallyExclusive("goal", "target")

	rootCmd.AddCommand(initCmd)
}
