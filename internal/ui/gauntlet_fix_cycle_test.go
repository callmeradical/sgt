package ui

// Tests for the fix-cycle threading of claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 4, tasks 4.2 and 6.2): runFixCycles
// carries the caller's preflight result onto its fix phase and re-run stage,
// and each cycle's engine stamps that cycle on every phase it records.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
)

const markerGate = "factory:\n  pipeline: [test]\n  gates:\n    unit: \"test -f .sgt/fixed.marker\""

// dispatchFailingGate dispatches the fixture and waits for the run to fail
// on its gate, before any fix phase has had a chance to create the marker.
func (f *rolesFixture) dispatchFailingGate(t *testing.T) string {
	t.Helper()
	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)
	if got := runStatus(t, f.st, runID); got != "failed" {
		t.Fatalf("run reached %q after dispatch; the gate must fail before the fix", got)
	}
	// The terminal status is written before the run goroutine releases the
	// run; a fix requested in between is refused as "already executing".
	deadline := time.Now().Add(10 * time.Second)
	for f.srv.isRunActive(runID) {
		if time.Now().After(deadline) {
			t.Fatalf("run %s was never released after reaching failed", runID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return runID
}

// The gate a corrective cycle re-runs records that cycle, not 0.
func TestAGateReRunByAFixCycleRecordsItsCycle(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", markerGate)
	runID := f.dispatchFailingGate(t)
	t.Setenv("STUB_TOUCH", ".sgt/fixed.marker")

	w := postJSON(t, f.mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d; body=%s", w.Code, w.Body.String())
	}
	if status := waitForRunFixTerminal(t, f.st, runID); status != "passed" {
		t.Fatalf("run status = %q, want passed once cycle 1's fix creates the marker", status)
	}

	phases, err := f.st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var passedGate bool
	for _, p := range phases {
		if p.Name == "unit" && p.Status == "passed" {
			passedGate = true
			if p.FixCycle != 1 {
				t.Errorf("the gate re-run by cycle 1 recorded fix cycle %d, want 1", p.FixCycle)
			}
		}
	}
	if !passedGate {
		t.Fatalf("no passing unit gate recorded: %+v", phases)
	}
}

// runFixCycles runs its fix phase with the roles and version it was given.
func TestAFixCycleRunsItsFixPhaseWithTheCallersRoles(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", markerGate)
	runID := f.dispatchFailingGate(t)
	t.Setenv("STUB_TOUCH", ".sgt/fixed.marker")

	proj, err := config.LoadProject("roles")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.srv.registerRun(runID, cancel)
	f.srv.runFixCycles(ctx, cancel, proj, runID, "svc", preflightResult{Roles: true, Version: "0.1.0"})

	if got := runStatus(t, f.st, runID); got != "passed" {
		t.Fatalf("run status = %q after the fix cycle, want passed", got)
	}
	fix := onlyCall(t, f.claudeCalls(t))
	if role, ok := argAfter(fix, "--agent"); !ok || role != "gauntlet:alpha" {
		t.Errorf("fix ran with --agent %q (present=%v), want gauntlet:alpha", role, ok)
	}
	agent, role, version := provenanceOf(t, phaseRecord(t, f.st, runID, "fix"))
	if agent != f.claude || role != "gauntlet:alpha" || version != "0.1.0" {
		t.Errorf("fix payload agent/role/plugin_version = %v/%v/%v, want %s/gauntlet:alpha/0.1.0", agent, role, version, f.claude)
	}
	if rec := phaseRecord(t, f.st, runID, "fix"); rec.FixCycle != 1 {
		t.Errorf("fix phase recorded fix cycle %d, want 1", rec.FixCycle)
	}
}

// onlyCall returns the single claude invocation of a run whose only agent
// phase is its fix phase (a fix prompt carries no phase-objective header to
// find it by).
func onlyCall(t *testing.T, calls [][]string) []string {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one claude invocation (the fix phase), got %d", len(calls))
	}
	return calls[0]
}
