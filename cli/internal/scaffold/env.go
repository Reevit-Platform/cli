package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envHeader marks the block reevit init manages inside env files.
const envHeader = "# Added by `reevit init`"

// ErrLiveKeyInClientEnv is returned when a live-mode secret key would be
// written to a browser-exposed env var (NEXT_PUBLIC_*/VITE_*), which the
// bundler inlines into the public JS bundle for every visitor.
var ErrLiveKeyInClientEnv = errors.New("live API keys must never be written to browser-exposed env vars — use a test-mode key (pfk_test_…) for the client bundle")

// isLiveAPIKey reports whether the key is a live-mode Reevit secret key.
// The raw key form is "<keyID>.<secret>" and the keyID carries the prefix
// (backend internal/services/auth/api_keys.go: pfk_live / pfk_test).
func isLiveAPIKey(key string) bool {
	return strings.HasPrefix(key, "pfk_live")
}

// EnvResult reports what WriteEnv did, for the summary output.
type EnvResult struct {
	EnvFile        string // file the key was written to (relative)
	EnvExample     string // example file updated, "" when skipped
	GitignoreNoted bool   // true when we had to add the env file to .gitignore
	KeyAlreadySet  bool   // true when REEVIT_API_KEY was already present
	ClientKeyVar   string // browser-exposed var written (e.g. NEXT_PUBLIC_REEVIT_KEY), "" when none
}

// ClientKeyVar returns the browser-exposed env var the checkout templates
// read, following each bundler's convention for client-side variables:
// Next.js inlines NEXT_PUBLIC_*, Vite-based stacks (React/Vue/Svelte) expose
// VITE_* via import.meta.env. Server-only stacks have no client bundle ("").
func ClientKeyVar(stack Stack) string {
	switch stack {
	case StackNext:
		return "NEXT_PUBLIC_REEVIT_KEY"
	case StackReact, StackVue, StackSvelte:
		return "VITE_REEVIT_KEY"
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
func WriteEnv(project Project, apiKey, orgID string, includeClientKey bool) (EnvResult, error) {
	res := EnvResult{EnvFile: envFileFor(project.Stack)}
	envPath := filepath.Join(project.Root, res.EnvFile)
	clientVar := ClientKeyVar(project.Stack)

	existing, _ := os.ReadFile(envPath)
	if strings.Contains(string(existing), "REEVIT_API_KEY=") {
		res.KeyAlreadySet = true
	} else {
		block := fmt.Sprintf("\n%s\nREEVIT_API_KEY=%s\nREEVIT_ORG_ID=%s\n# Signing secret for verifying webhooks — `reevit listen` prints one for local dev\nREEVIT_WEBHOOK_SECRET=\n", envHeader, apiKey, orgID)
		if len(existing) == 0 {
			block = strings.TrimPrefix(block, "\n")
		}

		if err := appendFile(envPath, block); err != nil {
			return res, fmt.Errorf("write %s: %w", res.EnvFile, err)
		}
	}

	if includeClientKey && clientVar != "" {
		if isLiveAPIKey(apiKey) {
			return res, ErrLiveKeyInClientEnv
		}

		current, _ := os.ReadFile(envPath)
		if !strings.Contains(string(current), clientVar+"=") {
			block := fmt.Sprintf("# Exposed to the browser bundle by your framework — test-mode keys only\n%s=%s\n", clientVar, apiKey)
			if err := appendFile(envPath, block); err != nil {
				return res, fmt.Errorf("write %s: %w", res.EnvFile, err)
			}

			res.ClientKeyVar = clientVar
		}
	}

	// Placeholders only — the example file is committed.
	examplePath := filepath.Join(project.Root, ".env.example")
	if example, err := os.ReadFile(examplePath); err == nil || os.IsNotExist(err) {
		if !strings.Contains(string(example), "REEVIT_API_KEY") {
			clientLine := ""
			if includeClientKey && clientVar != "" {
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
