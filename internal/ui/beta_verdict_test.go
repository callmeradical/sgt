package ui

// Test for slice beta-verdict of change claude-is-the-default-harness-and-
// runs-gauntlet-roles: specs/gauntlet-roles/spec.md, "HOLD records a
// blocking finding carrying the gap", through a real dispatch whose review
// runs as gauntlet:beta.

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/handoff"
)

// Scenario: HOLD records a blocking finding carrying the gap.
func TestADispatchedBetaHoldFailsTheReviewAndBlocksTheBulletWithTheGap(t *testing.T) {
	const gap = "the empty-input scenario has no test"
	f := newRolesFixture(t, "agent: $CLAUDE\nretries: 2", "factory:\n  pipeline: [build, review]")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *gauntlet:beta*) echo 'BETA-REASONING'; echo 'VERDICT: HOLD: " + gap + "' ;;\n" +
		"  *) echo 'built' ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(f.claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)
	if got := runStatus(t, f.st, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}

	phases, err := f.st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var reviews int
	for _, p := range phases {
		if p.Name != "review" || p.Kind != "agent" {
			continue
		}
		reviews++
		if p.Status != "failed" || p.Error != "gauntlet:beta HOLD: "+gap {
			t.Errorf("review record status/error = %q/%q, want failed/gauntlet:beta HOLD: %s", p.Status, p.Error, gap)
		}
		if !handoff.HasBlockingFinding(handoff.ReviewFindings(p.Payload)) {
			t.Errorf("review payload %s has no blocking finding", p.Payload)
		}
	}
	if reviews != 1 {
		t.Errorf("review records = %d, want 1 (retries: 2, a HOLD is not retried)", reviews)
	}

	envs, err := f.st.ListEnvelopesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var blocking bool
	for _, e := range envs {
		if e.Stage == "review" && handoff.HasBlockingFinding(handoff.ReviewFindings(e.Data)) {
			blocking = true
		}
	}
	if !blocking {
		t.Error("no recorded review envelope carries a blocking finding")
	}

	run, err := f.st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	reason := srvBlockedReason(t, f.mux, f.st, run.IntentID)
	if !strings.Contains(reason, gap) {
		t.Errorf("bullet blocked_reason = %q, want it to contain the gap %q", reason, gap)
	}
	if strings.Contains(reason, "BETA-REASONING") {
		t.Errorf("bullet blocked_reason = %q carries beta's output", reason)
	}
}
