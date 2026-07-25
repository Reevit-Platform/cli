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
	ExistingFiles ExistingFilesPolicy
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
	if !validExistingFilesPolicy(options.ExistingFiles) {
		return fmt.Errorf("invalid existing-files policy %q", options.ExistingFiles)
	}
	conflicts, err := ConflictPaths(project, targets, manifest)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 && options.ExistingFiles == ExistingFilesReject {
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

func validExistingFilesPolicy(policy ExistingFilesPolicy) bool {
	switch policy {
	case ExistingFilesReject, ExistingFilesKeep, ExistingFilesOverwrite, ExistingFilesFresh:
		return true
	default:
		return false
	}
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
			if err := validateOutputPath(project.Root, output); err != nil {
				return nil, err
			}
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

func validateOutputPath(root, output string) error {
	clean := filepath.Clean(strings.TrimSpace(output))
	if clean == "." || filepath.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output path %q must stay inside the project", output)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	destination := filepath.Join(rootAbs, clean)
	relative, err := filepath.Rel(rootAbs, destination)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output path %q must stay inside the project", output)
	}

	current := rootAbs
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return fmt.Errorf("inspect output path %s: %w", output, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output path %q traverses a symbolic link", output)
		}
	}
	return nil
}
