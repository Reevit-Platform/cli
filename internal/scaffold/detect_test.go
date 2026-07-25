package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()

	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectStacks(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  Stack
	}{
		{"go", map[string]string{"go.mod": "module x"}, StackGo},
		{"php", map[string]string{"composer.json": "{}"}, StackPHP},
		{"python", map[string]string{"pyproject.toml": ""}, StackPython},
		{"next", map[string]string{"package.json": `{"dependencies":{"next":"16.0.0","react":"19"}}`}, StackNext},
		{"react", map[string]string{"package.json": `{"dependencies":{"react":"19"}}`}, StackReact},
		{"vue", map[string]string{"package.json": `{"dependencies":{"vue":"3"}}`}, StackVue},
		{"sveltekit", map[string]string{"package.json": `{"devDependencies":{"@sveltejs/kit":"2"}}`}, StackSvelte},
		{"node", map[string]string{"package.json": `{"dependencies":{"express":"4"}}`}, StackNode},
		{"empty", map[string]string{}, StackUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				write(t, dir, rel, content)
			}

			if got := Detect(dir).Stack; got != tc.want {
				t.Errorf("Detect().Stack = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectPackageManagers(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "bun.lock", "")
	write(t, dir, "pnpm-lock.yaml", "")
	write(t, dir, "package-lock.json", "")

	p := Detect(dir)

	if p.Manager != PMBun {
		t.Errorf("primary manager = %q, want bun (most deliberate lockfile)", p.Manager)
	}

	if len(p.Managers) != 3 {
		t.Errorf("managers = %v, want all three lockfiles reported", p.Managers)
	}
}

func TestPackageManagerDeclarationWinsOverLockfiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "package.json", `{
		"packageManager":"pnpm@10.33.0",
		"dependencies":{"next":"16"}
	}`)
	write(t, dir, "bun.lock", "")
	write(t, dir, "package-lock.json", "")

	project := Detect(dir)
	if project.Manager != PMPnpm {
		t.Fatalf("manager = %q, want pnpm", project.Manager)
	}
	plans := NpmInstallPlans(project, TargetsFor(project))
	if len(plans) != 1 || plans[0][0] != "pnpm" {
		t.Fatalf("install plans = %#v, want one pnpm command", plans)
	}
}

func TestDetectNextSrcDirAndTypescript(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "src/app/layout.tsx", "")

	p := Detect(dir)

	if !p.TypeScript || !p.SrcDir {
		t.Errorf("TypeScript=%v SrcDir=%v, want both true", p.TypeScript, p.SrcDir)
	}
}

func TestDetectNextPagesRouter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "pages/_app.tsx", "")

	project := Detect(dir)
	if project.NextRouter != NextRouterPages {
		t.Fatalf("NextRouter = %q, want pages", project.NextRouter)
	}
	var files []string
	for _, target := range TargetsFor(project) {
		for _, path := range target.Files {
			files = append(files, path)
		}
	}
	if !stringSliceContains(files, "pages/reevit-demo.tsx") ||
		!stringSliceContains(files, "pages/api/webhooks/reevit.ts") {
		t.Fatalf("generated files = %#v", files)
	}
}

func TestDetectsFullStackVueAndSvelteAdapters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		dependency string
		framework  Framework
	}{
		{name: "nuxt", dependency: `"nuxt":"4"`, framework: FrameworkNuxt},
		{name: "sveltekit", dependency: `"@sveltejs/kit":"2"`, framework: FrameworkSvelteKit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "package.json", `{"dependencies":{`+tc.dependency+`}}`)
			project := Detect(dir)
			if project.Framework != tc.framework {
				t.Fatalf("Framework = %q, want %q", project.Framework, tc.framework)
			}
			targets := TargetsFor(project)
			if len(targets) != 3 {
				t.Fatalf("targets = %#v, want checkout, webhook, and server client", targets)
			}
		})
	}
}

func TestDetectsPythonInstallerFromProjectFiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		file string
		want Installer
	}{
		{file: "uv.lock", want: InstallerUV},
		{file: "poetry.lock", want: InstallerPoetry},
		{file: "Pipfile.lock", want: InstallerPipenv},
		{file: "requirements.txt", want: InstallerPip},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "pyproject.toml", "[project]\nname='shop'\n")
			write(t, dir, tc.file, "")
			project := Detect(dir)
			if project.Installer != tc.want {
				t.Fatalf("Installer = %q, want %q", project.Installer, tc.want)
			}
		})
	}
}

func TestDetectPythonInstallerFromManifestWithoutLockfile(t *testing.T) {
	t.Parallel()

	poetryRoot := t.TempDir()
	write(t, poetryRoot, "pyproject.toml", "[tool.poetry]\nname='shop'\n")
	if got := Detect(poetryRoot).Installer; got != InstallerPoetry {
		t.Fatalf("Poetry installer = %q", got)
	}

	pipenvRoot := t.TempDir()
	write(t, pipenvRoot, "Pipfile", "[packages]\nflask='*'\n")
	project := Detect(pipenvRoot)
	if project.Stack != StackPython || project.Installer != InstallerPipenv {
		t.Fatalf("Pipfile project = %#v", project)
	}
}

func TestDetectUsesNearestParentLockfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "pnpm-lock.yaml", "")
	child := filepath.Join(root, "apps", "shop")
	write(t, child, "package.json", `{"dependencies":{"react":"19"}}`)

	project := Detect(child)
	if project.Manager != PMPnpm || len(project.Managers) != 1 {
		t.Fatalf("project managers = %v selected=%s", project.Managers, project.Manager)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
