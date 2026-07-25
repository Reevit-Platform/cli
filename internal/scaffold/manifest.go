package scaffold

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type GeneratedEdit struct {
	Path      string   `json:"path"`
	Kind      string   `json:"kind"`
	Fragments []string `json:"fragments"`
}

type Manifest struct {
	Status         string          `json:"status"`
	CLIVersion     string          `json:"cli_version"`
	ProjectID      string          `json:"project_id"`
	Adapter        string          `json:"adapter"`
	Capabilities   []string        `json:"capabilities,omitempty"`
	ServerKeyID    string          `json:"server_key_id,omitempty"`
	CheckoutKeyID  string          `json:"checkout_key_id,omitempty"`
	Origin         string          `json:"origin,omitempty"`
	GeneratedFiles []string        `json:"generated_files,omitempty"`
	GeneratedEdits []GeneratedEdit `json:"generated_edits,omitempty"`
}

func ReadManifest(project Project) (Manifest, error) {
	path := filepath.Join(project.Root, ".reevit", "manifest.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("read Reevit manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse Reevit manifest: %w", err)
	}
	return manifest, nil
}

func WriteManifest(project Project, manifest Manifest) error {
	dir := filepath.Join(project.Root, ".reevit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .reevit directory: %w", err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Reevit manifest: %w", err)
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, "manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create manifest temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set manifest permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, "manifest.json")); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	if _, err := ensureGitignored(project.Root, ".reevit/logs/"); err != nil {
		return err
	}
	return nil
}
