package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

// a-failed-gate-is-corrected-in-place: PhaseRunner.FixCycle stamps every
// phase it records, for both RunCodeGate and RunAgentPhase, so a corrective
// cycle's phases are distinguishable from the run's own original phases
// (FixCycle 0) and from each other.

func TestRunCodeGateRecordsFixCycle(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateRun(&store.RunRecord{ID: "run-1", Project: "p", TaskID: "run-1", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	// A plain PhaseRunner (FixCycle left at its zero value) must record cycle
	// 0 — no behaviour change for a normal dispatch or plain Resume.
	plain := &PhaseRunner{Store: st, Router: handoff.NewRouter(filepath.Join(dir, "handoff")), Worktree: dir, RepoName: "svc", RunID: "run-1"}
	if _, err := plain.RunCodeGate(context.Background(), "unit-tests", "echo ok"); err != nil {
		t.Fatal(err)
	}

	fixer := &PhaseRunner{Store: st, Router: handoff.NewRouter(filepath.Join(dir, "handoff")), Worktree: dir, RepoName: "svc", RunID: "run-1", FixCycle: 2}
	if _, err := fixer.RunCodeGate(context.Background(), "unit-tests-2", "echo ok"); err != nil {
		t.Fatal(err)
	}

	phases, err := st.ListPhasesForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]store.PhaseRecord{}
	for _, p := range phases {
		byName[p.Name] = p
	}
	if got := byName["unit-tests"].FixCycle; got != 0 {
		t.Errorf("plain PhaseRunner's gate phase FixCycle = %d, want 0", got)
	}
	if got := byName["unit-tests-2"].FixCycle; got != 2 {
		t.Errorf("fixer PhaseRunner's gate phase FixCycle = %d, want 2", got)
	}
}

func TestRunAgentPhaseRecordsFixCycleOnEveryAttempt(t *testing.T) {
	dir := t.TempDir()
	agent := fakeAgent(t, dir, "agent.sh", "echo done")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.FixCycle = 3

	if _, _, err := pr.RunAgentPhase(context.Background(), "fix", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	phases, err := st.ListPhasesForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) == 0 {
		t.Fatal("expected at least one recorded phase")
	}
	for _, p := range phases {
		if p.FixCycle != 3 {
			t.Errorf("phase %s (status %s) FixCycle = %d, want 3", p.ID, p.Status, p.FixCycle)
		}
	}
}
