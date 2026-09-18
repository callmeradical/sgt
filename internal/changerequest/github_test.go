package changerequest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGithubProvider_Create(t *testing.T) {
	tests := []struct {
		name       string
		ghScript   string
		wantURL    string
		wantErrStr string
	}{
		{
			name: "success",
			ghScript: `#!/bin/sh
echo "https://github.com/example/repo/pull/1"
`,
			wantURL: "https://github.com/example/repo/pull/1",
		},
		{
			name: "gh error",
			ghScript: `#!/bin/sh
echo "GraphQL: Not Found" >&2
exit 1
`,
			wantErrStr: "gh pr create:",
		},
		{
			name: "invalid URL format",
			ghScript: `#!/bin/sh
echo "just some output"
`,
			wantErrStr: "gh pr create did not return a URL: just some output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			fakeGH := filepath.Join(binDir, "gh")
			if err := os.WriteFile(fakeGH, []byte(tt.ghScript), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			provider := &githubProvider{}
			repoDir := t.TempDir()

			url, err := provider.Create(context.Background(), repoDir, "main", "feature", "title", "body")
			if tt.wantErrStr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrStr)
				}
				if !strings.Contains(err.Error(), tt.wantErrStr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErrStr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("got URL %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestGithubProvider_Status(t *testing.T) {
	tests := []struct {
		name       string
		ghScript   string
		wantMerged bool
		wantBranch string
		wantErrStr string
	}{
		{
			name: "merged",
			ghScript: `#!/bin/sh
echo '{"state":"MERGED","mergedAt":"2023-01-01T00:00:00Z","baseRefName":"main"}'
`,
			wantMerged: true,
			wantBranch: "main",
		},
		{
			name: "open",
			ghScript: `#!/bin/sh
echo '{"state":"OPEN","mergedAt":"","baseRefName":"main"}'
`,
			wantMerged: false,
			wantBranch: "main",
		},
		{
			name: "gh error",
			ghScript: `#!/bin/sh
echo "Not Found" >&2
exit 1
`,
			wantErrStr: "gh pr view",
		},
		{
			name: "invalid json",
			ghScript: `#!/bin/sh
echo 'not json'
`,
			wantErrStr: "parsing gh pr view output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			fakeGH := filepath.Join(binDir, "gh")
			if err := os.WriteFile(fakeGH, []byte(tt.ghScript), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			provider := &githubProvider{}
			repoDir := t.TempDir()

			res, err := provider.Status(context.Background(), repoDir, "https://github.com/example/repo/pull/1")
			if tt.wantErrStr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrStr)
				}
				if !strings.Contains(err.Error(), tt.wantErrStr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErrStr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Merged != tt.wantMerged {
				t.Errorf("got Merged %v, want %v", res.Merged, tt.wantMerged)
			}
			if res.MergedIntoBranch != tt.wantBranch {
				t.Errorf("got MergedIntoBranch %q, want %q", res.MergedIntoBranch, tt.wantBranch)
			}
		})
	}
}
