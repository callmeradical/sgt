package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListProjectsExcludesReservedGlobalConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_CONFIG", tempDir)

	globalConfigYAML := `
dev_root: ~/Dev
default_identity: someone@example.com
`
	if err := os.WriteFile(filepath.Join(tempDir, GlobalConfigFileName), []byte(globalConfigYAML), 0o644); err != nil {
		t.Fatalf("writing global config: %v", err)
	}

	projectYAML := `
project: real-project
repos:
  backend:
    path: /tmp/test-backend
`
	if err := os.WriteFile(filepath.Join(tempDir, "real-project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatalf("writing project yaml: %v", err)
	}

	projects, err := ListProjects()
	if err != nil {
		t.Fatalf("ListProjects returned error: %v", err)
	}

	if len(projects) != 1 {
		t.Fatalf("expected exactly 1 project, got %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "real-project" {
		t.Fatalf("expected project named 'real-project', got %q", projects[0].Name)
	}
	for _, p := range projects {
		if p.Name == "config" {
			t.Fatalf("reserved global config file was incorrectly listed as a project: %+v", p)
		}
	}
}
