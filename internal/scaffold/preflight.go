package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ConflictError struct {
	Paths []string
}

type PreflightOptions struct {
	AllowExistingOutputs bool
}

func (e *ConflictError) Error() string {
	return "existing files would be overwritten:\n  " + strings.Join(e.Paths, "\n  ")
}

// Preflight discovers every unmanaged output conflict before init performs
// authentication, platform bootstrap, package installation, or file writes.
func Preflight(project Project, targets []Target, manifest Manifest) error {
	return PreflightWithOptions(project, targets, manifest, PreflightOptions{})
}

func PreflightWithOptions(
	project Project,
	targets []Target,
	manifest Manifest,
	options PreflightOptions,
) error {
	conflicts, err := ConflictPaths(project, targets, manifest)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 && !options.AllowExistingOutputs {
		return &ConflictError{Paths: conflicts}
	}
	for _, target := range targets {
		for _, edit := range target.Edits {
			if err := validateFileEdit(project, edit); err != nil {
				return err
			}
		}
	}
	return nil
}

// ConflictPaths returns every unmanaged output that already exists.
func ConflictPaths(project Project, targets []Target, manifest Manifest) ([]string, error) {
	managed := make(map[string]struct{}, len(manifest.GeneratedFiles))
	for _, path := range manifest.GeneratedFiles {
		managed[filepath.Clean(path)] = struct{}{}
	}
	var conflicts []string
	for _, target := range targets {
		for _, output := range target.Files {
			clean := filepath.Clean(output)
			if _, ok := managed[clean]; ok {
				continue
			}
			if _, err := os.Stat(filepath.Join(project.Root, clean)); err == nil {
				conflicts = append(conflicts, filepath.ToSlash(clean))
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect %s: %w", clean, err)
			}
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}
