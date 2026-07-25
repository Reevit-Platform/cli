package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ReadEnvValue returns the value of key in the project's env file, or "" when
// the file or key is absent. Quotes around the value are stripped.
func ReadEnvValue(project Project, key string) string {
	raw, err := os.ReadFile(filepath.Join(project.Root, envFileFor(project.Stack)))
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, key+"=") {
			continue
		}

		value := strings.TrimPrefix(line, key+"=")

		return strings.Trim(strings.TrimSpace(value), `"'`)
	}

	return ""
}

// EnvFileName exposes the stack's conventional env filename for messages.
func EnvFileName(project Project) string {
	return envFileFor(project.Stack)
}

// SDKPackageFor returns the Reevit SDK dependency expected for the stack and
// whether the project declares it.
func SDKPackageFor(project Project) (pkg string, installed bool) {
	switch project.Stack {
	case StackNext, StackNode:
		pkg = "@reevit/node"
	case StackReact:
		pkg = "@reevit/react"
	case StackVue:
		pkg = "@reevit/vue"
	case StackSvelte:
		pkg = "@reevit/svelte"
	case StackGo:
		return "github.com/Reevit-Platform/go-sdk", fileContains(project.Root, "go.mod", "github.com/Reevit-Platform/go-sdk")
	case StackPHP:
		return "reevit/reevit-php", fileContains(project.Root, "composer.json", "reevit/reevit-php")
	case StackPython:
		installed = fileContains(project.Root, "requirements.txt", "reevit") ||
			fileContains(project.Root, "pyproject.toml", "reevit") ||
			fileContains(project.Root, "setup.py", "reevit")

		return "reevit", installed
	default:
		return "", false
	}

	// JS stacks: also accept @reevit/react on Next (checkout-only setups).
	pkgJSON := readPackageJSON(project.Root)
	if pkgJSON == nil {
		return pkg, false
	}

	if hasDep(pkgJSON, pkg) {
		return pkg, true
	}

	if project.Stack == StackNext && hasDep(pkgJSON, "@reevit/react") {
		return "@reevit/react", true
	}

	return pkg, false
}

// WebhookHandler returns the scaffolded webhook handler's file (if present)
// and the local URL path it serves on, so doctor can suggest the right
// --webhook-url. Empty file means no handler was found.
func WebhookHandler(project Project) (file, urlPath string) {
	candidates := map[string]string{}

	switch project.Stack {
	case StackNext:
		for _, extension := range []string{"ts", "js"} {
			candidates[prefixSrc(project, "app/api/webhooks/reevit/route."+extension)] = "/api/webhooks/reevit"
			candidates[prefixSrc(project, "pages/api/webhooks/reevit."+extension)] = "/api/webhooks/reevit"
		}
	case StackNode:
		for _, ext := range []string{"ts", "js"} {
			candidates["reevit/webhook."+ext] = "/webhooks/reevit"
		}
	case StackGo:
		candidates["reevit_webhook.go"] = "/webhooks/reevit"
	case StackPython:
		candidates["reevit_webhook.py"] = "/webhooks/reevit"
	case StackPHP:
		candidates["reevit-webhook.php"] = "/reevit-webhook.php"
	}
	if project.Framework == FrameworkLaravel {
		candidates["routes/reevit.php"] = "/webhooks/reevit"
	}

	if project.Framework == FrameworkNuxt {
		candidates["server/api/webhooks/reevit.post.ts"] = "/api/webhooks/reevit"
	}
	if project.Framework == FrameworkSvelteKit {
		candidates["src/routes/api/webhooks/reevit/+server.ts"] = "/api/webhooks/reevit"
	}

	for rel, path := range candidates {
		if exists(project.Root, rel) {
			return rel, path
		}
	}

	return "", ""
}

// WebhookMountInstruction returns the exact integration step for adapters
// where init cannot safely rewrite an arbitrary application entry file.
func WebhookMountInstruction(project Project) string {
	if len(WebhookMountEdits(project)) > 0 {
		return ""
	}

	switch project.Framework {
	case FrameworkExpress:
		return `Import mountReevitWebhook from reevit/webhook and call mountReevitWebhook(app) before app.use(express.json()).`
	case FrameworkFastAPI:
		return `Import router from reevit_webhook and call app.include_router(router).`
	case FrameworkFlask:
		return `Import reevit_webhooks from reevit_webhook and call app.register_blueprint(reevit_webhooks).`
	case FrameworkDjango:
		return `Import reevit_webhook and add path("webhooks/reevit", reevit_webhook) to urlpatterns.`
	case FrameworkLaravel:
		return `Load routes/reevit.php in bootstrap/app.php's withRouting(then: ...) under Route::middleware("api").`
	}

	switch project.Stack {
	case StackGo:
		return `Register http.HandleFunc("/webhooks/reevit", HandleReevitWebhook) on your server mux.`
	case StackPython:
		return `Call handle_reevit_event with the raw request body and X-Reevit-Signature header from your framework.`
	case StackPHP:
		return `reevit-webhook.php is a standalone handler; route POST /reevit-webhook.php to it.`
	default:
		return ""
	}
}

// CheckoutComponent returns the scaffolded checkout component file if present.
func CheckoutComponent(project Project) string {
	var candidates []string

	switch project.Stack {
	case StackNext:
		for _, ext := range []string{"tsx", "jsx"} {
			candidates = append(candidates, prefixSrc(project, "components/reevit-checkout-button."+ext))
		}
	case StackReact:
		for _, ext := range []string{"tsx", "jsx"} {
			candidates = append(candidates, "src/components/ReevitCheckoutButton."+ext)
		}
	case StackVue:
		candidates = append(candidates, "src/components/ReevitCheckoutButton.vue", "components/ReevitCheckoutButton.vue")
	case StackSvelte:
		candidates = append(candidates, "src/lib/ReevitCheckoutButton.svelte")
	}

	if project.Framework == FrameworkNuxt {
		candidates = append(candidates, "components/ReevitCheckoutButton.vue")
	}
	if project.Framework == FrameworkSvelteKit {
		candidates = append(candidates, "src/lib/ReevitCheckoutButton.svelte")
	}

	for _, rel := range candidates {
		if exists(project.Root, rel) {
			return rel
		}
	}

	return ""
}

// DemoPath is the URL path init scaffolds for a runnable checkout example.
func DemoPath(project Project) string {
	switch project.Framework {
	case FrameworkNext, FrameworkNuxt, FrameworkSvelteKit:
		return "/reevit-demo"
	case FrameworkReact, FrameworkVue, FrameworkSvelte:
		return "/reevit-demo.html"
	default:
		return ""
	}
}

// DefaultPort returns the conventional development port for the detected
// framework. It is used for exact post-init commands, never as configuration.
func DefaultPort(project Project) int {
	switch project.Framework {
	case FrameworkReact, FrameworkVue, FrameworkSvelte, FrameworkSvelteKit:
		return 5173
	case FrameworkNext, FrameworkNuxt:
		return 3000
	case FrameworkFlask:
		return 5000
	case FrameworkFastAPI, FrameworkDjango, FrameworkLaravel:
		return 8000
	default:
		return 0
	}
}

// DevCommand returns the detected package manager's command for the project's
// dev script. Empty means init cannot safely infer how the app starts.
func DevCommand(project Project) []string {
	if !hasPackageScript(project.Root, "dev") {
		return nil
	}

	switch project.Manager {
	case PMPnpm:
		return []string{"pnpm", "dev"}
	case PMYarn:
		return []string{"yarn", "dev"}
	case PMBun:
		return []string{"bun", "run", "dev"}
	default:
		return []string{"npm", "run", "dev"}
	}
}

func hasPackageScript(root, name string) bool {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}

	var pkg struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return false
	}
	_, ok := pkg.Scripts[name]

	return ok
}

func fileContains(root, rel, needle string) bool {
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}

	return strings.Contains(string(raw), needle)
}
