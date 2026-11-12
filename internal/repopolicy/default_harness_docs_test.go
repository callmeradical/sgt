package repopolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The README and the sgt-setup skill tell an operator the same thing the
// manual does: claude is the default harness, build/fix/review run as
// gauntlet roles, and how to opt out, pin another harness, and install the
// plugin (tasks.md 10.1).
func TestOperatorDocsStateClaudeIsTheDefaultHarness(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"README.md",
		filepath.Join(".agents", "skills", "sgt-setup", "SKILL.md"),
	} {
		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatalf("reading %s: %v", rel, err)
			}
			text := string(body)
			for _, want := range []string{
				"The default agent harness is claude.",
				"gauntlet_roles: false",
				"defaults.agent",
				"gauntlet:alpha",
				"gauntlet:beta",
				"/plugin install gauntlet@gauntlet",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("%s does not contain %q", rel, want)
				}
			}
		})
	}
}
