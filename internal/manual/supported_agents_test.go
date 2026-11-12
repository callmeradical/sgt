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

// The review diff cap differs by harness (design decision 6, Size): 256 KiB
// for claude, whose large prompts go on stdin; for every other harness the
// whole prompt stays under 120 KiB, with the diff up to 96 KiB. The
// every-harness list must not state the claude cap as everyone's.
func TestSupportedAgentsSectionStatesBothReviewDiffCaps(t *testing.T) {
	body := supportedAgentsSection(t)
	for _, want := range []string{"256 KiB", "120 KiB", "96 KiB", "names the cap that applied"} {
		if !strings.Contains(body, want) {
			t.Errorf("Supported agents section does not contain %q", want)
		}
	}
}

// Only a dispatch request's `agent` is validated (runner.ValidateAgent's one
// caller is handleDispatch, on req.Agent); a project's defaults.agent is not
// checked up front. The section must say so rather than claim every
// unlisted name is refused.
func TestSupportedAgentsSectionSaysOnlyTheDispatchAgentIsValidated(t *testing.T) {
	// Collapse line wrapping, so a sentence is found wherever it breaks.
	body := strings.Join(strings.Fields(supportedAgentsSection(t)), " ")
	if strings.Contains(body, "A name not in this table is refused.") {
		t.Error("Supported agents section claims every unlisted name is refused")
	}
	for _, want := range []string{
		"an `agent` on a dispatch request that is not in this table is refused",
		"`defaults.agent` is not checked up front",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Supported agents section does not contain %q", want)
		}
	}
}
