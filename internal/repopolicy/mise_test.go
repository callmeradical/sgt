package repopolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractMiseTaskScript(t *testing.T) {
	root := t.TempDir()
	miseTomlContent := `
[tasks.build]
run = """
echo "building"
make
"""

[tasks.check]
run = """
echo "checking"
go test ./...
"""

[tasks.empty]
run = """
"""
`
	err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte(miseTomlContent), 0o644)
	if err != nil {
		t.Fatalf("failed to write mise.toml: %v", err)
	}

	tests := []struct {
		name       string
		header     string
		wantScript string
	}{
		{
			name:       "extract build task",
			header:     "[tasks.build]",
			wantScript: "echo \"building\"\nmake\n",
		},
		{
			name:       "extract check task",
			header:     "[tasks.check]",
			wantScript: "echo \"checking\"\ngo test ./...\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMiseTaskScript(t, root, tc.header)
			if got != tc.wantScript {
				t.Errorf("extractMiseTaskScript() = %q, want %q", got, tc.wantScript)
			}
		})
	}
}

func TestWriteExecutableScript(t *testing.T) {
	dir := t.TempDir()
	name := "test_script.sh"
	scriptContent := "#!/bin/sh\necho 'hello world'\n"

	path := writeExecutableScript(t, dir, name, scriptContent)

	if path != filepath.Join(dir, name) {
		t.Errorf("writeExecutableScript returned path %q, want %q", path, filepath.Join(dir, name))
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written script: %v", err)
	}
	if string(content) != scriptContent {
		t.Errorf("script content = %q, want %q", string(content), scriptContent)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written script: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("script permissions = %v, want 0755", info.Mode().Perm())
	}
}

func TestWriteStub(t *testing.T) {
	dir := t.TempDir()
	name := "stub_script.sh"
	printLine := "mock output"

	writeStub(t, dir, name, printLine)

	path := filepath.Join(dir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written stub: %v", err)
	}

	expectedContent := "#!/usr/bin/env bash\nprintf '%s\\n' \"mock output\"\n"
	if string(content) != expectedContent {
		t.Errorf("stub content = %q, want %q", string(content), expectedContent)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written stub: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("stub permissions = %v, want 0755", info.Mode().Perm())
	}
}
