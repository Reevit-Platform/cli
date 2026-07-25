// Package scaffold implements `reevit init`: it detects the project's stack,
// installs the matching SDK, wires environment variables, and writes
// integration starter files from embedded templates.
package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Stack identifies the project type an SDK maps to.
type Stack string

const (
	StackNext    Stack = "nextjs"
	StackReact   Stack = "react"
	StackVue     Stack = "vue"
	StackSvelte  Stack = "svelte"
	StackNode    Stack = "node"
	StackGo      Stack = "go"
	StackPHP     Stack = "php"
	StackPython  Stack = "python"
	StackUnknown Stack = "unknown"
)

type NextRouter string

const (
	NextRouterApp   NextRouter = "app"
	NextRouterPages NextRouter = "pages"
)

type Framework string

const (
	FrameworkNext      Framework = "nextjs"
	FrameworkReact     Framework = "react"
	FrameworkNuxt      Framework = "nuxt"
	FrameworkVue       Framework = "vue"
	FrameworkSvelteKit Framework = "sveltekit"
	FrameworkSvelte    Framework = "svelte"
	FrameworkExpress   Framework = "express"
	FrameworkFastAPI   Framework = "fastapi"
	FrameworkFlask     Framework = "flask"
	FrameworkDjango    Framework = "django"
	FrameworkLaravel   Framework = "laravel"
	FrameworkGeneric   Framework = "generic"
)

type Installer string

const (
	InstallerNPM      Installer = "npm"
	InstallerPNPM     Installer = "pnpm"
	InstallerYarn     Installer = "yarn"
	InstallerBun      Installer = "bun"
	InstallerGo       Installer = "go"
	InstallerUV       Installer = "uv"
	InstallerPoetry   Installer = "poetry"
	InstallerPipenv   Installer = "pipenv"
	InstallerPip      Installer = "pip"
	InstallerComposer Installer = "composer"
)

// PackageManager is how JS dependencies get installed in this project.
type PackageManager string

const (
	PMNpm  PackageManager = "npm"
	PMPnpm PackageManager = "pnpm"
	PMYarn PackageManager = "yarn"
	PMBun  PackageManager = "bun"
)

// Project is everything detection learned about the target directory.
type Project struct {
	Root      string
	Stack     Stack
	Framework Framework
	Installer Installer
	Manager   PackageManager
	// TypeScript is true when a tsconfig exists (JS stacks only).
	TypeScript bool
	// SrcDir is true when a Next.js project keeps its app under src/.
	SrcDir bool
	// NextRouter distinguishes the App and Pages Router output contracts.
	NextRouter NextRouter
	// Managers lists every package manager with a lockfile present — some
	// repos deliberately keep several in sync.
	Managers []PackageManager
}

// Detect inspects dir and returns what it finds. Detection is filesystem-only
// and never fails hard: an unrecognized directory yields StackUnknown.
func Detect(dir string) Project {
	p := Project{Root: dir, Stack: StackUnknown, Manager: PMNpm}

	if exists(dir, "go.mod") {
		p.Stack = StackGo
		p.Framework = FrameworkGeneric
		p.Installer = InstallerGo

		return p
	}

	if exists(dir, "composer.json") {
		p.Stack = StackPHP
		p.Framework = detectPHPFramework(dir)
		p.Installer = InstallerComposer

		return p
	}

	if exists(dir, "pyproject.toml") || exists(dir, "requirements.txt") ||
		exists(dir, "setup.py") || exists(dir, "Pipfile") {
		p.Stack = StackPython
		p.Framework = detectPythonFramework(dir)
		p.Installer = detectPythonInstaller(dir)

		return p
	}

	pkg := readPackageJSON(dir)
	if pkg == nil {
		return p
	}

	p.TypeScript = exists(dir, "tsconfig.json")
	p.SrcDir = exists(dir, "src/app") || (exists(dir, "src") && !exists(dir, "app") && hasDep(pkg, "next"))
	p.Managers = nearestLockfileManagers(dir)

	if declared, ok := declaredPackageManager(pkg); ok {
		p.Manager = declared
	} else if len(p.Managers) > 0 {
		p.Manager = p.Managers[0]
	}
	p.Installer = Installer(p.Manager)

	switch {
	case hasDep(pkg, "next"):
		p.Stack = StackNext
		p.Framework = FrameworkNext
		p.NextRouter = NextRouterApp
		if (exists(dir, "pages") || exists(dir, "src/pages")) &&
			!exists(dir, "app") && !exists(dir, "src/app") {
			p.NextRouter = NextRouterPages
		}
	case hasDep(pkg, "@sveltejs/kit"):
		p.Stack = StackSvelte
		p.Framework = FrameworkSvelteKit
	case hasDep(pkg, "svelte"):
		p.Stack = StackSvelte
		p.Framework = FrameworkSvelte
	case hasDep(pkg, "nuxt"):
		p.Stack = StackVue
		p.Framework = FrameworkNuxt
	case hasDep(pkg, "vue"):
		p.Stack = StackVue
		p.Framework = FrameworkVue
	case hasDep(pkg, "react") || hasDep(pkg, "react-dom"):
		p.Stack = StackReact
		p.Framework = FrameworkReact
	default:
		p.Stack = StackNode
		if hasDep(pkg, "express") {
			p.Framework = FrameworkExpress
		} else {
			p.Framework = FrameworkGeneric
		}
	}

	return p
}

func detectPythonInstaller(dir string) Installer {
	switch {
	case exists(dir, "uv.lock"):
		return InstallerUV
	case exists(dir, "poetry.lock") ||
		strings.Contains(strings.ToLower(readProjectFiles(dir, "pyproject.toml")), "[tool.poetry]"):
		return InstallerPoetry
	case exists(dir, "Pipfile.lock") || exists(dir, "Pipfile"):
		return InstallerPipenv
	default:
		return InstallerPip
	}
}

func nearestLockfileManagers(dir string) []PackageManager {
	current, err := filepath.Abs(dir)
	if err != nil {
		current = dir
	}

	for {
		if managers := lockfileManagers(current); len(managers) > 0 {
			return managers
		}
		if exists(current, ".git") {
			return nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func detectPythonFramework(dir string) Framework {
	raw := strings.ToLower(readProjectFiles(dir, "pyproject.toml", "requirements.txt", "setup.py", "Pipfile"))
	switch {
	case strings.Contains(raw, "fastapi"):
		return FrameworkFastAPI
	case strings.Contains(raw, "django"):
		return FrameworkDjango
	case strings.Contains(raw, "flask"):
		return FrameworkFlask
	default:
		return FrameworkGeneric
	}
}

func detectPHPFramework(dir string) Framework {
	raw := strings.ToLower(readProjectFiles(dir, "composer.json"))
	if strings.Contains(raw, "laravel/framework") {
		return FrameworkLaravel
	}
	return FrameworkGeneric
}

func readProjectFiles(dir string, names ...string) string {
	var combined strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			combined.Write(raw)
			combined.WriteByte('\n')
		}
	}
	return combined.String()
}

func declaredPackageManager(pkg map[string]any) (PackageManager, bool) {
	raw, ok := pkg["packageManager"].(string)
	if !ok {
		return "", false
	}
	name := strings.ToLower(strings.TrimSpace(strings.SplitN(raw, "@", 2)[0]))
	switch PackageManager(name) {
	case PMNpm, PMPnpm, PMYarn, PMBun:
		return PackageManager(name), true
	default:
		return "", false
	}
}

// lockfileManagers returns every manager with a lockfile, ordered by
// specificity: bun and pnpm lockfiles are deliberate choices, package-lock is
// often incidental, yarn between.
func lockfileManagers(dir string) []PackageManager {
	var managers []PackageManager

	if exists(dir, "bun.lock") || exists(dir, "bun.lockb") {
		managers = append(managers, PMBun)
	}

	if exists(dir, "pnpm-lock.yaml") {
		managers = append(managers, PMPnpm)
	}

	if exists(dir, "yarn.lock") {
		managers = append(managers, PMYarn)
	}

	if exists(dir, "package-lock.json") {
		managers = append(managers, PMNpm)
	}

	return managers
}

// InstallArgs returns the command + args to add a dependency with this manager.
func (m PackageManager) InstallArgs(pkg string) []string {
	switch m {
	case PMBun:
		return []string{"bun", "add", pkg}
	case PMPnpm:
		return []string{"pnpm", "add", pkg}
	case PMYarn:
		return []string{"yarn", "add", pkg}
	default:
		return []string{"npm", "install", pkg}
	}
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))

	return err == nil
}

func readPackageJSON(dir string) map[string]any {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}

	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil
	}

	return pkg
}

func hasDep(pkg map[string]any, name string) bool {
	for _, section := range []string{"dependencies", "devDependencies"} {
		deps, ok := pkg[section].(map[string]any)
		if !ok {
			continue
		}

		if _, ok := deps[name]; ok {
			return true
		}
	}

	return false
}
