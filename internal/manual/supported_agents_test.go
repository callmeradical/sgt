package manual

import (
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/runner"
)

func supportedAgentsSection(t *testing.T) string {
	t.Helper()
	for _, s := range Sections() {
		if s.Title == "Supported agents" {
			return s.Body
		}
	}
	t.Fatal(`the manual has no "Supported agents" section`)
	return ""
}

// The Supported agents section has a row for every harness the engine can
// drive (PRD R6, tasks.md 10.1). The expected names come from
// runner.SupportedAgents itself, so adding a harness there without
// documenting it fails this test.
func TestSupportedAgentsSectionNamesEveryRunnerSupportedAgent(t *testing.T) {
	body := supportedAgentsSection(t)
	if len(runner.SupportedAgents) == 0 {
		t.Fatal("runner.SupportedAgents is empty")
	}
	for _, agent := range runner.SupportedAgents {
		if !strings.Contains(body, "| `"+agent+"` |") {
			t.Errorf("Supported agents section has no table row for %q:\n%s", agent, body)
		}
	}
}

// claude is marked the default and the only harness that runs gauntlet
// roles, and the section lists what every harness gets.
func TestSupportedAgentsSectionMarksClaudeAndListsEveryHarnessBehaviour(t *testing.T) {
	body := supportedAgentsSection(t)
	for _, want := range []string{
		"| `claude` | default |",
		"the only harness that runs gauntlet roles",
		"Every gate in a repo re-runs after a fix",
		"Reviews see the run's whole diff",
		"A review with a blocking finding is recorded `failed`",
		"A blocked bullet shows its failed phase's own error",
		"`sgt run` follows the dispatch path",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Supported agents section does not contain %q", want)
		}
	}
	for _, agent := range runner.SupportedAgents {
		if agent == "claude" {
			continue
		}
		if strings.Contains(body, "| `"+agent+"` | default |") {
			t.Errorf("Supported agents section marks %q as the default", agent)
		}
	}
}
