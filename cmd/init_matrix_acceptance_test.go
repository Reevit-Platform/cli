package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func TestInitAdapterMatrix(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		expected       []string
		installer      string
		serverKey      bool
		checkoutKeyVar string
		mountedEntry   string
	}{
		{
			name: "next-app-pnpm",
			files: map[string]string{
				"package.json":  `{"packageManager":"pnpm@10","dependencies":{"next":"16","react":"19"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"app/api/webhooks/reevit/route.ts", "app/reevit-demo/page.tsx",
				"components/reevit-checkout-button.tsx", "lib/reevit.ts",
				"app/api/reevit/checkout/route.ts",
			},
			installer: "pnpm add", serverKey: true,
			checkoutKeyVar: "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "next-pages-npm",
			files: map[string]string{
				"package.json":      `{"packageManager":"npm@11","dependencies":{"next":"16","react":"19"}}`,
				"tsconfig.json":     `{}`,
				"pages/index.tsx":   `export default function Page(){return null}`,
				"package-lock.json": `{}`,
			},
			expected: []string{
				"pages/api/webhooks/reevit.ts", "pages/reevit-demo.tsx",
				"components/reevit-checkout-button.tsx", "lib/reevit.ts",
				"pages/api/reevit/checkout.ts",
			},
			installer: "npm install", serverKey: true,
			checkoutKeyVar: "NEXT_PUBLIC_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "react-vite-yarn",
			files: map[string]string{
				"package.json":  `{"packageManager":"yarn@4","dependencies":{"react":"19"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"src/components/ReevitCheckoutButton.tsx", "src/reevit-demo.tsx", "reevit-demo.html",
			},
			installer: "yarn add", checkoutKeyVar: "VITE_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "nuxt-bun",
			files: map[string]string{
				"package.json":  `{"packageManager":"bun@1.3","dependencies":{"nuxt":"4","vue":"3"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"server/api/webhooks/reevit.post.ts", "components/ReevitCheckoutButton.vue",
				"pages/reevit-demo.vue", "server/utils/reevit.ts",
			},
			installer: "bun add", serverKey: true,
			checkoutKeyVar: "VITE_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "vue-vite-npm",
			files: map[string]string{
				"package.json":  `{"dependencies":{"vue":"3"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"src/components/ReevitCheckoutButton.vue", "src/reevit-demo.ts", "reevit-demo.html",
			},
			installer: "npm install", checkoutKeyVar: "VITE_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "sveltekit-pnpm",
			files: map[string]string{
				"package.json":  `{"packageManager":"pnpm@10","dependencies":{"@sveltejs/kit":"2","svelte":"5"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"src/routes/api/webhooks/reevit/+server.ts", "src/lib/ReevitCheckoutButton.svelte",
				"src/routes/reevit-demo/+page.svelte", "src/lib/server/reevit.ts",
			},
			installer: "pnpm add", serverKey: true,
			checkoutKeyVar: "VITE_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "svelte-vite-npm",
			files: map[string]string{
				"package.json":  `{"dependencies":{"svelte":"5"}}`,
				"tsconfig.json": `{}`,
			},
			expected: []string{
				"src/lib/ReevitCheckoutButton.svelte", "src/reevit-demo.ts", "reevit-demo.html",
			},
			installer: "npm install", checkoutKeyVar: "VITE_REEVIT_CHECKOUT_KEY",
		},
		{
			name: "express-npm",
			files: map[string]string{
				"package.json":  `{"dependencies":{"express":"5"}}`,
				"tsconfig.json": `{}`,
				"src/server.ts": "import express from \"express\";\nconst app = express();\napp.use(express.json());\napp.listen(3000);\n",
			},
			expected:  []string{"reevit/webhook.ts", "reevit/client.ts"},
			installer: "npm install", serverKey: true,
			mountedEntry: "src/server.ts",
		},
		{
			name: "go-modules",
			files: map[string]string{
				"go.mod":  "module example.test/shop\n\ngo 1.24\n",
				"main.go": "package main\nimport \"net/http\"\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}\n",
			},
			expected:  []string{"reevit_webhook.go", "reevit_client.go"},
			installer: "go get", serverKey: true,
			mountedEntry: "main.go",
		},
		{
			name: "fastapi-pip",
			files: map[string]string{
				"requirements.txt": "fastapi\n",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			expected:  []string{"reevit_webhook.py", "reevit_client.py"},
			installer: "python -m pip install", serverKey: true,
			mountedEntry: "main.py",
		},
		{
			name: "flask-pip",
			files: map[string]string{
				"requirements.txt": "flask\n",
				"app.py":           "from flask import Flask\napp = Flask(__name__)\n",
			},
			expected:  []string{"reevit_webhook.py", "reevit_client.py"},
			installer: "python -m pip install", serverKey: true,
			mountedEntry: "app.py",
		},
		{
			name: "django-pip",
			files: map[string]string{
				"requirements.txt": "django\n",
				"manage.py":        "",
				"shop/urls.py":     "from django.urls import path\nurlpatterns = [\n]\n",
			},
			expected:  []string{"reevit_webhook.py", "reevit_client.py"},
			installer: "python -m pip install", serverKey: true,
			mountedEntry: "shop/urls.py",
		},
		{
			name: "generic-python-uv",
			files: map[string]string{
				"pyproject.toml": "[project]\nname='shop'\nversion='0.1.0'\n",
				"uv.lock":        "",
			},
			expected:  []string{"reevit_webhook.py", "reevit_client.py"},
			installer: "uv add", serverKey: true,
		},
		{
			name: "laravel-composer",
			files: map[string]string{
				"composer.json":     `{"require":{"laravel/framework":"^12"}}`,
				"artisan":           "",
				"bootstrap/app.php": "<?php\nuse Illuminate\\Foundation\\Application;\nreturn Application::configure(basePath: dirname(__DIR__))\n    ->withRouting(\n        web: __DIR__.'/../routes/web.php',\n        health: '/up',\n    )->create();\n",
			},
			expected:  []string{"routes/reevit.php", "reevit-client.php"},
			installer: "composer require", serverKey: true,
			mountedEntry: "bootstrap/app.php",
		},
		{
			name: "generic-php-composer",
			files: map[string]string{
				"composer.json": `{"require":{"php":">=8.2"}}`,
			},
			expected:  []string{"reevit-webhook.php", "reevit-client.php"},
			installer: "composer require", serverKey: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runInitMatrixCase(
				t,
				test.files,
				test.expected,
				test.installer,
				test.serverKey,
				test.checkoutKeyVar,
				test.mountedEntry,
			)
		})
	}
}

func runInitMatrixCase(
	t *testing.T,
	files map[string]string,
	expected []string,
	installer string,
	wantServer bool,
	checkoutVar string,
	mountedEntry string,
) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeAcceptanceFile(t, root, rel, content)
	}

	commandLog := filepath.Join(root, "commands.log")
	binDir := filepath.Join(root, ".test-bin")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> \"$REEVIT_TEST_COMMAND_LOG\"\n"
	for _, executable := range []string{"npm", "pnpm", "yarn", "bun", "go", "python", "uv", "poetry", "pipenv", "composer"} {
		writeAcceptanceFile(t, binDir, executable, script)
		if err := os.Chmod(filepath.Join(binDir, executable), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REEVIT_TEST_COMMAND_LOG", commandLog)
	t.Setenv("REEVIT_CONFIG", filepath.Join(root, "cli-config.json"))
	t.Setenv("REEVIT_TELEMETRY", "0")

	var bootstraps int
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/bootstrap":
			if r.Header.Get("X-Reevit-Key") != "pfk_test_login.secret" {
				t.Errorf("bootstrap used wrong login key")
			}
			var request api.BootstrapRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode bootstrap: %v", err)
			}
			bootstraps++
			var result api.BootstrapResult
			result.Project.ID = request.ProjectID
			result.Project.OrganizationID = "org_matrix"
			if containsString(request.Capabilities, "server") {
				raw := ""
				if request.ExistingServerKeyID == "" {
					raw = "pfk_test_server.secret"
				}
				result.Credentials.Server = &api.BootstrapCredential{
					ID: "pfk_test_server", Raw: raw,
					Scopes: []string{"payments:read", "payments:write"},
				}
			}
			if containsString(request.Capabilities, "checkout") {
				raw := ""
				if request.ExistingCheckoutKeyID == "" {
					raw = "pfk_test_checkout.secret"
				}
				result.Credentials.Checkout = &api.BootstrapCredential{
					ID: "pfk_test_checkout", Raw: raw,
					Scopes: []string{"checkout:write"},
				}
			}
			_ = json.NewEncoder(w).Encode(result)
		case "/v1/checkout/sessions":
			if r.Header.Get("X-Reevit-Key") != "pfk_test_checkout.secret" {
				t.Errorf("checkout verification used wrong key")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": "pmt_checkout", "session_secret": "cs_test_matrix",
			})
		case "/v1/payments/intents":
			if r.Header.Get("X-Reevit-Key") != "pfk_test_server.secret" {
				t.Errorf("server verification used wrong key")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": "pmt_server", "status": "succeeded",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	if _, err := config.Save(config.Config{
		APIKey: "pfk_test_login.secret", BaseURL: apiServer.URL, Mode: "test",
	}); err != nil {
		t.Fatal(err)
	}

	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCWD) }()

	resetInitTestFlags()
	defer resetInitTestFlags()
	initYes = true
	initOrigin = "http://localhost:3100"
	var out bytes.Buffer
	initCmd.SetIn(bytes.NewReader(nil))
	initCmd.SetOut(&out)
	initCmd.SetErr(&out)
	initCmd.SetContext(context.Background())
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if mountedEntry != "" {
		mounted := acceptanceRead(t, root, mountedEntry)
		if !strings.Contains(mounted, "reevit:init webhook") {
			t.Fatalf("webhook was not mounted in %s:\n%s", mountedEntry, mounted)
		}
	}
	project := scaffold.Detect(root)
	manifest, err := scaffold.ReadManifest(project)
	if err != nil || manifest.Status != "complete" || manifest.Origin != "http://localhost:3100" {
		t.Fatalf("manifest = %#v, err = %v", manifest, err)
	}
	env := acceptanceRead(t, root, scaffold.EnvFileName(project))
	if strings.Contains(env, "pfk_test_login.secret") {
		t.Fatal("CLI login credential leaked into project env")
	}
	if wantServer != strings.Contains(env, "REEVIT_API_KEY=pfk_test_server.secret") {
		t.Fatalf("server credential mismatch:\n%s", env)
	}
	if checkoutVar != "" && !strings.Contains(env, checkoutVar+"=pfk_test_checkout.secret") {
		t.Fatalf("checkout credential missing:\n%s", env)
	}
	if !strings.Contains(env, "REEVIT_WEBHOOK_SECRET=whsec_") && wantServer {
		t.Fatalf("webhook signing secret missing:\n%s", env)
	}
	if !wantServer &&
		strings.Contains(out.String(), "REEVIT_API_KEY (test mode)") {
		t.Fatalf("checkout-only summary claimed a server key:\n%s", out.String())
	}
	example := acceptanceRead(t, root, ".env.example")
	if !wantServer && strings.Contains(example, "REEVIT_API_KEY=") {
		t.Fatalf("checkout-only example claimed a server key:\n%s", example)
	}
	out.Reset()
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("idempotent rerun: %v\n%s", err, out.String())
	}
	if bootstraps != 2 {
		t.Fatalf("bootstrap calls = %d, want first run plus rerun", bootstraps)
	}
	if rerunEnv := acceptanceRead(t, root, scaffold.EnvFileName(project)); rerunEnv != env {
		t.Fatalf("rerun changed project credentials or signing secret:\nbefore:\n%s\nafter:\n%s", env, rerunEnv)
	}
	if mountedEntry != "" {
		mounted := acceptanceRead(t, root, mountedEntry)
		if strings.Count(mounted, "reevit:init webhook") == 0 {
			t.Fatalf("rerun lost webhook mount in %s", mountedEntry)
		}
	}
	log, err := os.ReadFile(commandLog)
	if err != nil || !strings.Contains(string(log), installer) {
		t.Fatalf("installer log = %q, err = %v; want %q", log, err, installer)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
