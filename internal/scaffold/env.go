package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envHeader marks the block reevit init manages inside env files.
const envHeader = "# Added by `reevit init`"

// EnvResult reports what WriteEnv did, for the summary output.
type EnvResult struct {
	EnvFile        string // file the key was written to (relative)
	EnvExample     string // example file updated, "" when skipped
	GitignoreNoted bool   // true when we had to add the env file to .gitignore
	KeyAlreadySet  bool   // true when REEVIT_API_KEY was already present
	ClientKeyVar   string // browser-exposed var written (e.g. NEXT_PUBLIC_REEVIT_KEY), "" when none
}

type ProjectCredentials struct {
	ServerKey     string
	CheckoutKey   string
	OrgID         string
	WebhookSecret string
}

// ClientKeyVar returns the browser-exposed env var the checkout templates
// read, following each bundler's convention for client-side variables:
// Next.js inlines NEXT_PUBLIC_*, Vite-based stacks (React/Vue/Svelte) expose
// VITE_* via import.meta.env. Server-only stacks have no client bundle ("").
func ClientKeyVar(stack Stack) string {
	switch stack {
	case StackNext:
		return "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY"
	case StackReact, StackVue, StackSvelte:
		return "VITE_REEVIT_CHECKOUT_KEY"
	default:
		return ""
	}
}

// envFileFor picks the conventional env filename per stack.
func envFileFor(stack Stack) string {
	switch stack {
	case StackNext, StackReact, StackVue, StackSvelte:
		return ".env.local"
	default:
		return ".env"
	}
}

// WriteEnv appends REEVIT_API_KEY (and a webhook-secret placeholder) to the
// stack's env file, mirrors placeholders into .env.example, and makes sure
// the env file is gitignored. Existing values are never overwritten.
// WriteEnv appends the Reevit env vars. includeClientKey additionally writes
// the stack's browser-exposed key variable (NEXT_PUBLIC_*/VITE_*) so the
// scaffolded checkout component actually has a key at runtime — test-mode
// keys only; the comment in the file says so.
func WriteEnv(project Project, credentials ProjectCredentials) (EnvResult, error) {
	res := EnvResult{EnvFile: envFileFor(project.Stack)}
	envPath := filepath.Join(project.Root, res.EnvFile)
	clientVar := ClientKeyVar(project.Stack)

	existing, _ := os.ReadFile(envPath)
	current := string(existing)
	if strings.Contains(current, "REEVIT_API_KEY=") {
		res.KeyAlreadySet = true
	}

	var lines []string
	if !res.KeyAlreadySet && credentials.ServerKey != "" {
		lines = append(lines, "REEVIT_API_KEY="+credentials.ServerKey)
	}
	if !strings.Contains(current, "REEVIT_ORG_ID=") && credentials.OrgID != "" {
		lines = append(lines, "REEVIT_ORG_ID="+credentials.OrgID)
	}
	if !strings.Contains(current, "REEVIT_WEBHOOK_SECRET=") && credentials.WebhookSecret != "" {
		lines = append(lines, "REEVIT_WEBHOOK_SECRET="+credentials.WebhookSecret)
	}
	if clientVar != "" && credentials.CheckoutKey != "" && !strings.Contains(current, clientVar+"=") {
		lines = append(lines, "# Browser checkout credential — test mode only", clientVar+"="+credentials.CheckoutKey)
		res.ClientKeyVar = clientVar
	}
	if len(lines) > 0 {
		block := "\n" + envHeader + "\n" + strings.Join(lines, "\n") + "\n"
		if len(existing) == 0 {
			block = strings.TrimPrefix(block, "\n")
		}
		if err := appendFile(envPath, block); err != nil {
			return res, fmt.Errorf("write %s: %w", res.EnvFile, err)
		}
	}

	// Placeholders only — the example file is committed.
	examplePath := filepath.Join(project.Root, ".env.example")
	if example, err := os.ReadFile(examplePath); err == nil || os.IsNotExist(err) {
		if !strings.Contains(string(example), "REEVIT_API_KEY") {
			clientLine := ""
			if credentials.CheckoutKey != "" && clientVar != "" {
				clientLine = clientVar + "=\n"
			}

			block := fmt.Sprintf("\n%s\nREEVIT_API_KEY=\nREEVIT_ORG_ID=\nREEVIT_WEBHOOK_SECRET=\n%s", envHeader, clientLine)
			if len(example) == 0 {
				block = strings.TrimPrefix(block, "\n")
			}

			if err := appendFile(examplePath, block); err != nil {
				return res, fmt.Errorf("write .env.example: %w", err)
			}

			res.EnvExample = ".env.example"
		}
	}

	noted, err := ensureGitignored(project.Root, res.EnvFile)
	if err != nil {
		return res, err
	}

	res.GitignoreNoted = noted

	return res, nil
}

// ensureGitignored adds the env file to .gitignore unless a pattern already
// covers it. Returns true when a line was added.
func ensureGitignored(root, envFile string) (bool, error) {
	path := filepath.Join(root, ".gitignore")

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read .gitignore: %w", err)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		pattern := strings.TrimSpace(line)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}

		if gitignoreCovers(pattern, envFile) {
			return false, nil
		}
	}

	block := "\n# Local secrets (added by `reevit init`)\n" + envFile + "\n"
	if len(raw) == 0 {
		block = strings.TrimPrefix(block, "\n")
	}

	if err := appendFile(path, block); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}

	return true, nil
}

// gitignoreCovers handles the common patterns that match an env file:
// exact name, leading slash, ".env*", "*.local", and bare ".env" for
// ".env.local"-style names is NOT assumed (git treats it as exact).
func gitignoreCovers(pattern, envFile string) bool {
	pattern = strings.TrimPrefix(pattern, "/")

	if pattern == envFile {
		return true
	}

	if matched, err := filepath.Match(pattern, envFile); err == nil && matched {
		return true
	}

	return false
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(content)

	return err
}
