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
