package repopolicy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/runner"
)

// sectionAfter returns the text from heading to the next heading of the same
// or higher level.
func sectionAfter(t *testing.T, text, heading string) string {
	t.Helper()
	i := strings.Index(text, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("no %q heading", heading)
	}
	rest := text[i+len(heading)+2:]
	level := strings.SplitN(heading, " ", 2)[0]
	for _, prefix := range []string{"\n# ", "\n## ", "\n### "} {
		if len(prefix)-2 > len(level) {
			break
		}
		if j := strings.Index(rest, prefix); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
}

// README and the sgt-setup skill each carry a Supported agents section with
// a row for every harness in runner.SupportedAgents (PRD R6, tasks.md 10.1),
// claude marked as the default.
func TestOperatorDocsListEverySupportedAgent(t *testing.T) {
	root := repoRoot(t)
	for _, doc := range []struct{ rel, heading string }{
		{"README.md", "### Supported agents"},
		{filepath.Join(".agents", "skills", "sgt-setup", "SKILL.md"), "### Supported agents"},
	} {
		t.Run(doc.rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, doc.rel))
			if err != nil {
				t.Fatalf("reading %s: %v", doc.rel, err)
			}
			section := sectionAfter(t, string(body), doc.heading)
			for _, agent := range runner.SupportedAgents {
				if !strings.Contains(section, "| `"+agent+"` |") {
					t.Errorf("%s Supported agents section has no row for %q", doc.rel, agent)
				}
			}
			if !strings.Contains(section, "| `claude` | default |") {
				t.Errorf("%s Supported agents section does not mark claude as the default", doc.rel)
			}
			if !strings.Contains(section, "the only harness that runs gauntlet roles") {
				t.Errorf("%s Supported agents section does not say claude is the only harness that runs gauntlet roles", doc.rel)
			}
		})
	}
}

// `mise run check` accepts every harness the engine can drive: with no
// harness present, its MISSING line names each one, and with only that
// harness on PATH, the check passes.
func TestMiseCheckAcceptsEverySupportedAgent(t *testing.T) {
	root := repoRoot(t)
	checkScript := writeExecutableScript(t, t.TempDir(), "check.sh", extractMiseTaskScript(t, root, "[tasks.check]"))
	run := func(agent string) (string, error) {
		dir := t.TempDir()
		for _, cmd := range []string{"git", "gh", "openspec"} {
			writeStub(t, dir, cmd, cmd+" version 1.0")
		}
		if agent != "" {
			writeStub(t, dir, agent, agent+" 1.0")
		}
		cmd := exec.Command(checkScript)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+":/usr/bin:/bin:/usr/sbin:/sbin",
			"MISE_PROJECT_ROOT="+root,
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, _ := run("")
	for _, agent := range runner.SupportedAgents {
		if !strings.Contains(out, " "+agent) {
			t.Errorf("the check's MISSING line does not name %q:\n%s", agent, out)
		}
	}
	for _, agent := range runner.SupportedAgents {
		if agent == "claude" {
			continue // covered, with its plugin stub, by TestMiseCheckValidatesV2EnginePrerequisites
		}
		t.Run(agent, func(t *testing.T) {
			out, err := run(agent)
			if err != nil || !strings.Contains(out, "ok   "+agent) {
				t.Errorf("the check did not accept %q:\n%s (err=%v)", agent, out, err)
			}
		})
	}
}
