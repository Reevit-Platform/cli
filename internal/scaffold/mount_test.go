package scaffold

import (
	"strings"
	"testing"
)

func TestApplyMountsRecognizedWebhookEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		project    Project
		entry      string
		source     string
		want       string
		wantBefore string
	}{
		{
			name: "express",
			project: Project{
				Stack: StackNode, Framework: FrameworkExpress,
				TypeScript: true,
			},
			entry: "src/server.ts",
			source: `import express from "express";
const app = express();
app.use(express.json());
app.listen(3000);
`,
			want:       "mountReevitWebhook(app)",
			wantBefore: "app.use(express.json())",
		},
		{
			name: "go-default-mux",
			project: Project{
				Stack: StackGo, Framework: FrameworkGeneric,
			},
			entry: "main.go",
			source: `package main
import "net/http"
func main() {
	http.ListenAndServe(":8080", nil)
}
`,
			want:       `http.HandleFunc("/webhooks/reevit", HandleReevitWebhook)`,
			wantBefore: `http.ListenAndServe`,
		},
		{
			name: "fastapi",
			project: Project{
				Stack: StackPython, Framework: FrameworkFastAPI,
				Installer: InstallerPip,
			},
			entry:      "main.py",
			source:     "\"\"\"Application entry.\"\"\"\nfrom __future__ import annotations\nfrom fastapi import FastAPI\napp = FastAPI()\n",
			want:       "app.include_router(reevit_router)",
			wantBefore: "",
		},
		{
			name: "flask",
			project: Project{
				Stack: StackPython, Framework: FrameworkFlask,
				Installer: InstallerPip,
			},
			entry:      "app.py",
			source:     "from flask import Flask\napp = Flask(__name__)\n",
			want:       "app.register_blueprint(reevit_webhooks)",
			wantBefore: "",
		},
		{
			name: "django",
			project: Project{
				Stack: StackPython, Framework: FrameworkDjango,
				Installer: InstallerPip,
			},
			entry: "shop/urls.py",
			source: `from django.urls import path
urlpatterns = [
]
`,
			want:       `path("webhooks/reevit", reevit_webhook)`,
			wantBefore: "",
		},
		{
			name: "laravel",
			project: Project{
				Stack: StackPHP, Framework: FrameworkLaravel,
				Installer: InstallerComposer,
			},
			entry: "bootstrap/app.php",
			source: `<?php
use Illuminate\Foundation\Application;
return Application::configure(basePath: dirname(__DIR__))
    ->withRouting(
        web: __DIR__.'/../routes/web.php',
        health: '/up',
    )->create();
`,
			want:       "routes/reevit.php",
			wantBefore: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.project.Root = root
			write(t, root, test.entry, test.source)

			targets := TargetsFor(test.project)
			if err := Preflight(test.project, targets, Manifest{}); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(test.project, targets, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(test.project, targets, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}

			updated := readFile(t, root, test.entry)
			if strings.Count(updated, webhookMountMarker) == 0 ||
				!strings.Contains(updated, test.want) {
				t.Fatalf("entry was not mounted:\n%s", updated)
			}
			if strings.Count(updated, test.want) != 1 {
				t.Fatalf("mount was not idempotent:\n%s", updated)
			}
			if test.wantBefore != "" &&
				strings.Index(updated, test.want) > strings.Index(updated, test.wantBefore) {
				t.Fatalf("mount must precede %q:\n%s", test.wantBefore, updated)
			}
			if strings.Contains(test.source, "from __future__ import") &&
				strings.Index(updated, "from __future__ import") >
					strings.Index(updated, "from reevit_webhook import") {
				t.Fatalf("Reevit import preceded Python future import:\n%s", updated)
			}

			var withoutWebhook []Target
			for _, target := range targets {
				if target.Key != TargetWebhook {
					withoutWebhook = append(withoutWebhook, target)
				}
			}
			preparation, err := PrepareApply(
				test.project,
				withoutWebhook,
				Manifest{
					Capabilities:   []string{"webhook"},
					GeneratedEdits: []string{test.entry},
				},
				ExistingFilesFresh,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(test.project, withoutWebhook, ApplyOptions{
				ExistingFiles: ExistingFilesFresh,
				Preparation:   preparation,
			}); err != nil {
				t.Fatal(err)
			}
			cleaned := readFile(t, root, test.entry)
			if strings.Contains(cleaned, webhookMountMarker) ||
				strings.Contains(cleaned, test.want) {
				t.Fatalf("fresh setup left a stale webhook mount:\n%s", cleaned)
			}
		})
	}
}

func TestCustomExpressEntryFallsBackWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "src/server.ts", "export const customServer = true;\n")
	project := Project{
		Root: root, Stack: StackNode, Framework: FrameworkExpress,
		TypeScript: true,
	}
	targets := TargetsFor(project)
	if len(targets[0].Edits) != 0 {
		t.Fatalf("custom entry received unsafe edit: %#v", targets[0].Edits)
	}
}
