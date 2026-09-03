package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNPMPackageShipsCompleteGettingStartedDocumentation(t *testing.T) {
	t.Parallel()

	readmeBytes, err := os.ReadFile("npm/README.md")
	if err != nil {
		t.Fatalf("read npm README: %v", err)
	}

	readme := string(readmeBytes)
	requiredTopics := []string{
		"## Start testing in a few minutes",
		"## What the wizard configures",
		"## Supported projects",
		"## Test the integration",
		"## Safe to rerun",
		"## Troubleshooting",
		"npm install -g @reevit/cli",
		"reevit init --goal full",
		"reevit init --dry-run",
		"--checkout-page",
		"--checkout-fields",
		"--checkout-metadata",
		"reevit doctor",
		"reevit trigger payment.succeeded",
		"reevit listen",
		"--rotate-test-keys",
		"--overwrite",
		"--fresh",
		".reevit/backups/",
		"Node.js 18 or newer",
	}

	for _, topic := range requiredTopics {
		if !strings.Contains(readme, topic) {
			t.Errorf("npm README must explain %q", topic)
		}
	}

	packageBytes, err := os.ReadFile("npm/package.json")
	if err != nil {
		t.Fatalf("read npm package: %v", err)
	}

	var pkg struct {
		Description string   `json:"description"`
		Files       []string `json:"files"`
	}
	if err := json.Unmarshal(packageBytes, &pkg); err != nil {
		t.Fatalf("parse npm package: %v", err)
	}

	if !strings.Contains(pkg.Description, "Set up Reevit") {
		t.Errorf("npm description must lead with project setup, got %q", pkg.Description)
	}

	for _, file := range pkg.Files {
		if file == "README.md" {
			return
		}
	}

	t.Error("npm package files must include README.md")
}

// readmes are the two files a user reads before running anything. They
// described the same paired key with two different scope lists, which is how
// the stale one survived: nothing compared them.
func readmes(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for _, path := range []string{"README.md", "npm/README.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		out[path] = string(raw)
	}

	return out
}

// The backend deliberately withholds api_keys:* from a paired key, so a
// paired key cannot mint another key. Documenting otherwise would invite
// someone to build on an escalation that does not exist.
func TestNoReadmePromisesKeyMintingScopes(t *testing.T) {
	t.Parallel()

	for path, body := range readmes(t) {
		for _, line := range strings.Split(body, "\n") {
			if !strings.Contains(line, "api_keys:") {
				continue
			}

			if !strings.Contains(line, "never") && !strings.Contains(line, "not ") {
				t.Errorf("%s claims a key has api_keys scope:\n  %s", path, line)
			}
		}
	}
}

// Both files list the scopes a paired login key receives. They have to list
// the same ones.
func TestBothReadmesListTheSamePairedKeyScopes(t *testing.T) {
	t.Parallel()

	want := []string{"payments:read", "payments:write", "webhooks:read", "webhooks:write"}

	for path, body := range readmes(t) {
		for _, scope := range want {
			if !strings.Contains(body, scope) {
				t.Errorf("%s never names the %s scope a paired key receives", path, scope)
			}
		}
	}
}

// The exit codes are the CLI's contract with CI. A code the binary can return
// and the README does not explain is a code nobody can act on.
func TestReadmeDocumentsEveryExitCode(t *testing.T) {
	t.Parallel()

	body := readmes(t)["README.md"]

	for _, code := range []struct{ code, meaning string }{
		{"`0`", "success"},
		{"`1`", "runtime error"},
		{"`2`", "usage error"},
		{"`3`", "found problems"},
		{"`130`", "cancelled"},
	} {
		row := ""

		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "| "+code.code+" |") {
				row = line
				break
			}
		}

		if row == "" {
			t.Errorf("README has no exit-code row for %s", code.code)
			continue
		}

		if !strings.Contains(row, code.meaning) {
			t.Errorf("README's %s row does not say %q:\n  %s", code.code, code.meaning, row)
		}
	}
}
