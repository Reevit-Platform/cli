package scaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This release-only test compiles generated TypeScript against the published
// SDK declarations. It is opt-in because it installs registry packages.
func TestExternalGeneratedTypeScriptCompiles(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	tests := []struct {
		name    string
		project Project
		pkg     string
		types   string
	}{
		{
			name: "next-app",
			project: Project{
				Stack: StackNext, Framework: FrameworkNext, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true, NextRouter: NextRouterApp,
			},
			pkg: `{
				"private": true,
				"packageManager": "pnpm@10.33.0",
				"dependencies": {
					"next": "16.2.10", "react": "19.2.4", "react-dom": "19.2.4",
					"@reevit/react": "0.10.2", "@reevit/node": "0.9.0"
				},
				"devDependencies": {
					"typescript": "5.9.3", "@types/node": "25.5.0",
					"@types/react": "19.2.14", "@types/react-dom": "19.2.3"
				}
			}`,
			types: `"node"`,
		},
		{
			name: "next-pages",
			project: Project{
				Stack: StackNext, Framework: FrameworkNext, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true, NextRouter: NextRouterPages,
			},
			pkg: `{
				"private": true,
				"packageManager": "pnpm@10.33.0",
				"dependencies": {
					"next": "16.2.10", "react": "19.2.4", "react-dom": "19.2.4",
					"@reevit/react": "0.10.2", "@reevit/node": "0.9.0"
				},
				"devDependencies": {
					"typescript": "5.9.3", "@types/node": "25.5.0",
					"@types/react": "19.2.14", "@types/react-dom": "19.2.3"
				}
			}`,
			types: `"node"`,
		},
		{
			name: "react-vite",
			project: Project{
				Stack: StackReact, Framework: FrameworkReact, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private": true,
				"packageManager": "pnpm@10.33.0",
				"dependencies": {
					"react": "19.2.4", "react-dom": "19.2.4", "@reevit/react": "0.10.2"
				},
				"devDependencies": {
					"typescript": "5.9.3", "@types/react": "19.2.14",
					"@types/react-dom": "19.2.3", "@types/node": "25.5.0", "vite": "7.2.2"
				}
			}`,
			types: `"node", "vite/client"`,
		},
		{
			name: "express",
			project: Project{
				Stack: StackNode, Framework: FrameworkExpress, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private": true, "packageManager": "pnpm@10.33.0",
				"dependencies": {"express":"5.1.0","@reevit/node":"0.9.0"},
				"devDependencies": {
					"typescript":"5.9.3","@types/node":"25.5.0","@types/express":"5.0.5"
				}
			}`,
			types: `"node"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.project.Root = root
			write(t, root, "package.json", test.pkg)
			write(t, root, "tsconfig.json", `{
				"compilerOptions": {
					"target": "ES2022", "lib": ["DOM", "ES2022"], "strict": true,
					"noEmit": true, "jsx": "preserve", "module": "ESNext",
					"moduleResolution": "Bundler", "esModuleInterop": true,
					"skipLibCheck": true, "types": [`+test.types+`]
				},
				"include": ["**/*.ts", "**/*.tsx"]
			}`)
			if test.name == "express" {
				write(t, root, "src/server.ts", `import express from "express";
const app = express();
app.use(express.json());
app.listen(3000);
`)
			}
			if _, err := Apply(test.project, TargetsFor(test.project), ApplyOptions{}); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			for _, command := range [][]string{
				{"pnpm", "install", "--ignore-scripts"},
				{"pnpm", "exec", "tsc", "--noEmit"},
			} {
				cmd := exec.CommandContext(ctx, command[0], command[1:]...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v failed: %v\n%s", command, err, output)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
				t.Fatalf("dependencies were not installed: %v", err)
			}
		})
	}
}

func TestExternalGeneratedGoCompiles(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/reevit-fixture\n\ngo 1.23\n")
	write(t, root, "main.go", `package main
import "net/http"
func main() {
	http.ListenAndServe(":8080", nil)
}
`)
	project := Detect(root)
	if _, err := Apply(project, TargetsFor(project), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, command := range [][]string{
		{"go", "get", "github.com/Reevit-Platform/go-sdk@latest"},
		{"go", "test", "./..."},
	} {
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", command, err, output)
		}
	}
}

func TestExternalGeneratedPythonImports(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	venv := filepath.Join(root, ".venv")
	python := filepath.Join(venv, "bin", "python")
	for _, command := range [][]string{
		{"python3", "-m", "venv", venv},
		{python, "-m", "pip", "install", "--quiet", "reevit==0.9.1", "fastapi", "flask", "django"},
	} {
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", command, err, output)
		}
	}

	for _, framework := range []Framework{
		FrameworkFastAPI, FrameworkFlask, FrameworkDjango, FrameworkGeneric,
	} {
		t.Run(string(framework), func(t *testing.T) {
			projectRoot := filepath.Join(root, string(framework))
			if err := os.MkdirAll(projectRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			project := Project{
				Root: projectRoot, Stack: StackPython,
				Framework: framework, Installer: InstallerPip,
			}
			entry := ""
			switch framework {
			case FrameworkFastAPI:
				entry = "main.py"
				write(t, projectRoot, entry, "\"\"\"App.\"\"\"\nfrom __future__ import annotations\nfrom fastapi import FastAPI\napp = FastAPI()\n")
			case FrameworkFlask:
				entry = "app.py"
				write(t, projectRoot, entry, "from __future__ import annotations\nfrom flask import Flask\napp = Flask(__name__)\n")
			case FrameworkDjango:
				entry = "shop/urls.py"
				write(t, projectRoot, entry, "from __future__ import annotations\nfrom django.urls import path\nurlpatterns = []\n")
			}
			if _, err := Apply(project, TargetsFor(project), ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			compileFiles := []string{"reevit_client.py", "reevit_webhook.py"}
			if entry != "" {
				compileFiles = append(compileFiles, entry)
			}
			for _, command := range [][]string{
				append([]string{python, "-m", "py_compile"}, compileFiles...),
				{python, "-c", "import reevit_client, reevit_webhook"},
			} {
				cmd := exec.CommandContext(ctx, command[0], command[1:]...)
				cmd.Dir = projectRoot
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v failed: %v\n%s", command, err, output)
				}
			}
		})
	}
}

func TestExternalGeneratedPHPSyntax(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	root := t.TempDir()
	write(t, root, "composer.json", `{"require":{"php":">=8.1"}}`)
	project := Detect(root)
	if _, err := Apply(project, TargetsFor(project), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, command := range [][]string{
		{"composer", "require", "--quiet", "reevit/reevit-php:0.1.0"},
		{"php", "-l", "reevit-client.php"},
		{"php", "-l", "reevit-webhook.php"},
	} {
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", command, err, output)
		}
	}

	laravelRoot := filepath.Join(root, "laravel")
	project = Project{
		Root: laravelRoot, Stack: StackPHP,
		Framework: FrameworkLaravel, Installer: InstallerComposer,
	}
	write(t, laravelRoot, "bootstrap/app.php", `<?php
use Illuminate\Foundation\Application;
return Application::configure(basePath: dirname(__DIR__))
    ->withRouting(
        web: __DIR__.'/../routes/web.php',
        health: '/up',
    )->create();
`)
	if _, err := Apply(project, TargetsFor(project), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"routes/reevit.php", "bootstrap/app.php"} {
		cmd := exec.CommandContext(ctx, "php", "-l", file)
		cmd.Dir = laravelRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Laravel %s syntax failed: %v\n%s", file, err, output)
		}
	}
}

func TestExternalGeneratedVueAndSvelteCompile(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	tests := []struct {
		name       string
		project    Project
		pkg        string
		tsconfig   string
		check      []string
		extraFiles map[string]string
	}{
		{
			name: "vue-vite",
			project: Project{
				Stack: StackVue, Framework: FrameworkVue, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private": true, "packageManager": "pnpm@10.33.0",
				"dependencies": {"vue":"3.5.22","@reevit/vue":"0.10.3","@reevit/core":"0.9.0"},
				"devDependencies": {"typescript":"5.9.3","vue-tsc":"3.1.4","vite":"7.2.2"}
			}`,
			tsconfig: `{
				"compilerOptions": {
					"target":"ES2022","module":"ESNext","moduleResolution":"Bundler",
					"strict":true,"noEmit":true,"skipLibCheck":true,"types":["vite/client"]
				},
				"include":["src/**/*.ts","src/**/*.vue"]
			}`,
			check: []string{"pnpm", "exec", "vue-tsc", "--noEmit"},
		},
		{
			name: "svelte-vite",
			project: Project{
				Stack: StackSvelte, Framework: FrameworkSvelte, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private": true, "packageManager": "pnpm@10.33.0", "type":"module",
				"dependencies": {"svelte":"5.43.2","@reevit/svelte":"0.10.2","@reevit/core":"0.9.0"},
				"devDependencies": {"typescript":"5.9.3","svelte-check":"4.3.3","vite":"7.2.2"}
			}`,
			tsconfig: `{
				"compilerOptions": {
					"target":"ES2022","module":"ESNext","moduleResolution":"Bundler",
					"strict":true,"noEmit":true,"skipLibCheck":true,"types":["vite/client"],
					"allowJs":true,"checkJs":true
				},
				"include":["src/**/*.ts","src/**/*.svelte"]
			}`,
			check: []string{"pnpm", "exec", "svelte-check", "--tsconfig", "./tsconfig.json"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.project.Root = root
			write(t, root, "package.json", test.pkg)
			write(t, root, "tsconfig.json", test.tsconfig)
			for rel, content := range test.extraFiles {
				write(t, root, rel, content)
			}
			if _, err := Apply(test.project, TargetsFor(test.project), ApplyOptions{}); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			for _, command := range [][]string{
				{"pnpm", "install", "--ignore-scripts"},
				test.check,
			} {
				cmd := exec.CommandContext(ctx, command[0], command[1:]...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v failed: %v\n%s", command, err, output)
				}
			}
		})
	}
}

func TestExternalGeneratedFullStackVueAndSvelteCompile(t *testing.T) {
	if os.Getenv("REEVIT_EXTERNAL_ADAPTER_TEST") != "1" {
		t.Skip("set REEVIT_EXTERNAL_ADAPTER_TEST=1 for registry-backed adapter checks")
	}

	tests := []struct {
		name       string
		project    Project
		pkg        string
		extraFiles map[string]string
		checks     [][]string
	}{
		{
			name: "nuxt",
			project: Project{
				Stack: StackVue, Framework: FrameworkNuxt, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private":true,"packageManager":"pnpm@10.33.0","type":"module",
				"dependencies":{
					"nuxt":"4.2.1","vue":"3.5.22","@reevit/vue":"0.10.3",
					"@reevit/core":"0.9.0","@reevit/node":"0.9.0"
				},
				"devDependencies":{"typescript":"5.9.3","vue-tsc":"3.1.4","@types/node":"25.5.0"}
			}`,
			extraFiles: map[string]string{
				"tsconfig.json": `{"extends":"./.nuxt/tsconfig.json"}`,
			},
			checks: [][]string{
				{"pnpm", "exec", "nuxi", "prepare"},
				{"pnpm", "exec", "nuxi", "typecheck"},
			},
		},
		{
			name: "sveltekit",
			project: Project{
				Stack: StackSvelte, Framework: FrameworkSvelteKit, Installer: InstallerPNPM,
				Manager: PMPnpm, TypeScript: true,
			},
			pkg: `{
				"private":true,"packageManager":"pnpm@10.33.0","type":"module",
				"dependencies":{
					"@sveltejs/kit":"2.48.5","svelte":"5.43.2","@reevit/svelte":"0.10.2",
					"@reevit/core":"0.9.0","@reevit/node":"0.9.0"
				},
				"devDependencies":{
					"@sveltejs/vite-plugin-svelte":"6.2.1","svelte-check":"4.3.3",
					"typescript":"5.9.3","vite":"7.2.2"
				}
			}`,
			extraFiles: map[string]string{
				"svelte.config.js": `import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";
export default { preprocess: vitePreprocess() };
`,
				"tsconfig.json": `{"extends":"./.svelte-kit/tsconfig.json","compilerOptions":{"allowJs":true,"checkJs":true,"strict":true,"skipLibCheck":true}}`,
			},
			checks: [][]string{
				{"pnpm", "exec", "svelte-kit", "sync"},
				{"pnpm", "exec", "svelte-check", "--tsconfig", "./tsconfig.json"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.project.Root = root
			write(t, root, "package.json", test.pkg)
			for rel, content := range test.extraFiles {
				write(t, root, rel, content)
			}
			if _, err := Apply(test.project, TargetsFor(test.project), ApplyOptions{}); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			commands := append([][]string{{"pnpm", "install", "--ignore-scripts"}}, test.checks...)
			for _, command := range commands {
				cmd := exec.CommandContext(ctx, command[0], command[1:]...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v failed: %v\n%s", command, err, output)
				}
			}
		})
	}
}
