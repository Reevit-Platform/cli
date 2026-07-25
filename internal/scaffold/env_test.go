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

	res, err := WriteEnv(project, ProjectCredentials{
		ServerKey:     "pfk_test_server.secret",
		CheckoutKey:   "pfk_test_checkout.secret",
		OrgID:         "org_123",
		WebhookSecret: "whsec_local",
	})
	if err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}

	if res.EnvFile != ".env.local" {
		t.Errorf("env file = %q, want .env.local for Next.js", res.EnvFile)
	}

	env := readFile(t, dir, ".env.local")
	if !strings.Contains(env, "REEVIT_API_KEY=pfk_test_server.secret") {
		t.Errorf(".env.local missing key: %s", env)
	}

	if !strings.Contains(env, "REEVIT_ORG_ID=org_123") {
		t.Errorf(".env.local missing org id: %s", env)
	}

	example := readFile(t, dir, ".env.example")
	if strings.Contains(example, "pfk_test_") {
		t.Error(".env.example must contain placeholders, never the real key")
	}

	if !strings.Contains(example, "REEVIT_API_KEY=") {
		t.Error(".env.example missing placeholder")
	}

	if res.ClientKeyVar != "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY" {
		t.Errorf("client var = %q, want NEXT_PUBLIC_REEVIT_CHECKOUT_KEY for Next.js", res.ClientKeyVar)
	}

	if !strings.Contains(env, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_checkout.secret") {
		t.Errorf(".env.local missing browser key: %s", env)
	}

	if !strings.Contains(env, "REEVIT_WEBHOOK_SECRET=whsec_local") {
		t.Errorf(".env.local missing webhook secret: %s", env)
	}

	if !strings.Contains(example, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=") {
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

func TestWriteEnvCheckoutOnlyExampleOmitsServerAndWebhookKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := WriteEnv(Project{
		Root: dir, Stack: StackReact,
	}, ProjectCredentials{
		CheckoutKey: "pfk_test_checkout.secret",
		OrgID:       "org_123",
	})
	if err != nil {
		t.Fatal(err)
	}

	example := readFile(t, dir, ".env.example")
	if strings.Contains(example, "REEVIT_API_KEY=") ||
		strings.Contains(example, "REEVIT_WEBHOOK_SECRET=") {
		t.Fatalf("checkout-only example contains server placeholders:\n%s", example)
	}
	if !strings.Contains(example, "REEVIT_ORG_ID=") ||
		!strings.Contains(example, "VITE_REEVIT_CHECKOUT_KEY=") {
		t.Fatalf("checkout-only example is incomplete:\n%s", example)
	}
}

func TestWriteEnvNeverOverwritesExistingKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "REEVIT_API_KEY=existing_key\n")

	project := Project{Root: dir, Stack: StackNode}

	res, err := WriteEnv(project, ProjectCredentials{
		ServerKey: "pfk_test_new.secret", OrgID: "org_123",
	})
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

func TestWriteEnvFillsBlankPlaceholdersWithoutDuplicatingKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, ".env.local", "REEVIT_API_KEY=\nNEXT_PUBLIC_REEVIT_CHECKOUT_KEY=\n")
	_, err := WriteEnv(Project{Root: dir, Stack: StackNext}, ProjectCredentials{
		ServerKey: "pfk_test_server.secret", CheckoutKey: "pfk_test_checkout.secret",
		OrgID: "org_123",
	})
	if err != nil {
		t.Fatalf("WriteEnv() error = %v", err)
	}
	env := readFile(t, dir, ".env.local")
	if strings.Count(env, "REEVIT_API_KEY=") != 1 ||
		!strings.Contains(env, "REEVIT_API_KEY=pfk_test_server.secret") {
		t.Fatalf("server key was not filled safely: %s", env)
	}
	if strings.Count(env, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=") != 1 ||
		!strings.Contains(env, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_checkout.secret") {
		t.Fatalf("checkout key was not filled safely: %s", env)
	}
}

func TestWriteEnvReplacesOnlyKnownLegacyLoginCredential(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := Project{Root: root, Stack: StackNext}
	write(t, root, ".env.local", "REEVIT_API_KEY=pfk_test_login.secret\nNEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_login.secret\n")

	_, err := WriteEnv(project, ProjectCredentials{
		ServerKey:           "pfk_test_server.secret",
		CheckoutKey:         "pfk_test_checkout.secret",
		PreviousServerKey:   "pfk_test_login.secret",
		PreviousCheckoutKey: "pfk_test_login.secret",
		OrgID:               "org_123",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, root, ".env.local")
	if strings.Contains(raw, "pfk_test_login.secret") ||
		!strings.Contains(raw, "REEVIT_API_KEY=pfk_test_server.secret") ||
		!strings.Contains(raw, "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY=pfk_test_checkout.secret") {
		t.Fatalf("legacy credential migration failed:\n%s", raw)
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
			res, err := WriteEnv(project, ProjectCredentials{ServerKey: "k", OrgID: "org_123"})
			if err != nil {
				t.Fatalf("WriteEnv: %v", err)
			}

			if res.GitignoreNoted == tc.covered {
				t.Errorf("pattern %q: GitignoreNoted = %v, want %v", tc.pattern, res.GitignoreNoted, !tc.covered)
			}
		})
	}
}
