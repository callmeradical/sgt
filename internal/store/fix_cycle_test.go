package store

import (
	"path/filepath"
	"testing"
	"time"
)

// a-failed-gate-is-corrected-in-place: every phase records which corrective
// cycle it belongs to (0 = the run's own original attempt), so a run that
// went through several corrective cycles has each cycle's phases
// distinguishable from the original run and from each other.

func TestRecordPhasePersistsFixCycle(t *testing.T) {
	st, _ := openTestStore(t)

	if err := st.CreateRun(&RunRecord{
		ID: "run-fc-1", Project: "p", TaskID: "run-fc-1", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.RecordPhase(&PhaseRecord{
		ID: "p-original", RunID: "run-fc-1", Repo: "svc", Name: "test", Kind: "code", Status: "failed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPhase(&PhaseRecord{
		ID: "p-cycle1-fix", RunID: "run-fc-1", Repo: "svc", Name: "fix", Kind: "agent", Status: "passed", FixCycle: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPhase(&PhaseRecord{
		ID: "p-cycle2-fix", RunID: "run-fc-1", Repo: "svc", Name: "fix", Kind: "agent", Status: "passed", FixCycle: 2,
	}); err != nil {
		t.Fatal(err)
	}

	phases, err := st.ListPhasesForRun("run-fc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 3 {
		t.Fatalf("ListPhasesForRun returned %d phases, want 3", len(phases))
	}

	byID := map[string]PhaseRecord{}
	for _, p := range phases {
		byID[p.ID] = p
	}
	if got := byID["p-original"].FixCycle; got != 0 {
		t.Errorf("original phase FixCycle = %d, want 0", got)
	}
	if got := byID["p-cycle1-fix"].FixCycle; got != 1 {
		t.Errorf("cycle-1 phase FixCycle = %d, want 1", got)
	}
	if got := byID["p-cycle2-fix"].FixCycle; got != 2 {
		t.Errorf("cycle-2 phase FixCycle = %d, want 2", got)
	}
}

// A database created before this change has no phases.fix_cycle column.
// Reopening it must add the column, the same additive pattern as
// phases.attempt, and every pre-existing row must read back as cycle 0 (the
// run's own original attempt) rather than an unknown or NULL value.
func TestOpenAddsFixCycleColumnToAnOlderDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-fix-cycle.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	if _, err := st.db.Exec("DROP TABLE phases"); err != nil {
		t.Fatalf("dropping phases: %v", err)
	}
	if _, err := st.db.Exec(`CREATE TABLE phases (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		repo TEXT NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL,
		error TEXT,
		duration_ms INTEGER,
		payload TEXT,
		attempt INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("recreating legacy phases: %v", err)
	}
	if err := st.CreateRun(&RunRecord{ID: "old-run", Project: "p", TaskID: "old-run", Status: "passed"}); err != nil {
		t.Fatalf("seeding legacy run: %v", err)
	}
	if _, err := st.db.Exec(
		`INSERT INTO phases (id, run_id, repo, name, kind, status, duration_ms, attempt, created_at) VALUES ('old-phase', 'old-run', 'svc', 'test', 'code', 'passed', 0, 1, ?)`,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("seeding legacy phase: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	upgraded, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen of older database failed: %v", err)
	}
	defer upgraded.Close()

	if has, err := upgraded.hasColumn("phases", "fix_cycle"); err != nil || !has {
		t.Fatalf("phases.fix_cycle was not added on open (has=%v, err=%v)", has, err)
	}

	phases, err := upgraded.ListPhasesForRun("old-run")
	if err != nil {
		t.Fatalf("reading legacy phase after upgrade: %v", err)
	}
	if len(phases) != 1 {
		t.Fatalf("ListPhasesForRun = %+v, want 1 legacy phase", phases)
	}
	if phases[0].FixCycle != 0 {
		t.Errorf("legacy phase FixCycle = %d, want 0 (predates the feature; is the run's own original attempt)", phases[0].FixCycle)
	}
}
