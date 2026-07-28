package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEnv_RefusesLiveKeyInBrowserVar(t *testing.T) {
	dir := t.TempDir()
	project := Project{Root: dir, Stack: StackReact}

	_, err := WriteEnv(project, "pfk_live_abc123.secretpart", "org_1", true)
	if !errors.Is(err, ErrLiveKeyInClientEnv) {
		t.Fatalf("want ErrLiveKeyInClientEnv, got %v", err)
	}

	env := readFile(t, dir, ".env.local")
	if strings.Contains(env, "VITE_REEVIT_KEY=") {
		t.Fatal("live key must never reach a browser-exposed var")
	}
}

func TestWriteEnv_CreatesEnvFileOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	project := Project{Root: dir, Stack: StackReact}

	if _, err := WriteEnv(project, "pfk_test_abc.parts", "org_1", false); err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ".env.local"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %o, want 600 (it holds the API key)", info.Mode().Perm())
	}
}

func TestWriteEnv_TightensPreExistingBroadEnvFile(t *testing.T) {
	dir := t.TempDir()
	project := Project{Root: dir, Stack: StackReact}

	// Framework-created 0644 env file predating reevit init.
	envPath := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(envPath, []byte("EXISTING=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteEnv(project, "pfk_test_abc.parts", "org_1", false); err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %o, want tightened to 600", info.Mode().Perm())
	}
}

func TestWriteEnv_TestKeyStillWritesBrowserVar(t *testing.T) {
	dir := t.TempDir()
	project := Project{Root: dir, Stack: StackReact}

	res, err := WriteEnv(project, "pfk_test_abc123.secretpart", "org_1", true)
	if err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	if res.ClientKeyVar != "VITE_REEVIT_KEY" {
		t.Fatalf("client var = %q, want VITE_REEVIT_KEY", res.ClientKeyVar)
	}

	env := readFile(t, dir, ".env.local")
	if !strings.Contains(env, "VITE_REEVIT_KEY=pfk_test_abc123.secretpart") {
		t.Fatalf(".env.local missing browser key: %s", env)
	}
}

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

	res, err := WriteEnv(project, "pfk_test_abc.secret", "org_123", true)
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

	if res.ClientKeyVar != "NEXT_PUBLIC_REEVIT_KEY" {
		t.Errorf("client var = %q, want NEXT_PUBLIC_REEVIT_KEY for Next.js", res.ClientKeyVar)
	}

	if !strings.Contains(env, "NEXT_PUBLIC_REEVIT_KEY=pfk_test_abc.secret") {
		t.Errorf(".env.local missing browser key: %s", env)
	}

	if !strings.Contains(example, "NEXT_PUBLIC_REEVIT_KEY=") {
		t.Error(".env.example missing client-key placeholder")
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

	res, err := WriteEnv(project, "pfk_test_new.secret", "org_123", false)
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
			res, err := WriteEnv(project, "k", "org_123", false)
			if err != nil {
				t.Fatalf("WriteEnv: %v", err)
			}

			if res.GitignoreNoted == tc.covered {
				t.Errorf("pattern %q: GitignoreNoted = %v, want %v", tc.pattern, res.GitignoreNoted, !tc.covered)
			}
		})
	}
}
