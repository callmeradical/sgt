package ui

// Tests for specs/gate-fix-loop/spec.md's dashboard scenario: corrective-
// cycle phases are grouped into their own labeled "Attempt N of M" blocks,
// distinct from the run's original phase sequence. fixCyclesHTML is pure
// JavaScript inside the embedded index.html, so — following this
// repository's established extractJSFunction pattern (stage_matrix_test.go,
// resume_dashboard_test.go) — it is lifted out and executed under real node
// against a real fixture rather than described in prose.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/store"
)

// renderFixCycles executes fixCyclesHTML out of the embedded UI asset.
func renderFixCycles(t *testing.T, phases []store.PhaseRecord, limit int) string {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot execute the embedded UI render logic")
	}
	src := embeddedIndex(t)

	parts := []string{
		extractJSFunction(t, src, "escapeHTML"),
		"const escapeAttr = escapeHTML;",
		extractJSBlock(t, src, "const PHASE_LOOK = {"),
		extractJSFunction(t, src, "fixCyclesHTML"),
	}

	phasesJSON, err := json.Marshal(phases)
	if err != nil {
		t.Fatal(err)
	}

	harness := fmt.Sprintf("%s\n\nprocess.stdout.write(fixCyclesHTML(%s, %d));",
		strings.Join(parts, "\n\n"), phasesJSON, limit)

	return runNode(t, node, "fix-cycles.mjs", harness)
}

func phase(repo, name, kind, status string, fixCycle int) store.PhaseRecord {
	return store.PhaseRecord{Repo: repo, Name: name, Kind: kind, Status: status, FixCycle: fixCycle}
}

// A run with no corrective cycles (every phase at fix_cycle 0, the default
// zero value) renders nothing extra — this section is additive, not a
// mandatory element every run shows.
func TestFixCyclesHTMLRendersNothingForARunWithNoCorrectiveCycles(t *testing.T) {
	phases := []store.PhaseRecord{
		phase("svc", "build", "agent", "passed", 0),
		phase("svc", "test", "code", "passed", 0),
	}

	html := renderFixCycles(t, phases, 5)

	if strings.TrimSpace(html) != "" {
		t.Errorf("expected no output for a run with no corrective cycles, got:\n%s", html)
	}
}

// A run with two corrective cycles groups them into two distinct, correctly
// labeled blocks — the requirement being tested is the grouping and the
// labeling, not any specific markup shape.
func TestFixCyclesHTMLGroupsMultipleCyclesIntoDistinctLabeledBlocks(t *testing.T) {
	phases := []store.PhaseRecord{
		phase("svc", "test", "code", "failed", 0), // the original run, not part of any cycle
		phase("svc", "fix", "agent", "passed", 1),
		phase("svc", "test", "code", "failed", 1),
		phase("svc", "fix", "agent", "passed", 2),
		phase("svc", "build", "agent", "passed", 2),
		phase("svc", "test", "code", "passed", 2),
	}

	html := renderFixCycles(t, phases, 5)

	if !strings.Contains(html, "Attempt 1 of 5") {
		t.Errorf("missing label for cycle 1:\n%s", html)
	}
	if !strings.Contains(html, "Attempt 2 of 5") {
		t.Errorf("missing label for cycle 2:\n%s", html)
	}

	// Cycle 1's block appears before cycle 2's, matching cycle order.
	idx1 := strings.Index(html, "Attempt 1 of 5")
	idx2 := strings.Index(html, "Attempt 2 of 5")
	if idx1 < 0 || idx2 < 0 || idx1 > idx2 {
		t.Errorf("cycle blocks are not in ascending order:\n%s", html)
	}

	// Cycle 2's real repeated phase sequence (fix → build → test) is shown
	// connected, not as an unconnected list.
	block2 := html[idx2:]
	fixIdx := strings.Index(block2, "fix")
	buildIdx := strings.Index(block2, "build")
	testIdx := strings.Index(block2, "test")
	if fixIdx < 0 || buildIdx < 0 || testIdx < 0 || !(fixIdx < buildIdx && buildIdx < testIdx) {
		t.Errorf("cycle 2's phase sequence fix→build→test is not shown in order:\n%s", block2)
	}
	if strings.Count(block2, "→") == 0 {
		t.Errorf("cycle 2's phases are not shown as a connected chain:\n%s", block2)
	}

	// The original run's own fix_cycle-0 phase must not be pulled into
	// either corrective block: exactly 2 blocks exist, one per cycle.
	if n := strings.Count(html, "mb-2 last:mb-0"); n != 2 {
		t.Errorf("expected exactly 2 attempt blocks, got %d:\n%s", n, html)
	}
}

// When no configured bound is supplied, M falls back to the highest cycle
// actually observed rather than rendering an empty or literal "undefined".
func TestFixCyclesHTMLFallsBackToTheHighestObservedCycleWhenNoLimitIsGiven(t *testing.T) {
	phases := []store.PhaseRecord{
		phase("svc", "fix", "agent", "passed", 1),
		phase("svc", "test", "code", "passed", 1),
	}

	html := renderFixCycles(t, phases, 0)

	if !strings.Contains(html, "Attempt 1 of 1") {
		t.Errorf("expected the bound to fall back to the highest observed cycle (1):\n%s", html)
	}
	if strings.Contains(html, "undefined") {
		t.Errorf("rendered a literal undefined instead of a fallback bound:\n%s", html)
	}
}

// A failed phase within a cycle must still render distinguishably (a
// corrective cycle can itself fail partway through before the next cycle
// starts), not silently look identical to a passed one.
func TestFixCyclesHTMLDistinguishesFailedPhasesWithinACycle(t *testing.T) {
	phases := []store.PhaseRecord{
		phase("svc", "fix", "agent", "passed", 1),
		phase("svc", "test", "code", "failed", 1),
	}

	html := renderFixCycles(t, phases, 3)

	if !strings.Contains(html, "text-rose-400") {
		t.Errorf("a failed phase within a cycle should render with the failed style:\n%s", html)
	}
}
