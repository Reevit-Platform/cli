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
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()

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

		printDetectedProject(out, project)

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
					fmt.Fprintf(out, "\n✓ Signed in to %s\n", accountName)
				case accountID != "":
					fmt.Fprintf(out, "\n✓ Signed in to organization %s\n", accountID)
				default:
					fmt.Fprintf(out, "\n✓ Signed in to Reevit (%s mode)\n", cfg.Mode)
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
			return printPlan(out, resolved)
		}
		if err := printMutationPlan(out, resolved); err != nil {
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
				printSetupEvent(out, event)
			},
		})
		if err != nil {
			return err
		}

		// --- 7. Summary + next steps ---
		fmt.Fprintln(out)

		if hasTarget(targets, scaffold.TargetClient) {
			if result.Env.KeyAlreadySet {
				fmt.Fprintf(out, "• %s already had REEVIT_API_KEY — left untouched\n", result.Env.EnvFile)
			} else {
				fmt.Fprintf(out, "✔ %s — REEVIT_API_KEY (test mode) + REEVIT_ORG_ID\n", result.Env.EnvFile)
			}
		} else {
			fmt.Fprintf(out, "✔ %s — REEVIT_ORG_ID\n", result.Env.EnvFile)
		}

		if result.Env.EnvExample != "" {
			fmt.Fprintf(out, "✔ %s — placeholders added\n", result.Env.EnvExample)
		}

		if result.Env.ClientKeyVar != "" {
			fmt.Fprintf(out, "✔ %s — %s (browser-exposed, test mode)\n", result.Env.EnvFile, result.Env.ClientKeyVar)
		}

		if result.Env.GitignoreNoted {
			fmt.Fprintf(out, "✔ .gitignore — %s added\n", result.Env.EnvFile)
		}

		for _, f := range result.Files {
			if f.Removed {
				fmt.Fprintf(out, "− %s — removed stale generated file\n", f.Path)
				if f.BackupPath != "" {
					fmt.Fprintf(out, "  Backup: %s\n", f.BackupPath)
				}
			} else if f.Skipped {
				fmt.Fprintf(out, "• %s exists — skipped\n", f.Path)
			} else {
				fmt.Fprintf(out, "✔ %s\n", f.Path)
				if f.BackupPath != "" {
					fmt.Fprintf(out, "  Backup: %s\n", f.BackupPath)
				}
			}
		}

		for _, plan := range result.ShowCmds {
			fmt.Fprintf(out, "\nInstall the SDK in your environment:  %s\n", strings.Join(plan, " "))
		}

		registerWebhookEndpoint(cmd, out, targets)

		printNextSteps(out, project, targets)

		return nil
	},
}

func printSetupEvent(out io.Writer, event setup.Event) {
	switch {
	case event.Stage == "bootstrap" && event.Status == "running":
		fmt.Fprintln(out, "\nConfiguring Reevit test mode…")
	case event.Stage == "install" && event.Status == "running":
		fmt.Fprintf(out, "  Installing dependencies (%s)…\n", event.Detail)
	case event.Stage == "install" && event.Status == "complete" && event.LogPath != "":
		fmt.Fprintf(out, "    Log retained at %s\n", event.LogPath)
	case event.Stage == "verify" && event.Status == "running":
		fmt.Fprintln(out, "  Verifying project credentials against the sandbox…")
	}
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

	fmt.Fprintln(cmd.OutOrStdout(), "\nRecommended setup:")
	for _, target := range available {
		fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s\n", target.Label)
	}
	picked, err := ui.Customize(
		cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(),
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

func printDetectedProject(out io.Writer, project scaffold.Project) {
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
	fmt.Fprintf(out, "\nReevit setup\n\nFound %s\n", strings.Join(parts, " · "))
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
			cmd.OutOrStdout(),
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
			cmd.OutOrStdout(),
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
			cmd.OutOrStdout(),
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
func registerWebhookEndpoint(cmd *cobra.Command, out interface{ Write([]byte) (int, error) }, targets []scaffold.Target) {
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
		fmt.Fprintf(out, "• skipping webhook registration — %q is not a URL\n", endpoint)

		return
	}

	c, err := client()
	if err != nil {
		fmt.Fprintf(out, "• skipping webhook registration — %v\n", err)

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
		fmt.Fprintf(out, "✔ webhook endpoint registered: %s\n", endpoint)
	default:
		if apiErr, ok := err.(*api.APIError); ok && apiErr.Status == 403 {
			fmt.Fprintln(out, "• couldn't register the endpoint — your CLI key lacks webhooks:write.")
			fmt.Fprintln(out, "  Run `reevit login` again for a fresh key, or register it in Dashboard → Developers → Webhooks.")

			return
		}

		fmt.Fprintf(out, "• webhook registration failed (%v) — you can register it in Dashboard → Developers → Webhooks\n", err)
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

func printPlan(out io.Writer, plan setup.Plan) error {
	fmt.Fprintln(out, "\nDry run — would do the following:")
	return printPlanOperations(out, plan)
}

func printMutationPlan(out io.Writer, plan setup.Plan) error {
	fmt.Fprintln(out, "\nSetup plan:")
	return printPlanOperations(out, plan)
}

func printPlanOperations(out io.Writer, plan setup.Plan) error {
	for _, warning := range plan.Warnings {
		fmt.Fprintf(out, "  ! %s\n", warning)
	}
	for _, operation := range plan.Operations {
		prefix := "•"
		if operation.Kind == setup.WriteFile || operation.Kind == setup.WriteEnv {
			prefix = "+"
		}
		fmt.Fprintf(out, "  %s %s\n    %s\n", prefix, operation.Detail, operation.Reason)
	}
	return nil
}

func printNextSteps(out interface{ Write([]byte) (int, error) }, project scaffold.Project, targets []scaffold.Target) {
	fmt.Fprintln(out, "\nNext steps:")

	if command := scaffold.DevCommand(project); len(command) > 0 {
		fmt.Fprintf(out, "  1. Start your app: %s\n", strings.Join(command, " "))
	}
	if path, port := scaffold.DemoPath(project), scaffold.DefaultPort(project); path != "" && port != 0 {
		fmt.Fprintf(out, "  2. Open the runnable checkout: http://localhost:%d%s\n", port, path)
	}

	for _, t := range targets {
		switch t.Key {
		case scaffold.TargetWebhook:
			if instruction := scaffold.WebhookMountInstruction(project); instruction != "" {
				fmt.Fprintf(out, "  • Mount the generated webhook: %s\n", instruction)
			}
			path := "/<your webhook path>"
			if _, handlerPath := scaffold.WebhookHandler(project); handlerPath != "" {
				path = handlerPath
			}

			fmt.Fprintln(out, "  • Forward signed test events to your webhook handler:")
			port := scaffold.DefaultPort(project)
			if port == 0 {
				fmt.Fprintf(out, "      reevit listen --forward-to http://localhost:<port>%s\n", path)
			} else {
				fmt.Fprintf(out, "      reevit listen --forward-to http://localhost:%d%s\n", port, path)
			}
			fmt.Fprintln(out, "    It automatically uses REEVIT_WEBHOOK_SECRET from your project env.")
			fmt.Fprintln(out, "  • Then verify the whole setup (signature check included):")
			if port == 0 {
				fmt.Fprintf(out, "      reevit doctor --webhook-url http://localhost:<port>%s\n", path)
			} else {
				fmt.Fprintf(out, "      reevit doctor --webhook-url http://localhost:%d%s\n", port, path)
			}
		case scaffold.TargetCheckout:
			fmt.Fprintln(out, "  • Render the checkout component with an amount in the smallest currency unit.")
		case scaffold.TargetClient:
			fmt.Fprintln(out, "  • Run another real simulator payment whenever you need one:")
			fmt.Fprintln(out, "      reevit trigger payment.succeeded")
			fmt.Fprintln(out, "    Then inspect it with `reevit payments list`.")
		}
	}

	if !hasTarget(targets, scaffold.TargetWebhook) {
		fmt.Fprintln(out, "  • Run `reevit doctor` any time to check the setup.")
	}

	fmt.Fprintln(out, "\nYou're on a TEST-MODE key. When you're ready for live traffic, create a live")
	fmt.Fprintln(out, "key in Dashboard → Developers → API keys and run:  reevit login --key <live_key>")
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
