package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/scaffold"
	"github.com/Reevit-Platform/cli/internal/telemetry"
)

var (
	initTargets         []string
	initYes             bool
	initDryRun          bool
	initWebhookPath     string
	initCheckoutPath    string
	initClientPath      string
	initRegisterWebhook string
	initRotateTestKeys  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up Reevit in the current project",
	Long: `Detects your stack, logs you in if needed (browser pairing, test-mode
key), installs the matching Reevit SDK, wires REEVIT_* environment variables,
and writes integration starter files — a webhook handler, a checkout
component, or a server-side client, depending on the project.

Existing files and env values are never overwritten.`,
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
			return fmt.Errorf("couldn't detect a project here — run `reevit init` inside a project with a package.json, go.mod, composer.json, or pyproject.toml")
		}

		fmt.Fprintf(out, "\nDetected: %s project", project.Stack)

		if len(project.Managers) > 1 {
			fmt.Fprintf(out, " (lockfiles: %s — all will be kept in sync)", joinManagers(project.Managers))
		}

		fmt.Fprintln(out)

		telemetry.SetContext(string(project.Stack), nil)

		available := scaffold.TargetsFor(project)

		// Resolve the adapter's complete recommendation.
		targets, err := pickTargets(cmd, available)
		if err != nil {
			return err
		}

		if len(targets) == 0 {
			return fmt.Errorf("nothing selected — nothing to do")
		}

		applyPathOverrides(targets)

		chosen := make([]string, len(targets))
		for i, t := range targets {
			chosen[i] = string(t.Key)
		}

		telemetry.SetContext(string(project.Stack), chosen)

		if initDryRun {
			return printPlan(out, project, targets)
		}

		if !initYes {
			ok, confirmErr := confirm(out, cmd.InOrStdin(), "\nApply this recommended setup?", true)
			if confirmErr != nil {
				return confirmErr
			}
			if !ok {
				return fmt.Errorf("setup cancelled")
			}
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.APIKey == "" {
			fmt.Fprintln(out, "\nSign in to Reevit to connect this project.")
			if err := browserLogin(cmd, true); err != nil {
				return err
			}
			if cfg, err = config.Load(); err != nil {
				return err
			}
		}

		manifest, err := scaffold.ReadManifest(project)
		if err != nil {
			return err
		}
		if manifest.ProjectID == "" {
			manifest.ProjectID = "rvproj_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		manifest.Status = "pending"
		manifest.Adapter = string(project.Stack)
		manifest.Origin = localOrigin(project)
		if err := scaffold.WriteManifest(project, manifest); err != nil {
			return err
		}

		serverKey := scaffold.ReadEnvValue(project, "REEVIT_API_KEY")
		checkoutVar := scaffold.ClientKeyVar(project.Stack)
		checkoutKey := scaffold.ReadEnvValue(project, checkoutVar)
		request := api.BootstrapRequest{
			ProjectID:         manifest.ProjectID,
			ProjectName:       filepath.Base(project.Root),
			Capabilities:      bootstrapCapabilities(targets),
			Origin:            manifest.Origin,
			RotateCredentials: initRotateTestKeys,
		}
		if serverKey != "" {
			request.ExistingServerKeyID = manifest.ServerKeyID
		}
		if checkoutKey != "" {
			request.ExistingCheckoutKeyID = manifest.CheckoutKeyID
		}

		fmt.Fprintln(out, "\nConfiguring Reevit test mode…")
		bootstrap, err := api.New(cfg).BootstrapProject(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("configure Reevit project: %w", err)
		}
		if bootstrap.Credentials.Server != nil {
			manifest.ServerKeyID = bootstrap.Credentials.Server.ID
			if bootstrap.Credentials.Server.Raw != "" {
				serverKey = bootstrap.Credentials.Server.Raw
			}
		}
		if bootstrap.Credentials.Checkout != nil {
			manifest.CheckoutKeyID = bootstrap.Credentials.Checkout.ID
			if bootstrap.Credentials.Checkout.Raw != "" {
				checkoutKey = bootstrap.Credentials.Checkout.Raw
			}
		}

		webhookSecret := scaffold.ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET")
		if webhookSecret == "" && hasTarget(targets, scaffold.TargetWebhook) {
			webhookSecret = "whsec_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		envRes, err := scaffold.WriteEnv(project, scaffold.ProjectCredentials{
			ServerKey: serverKey, CheckoutKey: checkoutKey,
			OrgID: bootstrap.Project.OrganizationID, WebhookSecret: webhookSecret,
		})
		if err != nil {
			return err
		}

		// Install SDKs with output hidden only after project credentials are
		// safely persisted to the local env file.
		for _, plan := range scaffold.NpmInstallPlans(project, targets) {
			fmt.Fprintf(out, "\n$ %s\n", strings.Join(plan, " "))

			install := exec.CommandContext(cmd.Context(), plan[0], plan[1:]...)
			install.Dir = project.Root
			install.Stdout = out
			install.Stderr = cmd.ErrOrStderr()

			if err := install.Run(); err != nil {
				return fmt.Errorf("install failed (%s): %w", strings.Join(plan, " "), err)
			}
		}

		runCmds, showCmds := scaffold.OtherInstallCmds(targets)

		for _, plan := range runCmds {
			fmt.Fprintf(out, "\n$ %s\n", strings.Join(plan, " "))

			install := exec.CommandContext(cmd.Context(), plan[0], plan[1:]...)
			install.Dir = project.Root
			install.Stdout = out
			install.Stderr = cmd.ErrOrStderr()

			if err := install.Run(); err != nil {
				return fmt.Errorf("install failed (%s): %w", strings.Join(plan, " "), err)
			}
		}

		// Starter files.
		files, err := scaffold.Apply(project, targets)
		if err != nil {
			return err
		}
		manifest.Status = "complete"
		if err := scaffold.WriteManifest(project, manifest); err != nil {
			return err
		}

		// --- 7. Summary + next steps ---
		fmt.Fprintln(out)

		if envRes.KeyAlreadySet {
			fmt.Fprintf(out, "• %s already had REEVIT_API_KEY — left untouched\n", envRes.EnvFile)
		} else {
			fmt.Fprintf(out, "✔ %s — REEVIT_API_KEY (test mode) + REEVIT_ORG_ID\n", envRes.EnvFile)
		}

		if envRes.EnvExample != "" {
			fmt.Fprintf(out, "✔ %s — placeholders added\n", envRes.EnvExample)
		}

		if envRes.ClientKeyVar != "" {
			fmt.Fprintf(out, "✔ %s — %s (browser-exposed, test mode)\n", envRes.EnvFile, envRes.ClientKeyVar)
		}

		if envRes.GitignoreNoted {
			fmt.Fprintf(out, "✔ .gitignore — %s added\n", envRes.EnvFile)
		}

		for _, f := range files {
			if f.Skipped {
				fmt.Fprintf(out, "• %s exists — skipped\n", f.Path)
			} else {
				fmt.Fprintf(out, "✔ %s\n", f.Path)
			}
		}

		for _, plan := range showCmds {
			fmt.Fprintf(out, "\nInstall the SDK in your environment:  %s\n", strings.Join(plan, " "))
		}

		registerWebhookEndpoint(cmd, out, targets)

		printNextSteps(out, project, targets)

		return nil
	},
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

	if initYes || len(available) == 1 {
		return available, nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), "\nRecommended setup:")
	for _, target := range available {
		fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s\n", target.Label)
	}
	customize, err := confirm(cmd.OutOrStdout(), cmd.InOrStdin(), "\nCustomize setup?", false)
	if err != nil {
		return nil, err
	}
	if !customize {
		return available, nil
	}

	labels := make([]string, len(available))
	for i, t := range available {
		labels[i] = t.Label
	}

	picks, err := choose(cmd.OutOrStdout(), cmd.InOrStdin(), "Choose advanced capabilities", labels, true)
	if err != nil {
		return nil, err
	}

	var picked []scaffold.Target
	for _, i := range picks {
		picked = append(picked, available[i])
	}

	return picked, nil
}

func bootstrapCapabilities(targets []scaffold.Target) []string {
	var capabilities []string
	if hasTarget(targets, scaffold.TargetClient) {
		capabilities = append(capabilities, "server")
	}
	if hasTarget(targets, scaffold.TargetCheckout) {
		capabilities = append(capabilities, "checkout")
	}
	return capabilities
}

func localOrigin(project scaffold.Project) string {
	switch project.Stack {
	case scaffold.StackNext, scaffold.StackReact, scaffold.StackVue, scaffold.StackSvelte:
		return "http://localhost:3000"
	default:
		return ""
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
		for tmpl := range targets[i].Files {
			remapped[tmpl] = override
		}

		targets[i].Files = remapped
	}
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

func printPlan(out interface{ Write([]byte) (int, error) }, project scaffold.Project, targets []scaffold.Target) error {
	fmt.Fprintln(out, "\nDry run — would do the following:")

	for _, plan := range scaffold.NpmInstallPlans(project, targets) {
		fmt.Fprintf(out, "  $ %s\n", strings.Join(plan, " "))
	}

	runCmds, showCmds := scaffold.OtherInstallCmds(targets)
	for _, plan := range append(runCmds, showCmds...) {
		fmt.Fprintf(out, "  $ %s\n", strings.Join(plan, " "))
	}

	fmt.Fprintf(out, "  write %s (REEVIT_API_KEY, REEVIT_ORG_ID, REEVIT_WEBHOOK_SECRET) + .env.example + .gitignore\n", "env file")

	for _, t := range targets {
		for _, path := range t.Files {
			fmt.Fprintf(out, "  write %s\n", path)
		}
	}

	return nil
}

func printNextSteps(out interface{ Write([]byte) (int, error) }, project scaffold.Project, targets []scaffold.Target) {
	fmt.Fprintln(out, "\nNext steps:")

	for _, t := range targets {
		switch t.Key {
		case scaffold.TargetWebhook:
			path := "/<your webhook path>"
			if _, handlerPath := scaffold.WebhookHandler(project); handlerPath != "" {
				path = handlerPath
			}

			fmt.Fprintln(out, "  • Forward signed test events to your webhook handler:")
			fmt.Fprintf(out, "      reevit listen --forward-to http://localhost:<port>%s\n", path)
			fmt.Fprintln(out, "    and put the printed signing secret in REEVIT_WEBHOOK_SECRET.")
			fmt.Fprintln(out, "  • Then verify the whole setup (signature check included):")
			fmt.Fprintf(out, "      reevit doctor --webhook-url http://localhost:<port>%s\n", path)
		case scaffold.TargetCheckout:
			fmt.Fprintln(out, "  • Render the checkout component with an amount in the smallest currency unit.")
		case scaffold.TargetClient:
			fmt.Fprintln(out, "  • Create a test payment with the server client, then check `reevit payments list`.")
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

func joinManagers(managers []scaffold.PackageManager) string {
	names := make([]string, len(managers))
	for i, m := range managers {
		names[i] = string(m)
	}

	return strings.Join(names, ", ")
}

func init() {
	initCmd.Flags().StringSliceVar(&initTargets, "target", nil, "what to scaffold (webhook, checkout, client) — skips the prompt")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "scaffold everything available without prompting")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "print what would happen without writing anything")
	initCmd.Flags().StringVar(&initWebhookPath, "webhook-path", "", "custom output path for the webhook handler")
	initCmd.Flags().StringVar(&initCheckoutPath, "checkout-path", "", "custom output path for the checkout component")
	initCmd.Flags().StringVar(&initClientPath, "client-path", "", "custom output path for the server client")
	initCmd.Flags().StringVar(&initRegisterWebhook, "register-webhook", "", "register this production webhook endpoint in your dashboard")
	initCmd.Flags().BoolVar(&initRotateTestKeys, "rotate-test-keys", false, "replace project test credentials after local secrets were lost")

	rootCmd.AddCommand(initCmd)
}
