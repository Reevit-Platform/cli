package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	return string(raw)
}

func TestWriteEnvFreshNextProject(t *testing.T) {
	dir := t.TempDir()
	project := Project{Root: dir, Stack: StackNext}

	res, err := WriteEnv(project, "pfk_test_abc.secret", "org_123")
	if err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	if res.EnvFile != ".env.local" {
		t.Errorf("env file = %q, want .env.local for Next.js", res.EnvFile)
	}

	env := readFile(t, dir, ".env.local")
	if !strings.Contains(env, "REEVIT_API_KEY=pfk_test_abc.secret") {
		t.Errorf(".env.local missing key: %s", env)
	}

	if !strings.Contains(env, "REEVIT_ORG_ID=org_123") {
		t.Errorf(".env.local missing org id: %s", env)
	}

	example := readFile(t, dir, ".env.example")
	if strings.Contains(example, "pfk_test_abc") {
		t.Error(".env.example must contain placeholders, never the real key")
	}

	if !strings.Contains(example, "REEVIT_API_KEY=") {
		t.Error(".env.example missing placeholder")
	}

	gitignore := readFile(t, dir, ".gitignore")
	if !strings.Contains(gitignore, ".env.local") {
		t.Error(".gitignore must cover the env file")
	}

	if !res.GitignoreNoted {
		t.Error("GitignoreNoted should be true on a fresh project")
	}
}

func TestWriteEnvNeverOverwritesExistingKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "REEVIT_API_KEY=existing_key\n")

	project := Project{Root: dir, Stack: StackNode}

	res, err := WriteEnv(project, "pfk_test_new.secret", "org_123")
	if err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	if !res.KeyAlreadySet {
		t.Error("KeyAlreadySet should be true")
	}

	env := readFile(t, dir, ".env")
	if strings.Contains(env, "pfk_test_new") {
		t.Error("existing REEVIT_API_KEY must never be overwritten")
	}
}

func TestWriteEnvRespectsExistingGitignorePatterns(t *testing.T) {
	cases := []struct {
		pattern string
		covered bool
	}{
		{".env.local", true},
		{".env*", true},
		{"*.local", true},
		{"/.env.local", true},
		{".env", false}, // exact match only — does NOT cover .env.local
	}

	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, ".gitignore", tc.pattern+"\n")

			project := Project{Root: dir, Stack: StackNext}
			res, err := WriteEnv(project, "k", "org_123")
			if err != nil {
				t.Fatalf("WriteEnv: %v", err)
			}

			if res.GitignoreNoted == tc.covered {
				t.Errorf("pattern %q: GitignoreNoted = %v, want %v", tc.pattern, res.GitignoreNoted, !tc.covered)
			}
		})
	}
}
