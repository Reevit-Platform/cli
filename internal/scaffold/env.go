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
	ServerKey           string
	CheckoutKey         string
	PreviousServerKey   string
	PreviousCheckoutKey string
	OrgID               string
	WebhookSecret       string
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
	_, serverWasSet := envKeyState(current, "REEVIT_API_KEY")
	var replaced bool
	if credentials.PreviousServerKey != "" {
		var changed bool
		current, changed = replaceExactEnvValue(
			current, "REEVIT_API_KEY", credentials.PreviousServerKey, credentials.ServerKey,
		)
		replaced = replaced || changed
	}
	if clientVar != "" && credentials.PreviousCheckoutKey != "" {
		var changed bool
		current, changed = replaceExactEnvValue(
			current, clientVar, credentials.PreviousCheckoutKey, credentials.CheckoutKey,
		)
		replaced = replaced || changed
	}
	if serverWasSet && credentials.PreviousServerKey == "" {
		res.KeyAlreadySet = true
	}
	replacements := map[string]string{
		"REEVIT_API_KEY":        credentials.ServerKey,
		"REEVIT_ORG_ID":         credentials.OrgID,
		"REEVIT_WEBHOOK_SECRET": credentials.WebhookSecret,
	}
	if clientVar != "" {
		replacements[clientVar] = credentials.CheckoutKey
	}
	for key, value := range replacements {
		if value == "" {
			continue
		}
		var changed bool
		current, changed = fillBlankEnvValue(current, key, value)
		replaced = replaced || changed
	}
	if replaced {
		if err := os.WriteFile(envPath, []byte(current), 0o644); err != nil {
			return res, fmt.Errorf("fill blank values in %s: %w", res.EnvFile, err)
		}
		existing = []byte(current)
	}

	var lines []string
	serverFound, _ := envKeyState(current, "REEVIT_API_KEY")
	if !serverFound && credentials.ServerKey != "" {
		lines = append(lines, "REEVIT_API_KEY="+credentials.ServerKey)
	}
	orgFound, _ := envKeyState(current, "REEVIT_ORG_ID")
	if !orgFound && credentials.OrgID != "" {
		lines = append(lines, "REEVIT_ORG_ID="+credentials.OrgID)
	}
	webhookFound, _ := envKeyState(current, "REEVIT_WEBHOOK_SECRET")
	if !webhookFound && credentials.WebhookSecret != "" {
		lines = append(lines, "REEVIT_WEBHOOK_SECRET="+credentials.WebhookSecret)
	}
	clientFound, clientSet := envKeyState(current, clientVar)
	if clientVar != "" && credentials.CheckoutKey != "" && !clientFound {
		lines = append(lines, "# Browser checkout credential — test mode only", clientVar+"="+credentials.CheckoutKey)
		res.ClientKeyVar = clientVar
	} else if clientSet && !strings.Contains(string(existing), clientVar+"="+credentials.CheckoutKey) {
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

	// Placeholders only for capabilities this project actually uses — the
	// example file is committed, so it must never contain credential values.
	examplePath := filepath.Join(project.Root, ".env.example")
	if example, err := os.ReadFile(examplePath); err == nil || os.IsNotExist(err) {
		exampleText := string(example)
		var exampleLines []string
		if credentials.ServerKey != "" &&
			!strings.Contains(exampleText, "REEVIT_API_KEY=") {
			exampleLines = append(exampleLines, "REEVIT_API_KEY=")
		}
		if credentials.OrgID != "" &&
			!strings.Contains(exampleText, "REEVIT_ORG_ID=") {
			exampleLines = append(exampleLines, "REEVIT_ORG_ID=")
		}
		if credentials.WebhookSecret != "" &&
			!strings.Contains(exampleText, "REEVIT_WEBHOOK_SECRET=") {
			exampleLines = append(exampleLines, "REEVIT_WEBHOOK_SECRET=")
		}
		if credentials.CheckoutKey != "" && clientVar != "" &&
			!strings.Contains(exampleText, clientVar+"=") {
			exampleLines = append(exampleLines, clientVar+"=")
		}
		if len(exampleLines) > 0 {
			block := "\n" + envHeader + "\n" +
				strings.Join(exampleLines, "\n") + "\n"
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

func envKeyState(content, key string) (found, nonEmpty bool) {
	if key == "" {
		return false, false
	}
	prefix := key + "="
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		value = strings.Trim(value, `"'`)
		return true, value != ""
	}
	return false, false
}

func fillBlankEnvValue(content, key, value string) (string, bool) {
	found, nonEmpty := envKeyState(content, key)
	if !found || nonEmpty {
		return content, false
	}
	lines := strings.Split(content, "\n")
	prefix := key + "="
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[index] = prefix + value
			return strings.Join(lines, "\n"), true
		}
	}
	return content, false
}

func replaceExactEnvValue(content, key, previous, replacement string) (string, bool) {
	if key == "" || previous == "" || replacement == "" {
		return content, false
	}
	lines := strings.Split(content, "\n")
	prefix := key + "="
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		current := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), `"'`)
		if current != previous {
			return content, false
		}
		lines[index] = prefix + replacement
		return strings.Join(lines, "\n"), true
	}
	return content, false
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
