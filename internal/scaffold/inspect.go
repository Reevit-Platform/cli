package scaffold

import (
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
		for _, ext := range []string{"ts", "js"} {
			candidates[prefixSrc(project, "app/api/webhooks/reevit/route."+ext)] = "/api/webhooks/reevit"
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

	for rel, path := range candidates {
		if exists(project.Root, rel) {
			return rel, path
		}
	}

	return "", ""
}

func fileContains(root, rel, needle string) bool {
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}

	return strings.Contains(string(raw), needle)
}
