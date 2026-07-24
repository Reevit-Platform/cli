package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// TargetKey identifies one thing `reevit init` can scaffold.
type TargetKey string

const (
	TargetWebhook  TargetKey = "webhook"
	TargetCheckout TargetKey = "checkout"
	TargetClient   TargetKey = "client"
)

// Target is a scaffoldable integration point for the detected stack.
type Target struct {
	Key   TargetKey
	Label string
	// Files maps template name → output path (relative to project root).
	Files map[string]string
	// NpmPackages are installed with the detected JS package manager(s).
	NpmPackages []string
	// InstallCmds are non-JS installs: run (go) or printed (pip/composer).
	InstallCmds [][]string
	// Run marks InstallCmds safe to execute rather than just print.
	Run bool
}

// templateData feeds the .tmpl files.
type templateData struct {
	TS bool
}

// ext picks the TypeScript or JavaScript extension for JS-family files.
func ext(project Project, tsExt, jsExt string) string {
	if project.TypeScript {
		return tsExt
	}

	return jsExt
}

// prefixSrc prepends src/ for Next.js projects that keep their app there.
func prefixSrc(project Project, path string) string {
	if project.SrcDir {
		return "src/" + path
	}

	return path
}

// TargetsFor returns what init can scaffold for the detected stack, in
// display order. An unknown stack gets nothing (init explains and stops).
func TargetsFor(project Project) []Target {
	switch project.Stack {
	case StackNext:
		return []Target{
			{
				Key:   TargetWebhook,
				Label: "Webhook handler (app/api/webhooks/reevit)",
				Files: map[string]string{
					"next-webhook.ts.tmpl": prefixSrc(project, "app/api/webhooks/reevit/route."+ext(project, "ts", "js")),
				},
				NpmPackages: []string{"@reevit/node"},
			},
			{
				Key:   TargetCheckout,
				Label: "Checkout button component (@reevit/react)",
				Files: map[string]string{
					"next-checkout.tsx.tmpl":      prefixSrc(project, "components/reevit-checkout-button."+ext(project, "tsx", "jsx")),
					"next-app-demo-page.tsx.tmpl": prefixSrc(project, "app/reevit-demo/page."+ext(project, "tsx", "jsx")),
				},
				NpmPackages: []string{"@reevit/react"},
			},
			{
				Key:   TargetClient,
				Label: "Server-side client (lib/reevit) for creating payments",
				Files: map[string]string{
					"next-client.ts.tmpl": prefixSrc(project, "lib/reevit."+ext(project, "ts", "js")),
				},
				NpmPackages: []string{"@reevit/node"},
			},
		}
	case StackReact:
		return []Target{{
			Key:   TargetCheckout,
			Label: "Checkout button component (@reevit/react)",
			Files: map[string]string{
				"react-checkout.tsx.tmpl": "src/components/ReevitCheckoutButton." + ext(project, "tsx", "jsx"),
			},
			NpmPackages: []string{"@reevit/react"},
		}}
	case StackVue:
		return []Target{{
			Key:   TargetCheckout,
			Label: "Checkout component (@reevit/vue)",
			Files: map[string]string{
				"vue-checkout.vue.tmpl": "src/components/ReevitCheckoutButton.vue",
			},
			NpmPackages: []string{"@reevit/vue", "@reevit/core"},
		}}
	case StackSvelte:
		return []Target{{
			Key:   TargetCheckout,
			Label: "Checkout component (@reevit/svelte)",
			Files: map[string]string{
				"svelte-checkout.svelte.tmpl": "src/lib/ReevitCheckoutButton.svelte",
			},
			NpmPackages: []string{"@reevit/svelte", "@reevit/core"},
		}}
	case StackNode:
		return []Target{
			{
				Key:   TargetWebhook,
				Label: "Webhook handler (Express)",
				Files: map[string]string{
					"express-webhook.ts.tmpl": "reevit/webhook." + ext(project, "ts", "js"),
				},
				NpmPackages: []string{"@reevit/node"},
			},
			{
				Key:   TargetClient,
				Label: "Server-side client for creating payments",
				Files: map[string]string{
					"node-client.ts.tmpl": "reevit/client." + ext(project, "ts", "js"),
				},
				NpmPackages: []string{"@reevit/node"},
			},
		}
	case StackGo:
		return []Target{
			{
				Key:   TargetWebhook,
				Label: "Webhook handler (net/http, verifier inlined)",
				Files: map[string]string{"go-webhook.go.tmpl": "reevit_webhook.go"},
			},
			{
				Key:         TargetClient,
				Label:       "Server-side client for creating payments",
				Files:       map[string]string{"go-client.go.tmpl": "reevit_client.go"},
				InstallCmds: [][]string{{"go", "get", "github.com/Reevit-Platform/go-sdk"}},
				Run:         true,
			},
		}
	case StackPython:
		return []Target{
			{
				Key:         TargetWebhook,
				Label:       "Webhook handler (FastAPI wiring, framework-agnostic core)",
				Files:       map[string]string{"python-webhook.py.tmpl": "reevit_webhook.py"},
				InstallCmds: [][]string{{"pip", "install", "reevit"}},
			},
			{
				Key:         TargetClient,
				Label:       "Server-side client for creating payments",
				Files:       map[string]string{"python-client.py.tmpl": "reevit_client.py"},
				InstallCmds: [][]string{{"pip", "install", "reevit"}},
			},
		}
	case StackPHP:
		return []Target{
			{
				Key:         TargetWebhook,
				Label:       "Webhook handler",
				Files:       map[string]string{"php-webhook.php.tmpl": "reevit-webhook.php"},
				InstallCmds: [][]string{{"composer", "require", "reevit/reevit-php"}},
			},
			{
				Key:         TargetClient,
				Label:       "Server-side client for creating payments",
				Files:       map[string]string{"php-client.php.tmpl": "reevit-client.php"},
				InstallCmds: [][]string{{"composer", "require", "reevit/reevit-php"}},
			},
		}
	default:
		return nil
	}
}

// FileResult reports one written (or skipped) file.
type FileResult struct {
	Path    string
	Skipped bool // an existing file is never overwritten
}

// Apply renders the targets' templates into the project. Existing files are
// left untouched and reported as skipped.
func Apply(project Project, targets []Target) ([]FileResult, error) {
	var results []FileResult

	data := templateData{TS: project.TypeScript}

	for _, target := range targets {
		for tmplName, outRel := range target.Files {
			outPath := filepath.Join(project.Root, outRel)

			if _, err := os.Stat(outPath); err == nil {
				results = append(results, FileResult{Path: outRel, Skipped: true})

				continue
			}

			content, err := render(tmplName, data)
			if err != nil {
				return results, err
			}

			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return results, fmt.Errorf("create %s: %w", filepath.Dir(outRel), err)
			}

			if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
				return results, fmt.Errorf("write %s: %w", outRel, err)
			}

			results = append(results, FileResult{Path: outRel})
		}
	}

	return results, nil
}

func render(name string, data templateData) (string, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/"+name)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", name, err)
	}

	return sb.String(), nil
}

// NpmInstallPlans returns one install command per package manager with a
// lockfile — multi-lockfile repos must keep every lockfile in sync or frozen
// installs break downstream. Packages are deduplicated across targets.
func NpmInstallPlans(project Project, targets []Target) [][]string {
	seen := map[string]bool{}

	var pkgs []string

	for _, t := range targets {
		for _, p := range t.NpmPackages {
			if !seen[p] {
				seen[p] = true
				pkgs = append(pkgs, p)
			}
		}
	}

	if len(pkgs) == 0 {
		return nil
	}

	managers := project.Managers
	if len(managers) == 0 {
		managers = []PackageManager{project.Manager}
	}

	var plans [][]string

	for _, m := range managers {
		args := m.InstallArgs(pkgs[0])
		args = append(args[:len(args)-1], pkgs...)
		plans = append(plans, args)
	}

	return plans
}

// OtherInstallCmds collects non-npm install commands, split into ones safe to
// run (go get) and ones only printed (pip/composer — environment-dependent).
func OtherInstallCmds(targets []Target) (run, show [][]string) {
	seen := map[string]bool{}

	for _, t := range targets {
		for _, cmd := range t.InstallCmds {
			key := strings.Join(cmd, " ")
			if seen[key] {
				continue
			}

			seen[key] = true

			if t.Run {
				run = append(run, cmd)
			} else {
				show = append(show, cmd)
			}
		}
	}

	return run, show
}
