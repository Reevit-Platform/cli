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
