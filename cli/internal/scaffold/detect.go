// Package scaffold implements `reevit init`: it detects the project's stack,
// installs the matching SDK, wires environment variables, and writes
// integration starter files from embedded templates.
package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	Root    string
	Stack   Stack
	Manager PackageManager
	// TypeScript is true when a tsconfig exists (JS stacks only).
	TypeScript bool
	// SrcDir is true when a Next.js project keeps its app under src/.
	SrcDir bool
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

		return p
	}

	if exists(dir, "composer.json") {
		p.Stack = StackPHP

		return p
	}

	if exists(dir, "pyproject.toml") || exists(dir, "requirements.txt") || exists(dir, "setup.py") {
		p.Stack = StackPython

		return p
	}

	pkg := readPackageJSON(dir)
	if pkg == nil {
		return p
	}

	p.TypeScript = exists(dir, "tsconfig.json")
	p.SrcDir = exists(dir, "src/app") || (exists(dir, "src") && !exists(dir, "app") && hasDep(pkg, "next"))
	p.Managers = lockfileManagers(dir)

	if len(p.Managers) > 0 {
		p.Manager = p.Managers[0]
	}

	switch {
	case hasDep(pkg, "next"):
		p.Stack = StackNext
	case hasDep(pkg, "@sveltejs/kit") || hasDep(pkg, "svelte"):
		p.Stack = StackSvelte
	case hasDep(pkg, "vue") || hasDep(pkg, "nuxt"):
		p.Stack = StackVue
	case hasDep(pkg, "react") || hasDep(pkg, "react-dom"):
		p.Stack = StackReact
	default:
		p.Stack = StackNode
	}

	return p
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
