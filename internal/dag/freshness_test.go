package dag

// Tests for specs/verification-freshness/spec.md, requirement "A verification
// counts only if it passed after the last code change" (claude-is-the-default-
// harness-and-runs-gauntlet-roles, design decision 8): the shared rule
// dag.PhasePassed over seeded records, and the engine's resume skips that use
// it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// seq builds phase records in the order given, each stamped one millisecond
// after the previous, the order RecordPhase would have written them in.
type seq struct {
	t0   time.Time
	recs []store.PhaseRecord
}

func newSeq() *seq { return &seq{t0: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)} }

func (s *seq) add(repo, name, kind, status string) *seq {
	s.recs = append(s.recs, store.PhaseRecord{
		ID:        fmt.Sprintf("seed-%d-%s-%s", len(s.recs), name, status),
		Repo:      repo,
		Name:      name,
		Kind:      kind,
		Status:    status,
		CreatedAt: s.t0.Add(time.Duration(len(s.recs)) * time.Millisecond),
	})
	return s
}

func TestPhasePassedAppliesTheFreshnessTable(t *testing.T) {
	defaultWithReview := []string{"plan", "build", "test", "review"}

	cases := []struct {
		name     string
		recs     *seq
		repo     string
		phase    string
		kind     string
		pipeline []string
		want     bool
	}{
		{
			name: "no record is not passed",
			recs: newSeq(),
			repo: "svc", phase: "build", kind: "agent", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a work phase whose newest record passed counts",
			recs: newSeq().add("svc", "build", "agent", "failed").add("svc", "build", "agent", "passed"),
			repo: "svc", phase: "build", kind: "agent", pipeline: defaultWithReview,
			want: true,
		},
		{
			name: "a work phase whose newest record failed does not count, even after an earlier pass",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "build", "agent", "failed"),
			repo: "svc", phase: "build", kind: "agent", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a work phase is not judged against later code changes",
			recs: newSeq().add("svc", "plan", "agent", "passed").add("svc", "build", "agent", "passed").
				add("svc", "fix", "agent", "passed"),
			repo: "svc", phase: "plan", kind: "agent", pipeline: defaultWithReview,
			want: true,
		},
		{
			name: "a gate that passed after the last build counts",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: true,
		},
		{
			name: "a gate whose newest record failed does not count",
			recs: newSeq().add("svc", "unit", "code", "passed").add("svc", "unit", "code", "failed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a fix after a gate pass makes it stale",
			recs: newSeq().add("svc", "unit", "code", "passed").add("svc", "fix", "agent", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a failed fix after a gate pass makes it stale too",
			recs: newSeq().add("svc", "unit", "code", "passed").add("svc", "fix", "agent", "failed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a gate that passed again after a failed fix counts",
			recs: newSeq().add("svc", "unit", "code", "passed").add("svc", "fix", "agent", "failed").
				add("svc", "unit", "code", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: true,
		},
		{
			name: "another repo's fix does not make this repo's gate stale",
			recs: newSeq().add("svc", "unit", "code", "passed").add("web", "fix", "agent", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: defaultWithReview,
			want: true,
		},
		{
			// Scenario: An agent phase before test makes earlier gate passes stale.
			name: "an agent phase placed before test makes an earlier gate pass stale",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").
				add("svc", "docs", "agent", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: []string{"build", "docs", "test"},
			want: false,
		},
		{
			name: "an agent phase placed after test does not make the gates stale",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").
				add("svc", "docs", "agent", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: []string{"build", "test", "docs"},
			want: true,
		},
		{
			name: "a review placed before test does not make the gates stale",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").
				add("svc", "review", "agent", "passed"),
			repo: "svc", phase: "unit", kind: "code", pipeline: []string{"build", "review", "test"},
			want: true,
		},
		{
			name: "the placeholder test gate is judged like any gate",
			recs: newSeq().add("svc", "test", "code", "passed").add("svc", "build", "agent", "passed"),
			repo: "svc", phase: "test", kind: "code", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a review that passed after the build counts",
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").
				add("svc", "review", "agent", "passed"),
			repo: "svc", phase: "review", kind: "agent", pipeline: defaultWithReview,
			want: true,
		},
		{
			name: "a build newer than the review makes the review stale",
			recs: newSeq().add("svc", "review", "agent", "passed").add("svc", "build", "agent", "passed"),
			repo: "svc", phase: "review", kind: "agent", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a fix newer than the review makes the review stale",
			recs: newSeq().add("svc", "review", "agent", "passed").add("svc", "fix", "agent", "failed"),
			repo: "svc", phase: "review", kind: "agent", pipeline: defaultWithReview,
			want: false,
		},
		{
			name: "a gate run after the review is not a code change",
			recs: newSeq().add("svc", "review", "agent", "passed").add("svc", "unit", "code", "passed"),
			repo: "svc", phase: "review", kind: "agent", pipeline: defaultWithReview,
			want: true,
		},
		{
			// Scenario: A gate named like a phase is not confused with it.
			name: "a passed gate named build does not make the agent build phase count",
			recs: newSeq().add("svc", "build", "agent", "failed").add("svc", "build", "code", "passed"),
			repo: "svc", phase: "build", kind: "agent", pipeline: []string{"build", "test"},
			want: false,
		},
		{
			name: "the gate named build is judged on its own records",
			recs: newSeq().add("svc", "build", "agent", "failed").add("svc", "build", "code", "passed"),
			repo: "svc", phase: "build", kind: "code", pipeline: []string{"build", "test"},
			want: true,
		},
		{
			name: "a failed gate named review does not shadow a passed review phase",
			recs: newSeq().add("svc", "review", "agent", "passed").add("svc", "review", "code", "failed"),
			repo: "svc", phase: "review", kind: "agent", pipeline: []string{"build", "review", "test"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PhasePassed(tc.recs.recs, tc.repo, tc.phase, tc.kind, tc.pipeline)
			if got != tc.want {
				t.Errorf("PhasePassed(%s/%s %s, pipeline %v) = %v, want %v; records: %+v",
					tc.repo, tc.phase, tc.kind, tc.pipeline, got, tc.want, tc.recs.recs)
			}
		})
	}
}

// The newest record is decided by when it was written, not by where it sits
// in the slice a caller hands over.
func TestPhasePassedOrdersByCreatedAtNotSlicePosition(t *testing.T) {
	recs := newSeq().add("svc", "unit", "code", "passed").add("svc", "fix", "agent", "passed").recs
	reversed := []store.PhaseRecord{recs[1], recs[0]}
	if PhasePassed(reversed, "svc", "unit", "code", []string{"build", "test"}) {
		t.Error("PhasePassed counted a gate pass written before a fix because the fix came first in the slice")
	}
}

// ---- Engine.RunStage with Resume -------------------------------------------

// failingAgent writes an agent CLI stub that exits 1 and records that it ran,
// so a test can prove a phase was or was not invoked.
func failingAgent(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "bin", "goose")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func seedPhases(t *testing.T, e *Engine, runID string, recs []store.PhaseRecord) {
	t.Helper()
	for i := range recs {
		rec := recs[i]
		rec.RunID = runID
		if err := e.Store.RecordPhase(&rec); err != nil {
			t.Fatal(err)
		}
	}
}

func recordsNamed(t *testing.T, e *Engine, runID, name, kind string) []store.PhaseRecord {
	t.Helper()
	phases, err := e.Store.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// Scenario: Resume does not re-run passed work phases.
func TestResumeDoesNotRerunPassedWorkPhases(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(tempDir, "fleet"))
	repoDir := filepath.Join(tempDir, "svc")
	newGitRepo(t, repoDir)

	proj := &config.Project{
		Name:     "p",
		Defaults: config.ProjectDefaults{Agent: failingAgent(t, tempDir)}, // fails if plan or build re-runs
		Repos: map[string]config.Repo{
			"svc": {Path: repoDir, Factory: &config.FactoryConfig{
				Pipeline: []string{"plan", "build", "test"},
				Gates:    map[string]string{"unit": "true"},
			}},
		},
	}
	e := newEngine(t, proj)
	e.Resume = true
	createTestRun(t, e, "p", "run-work", "failed")
	seedPhases(t, e, "run-work", newSeq().
		add("svc", "plan", "agent", "passed").
		add("svc", "build", "agent", "passed").
		add("svc", "unit", "code", "failed").recs)

	if err := e.RunStage(context.Background(), "run-work", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage on resume: %v; a passed plan or build was re-run and its failing agent failed the stage", err)
	}
	for _, name := range []string{"plan", "build"} {
		if n := len(recordsNamed(t, e, "run-work", name, "agent")); n != 1 {
			t.Errorf("%s has %d records after resume, want the 1 seeded record: a passed work phase must not re-run", name, n)
		}
	}
	if n := len(recordsNamed(t, e, "run-work", "unit", "code")); n != 2 {
		t.Errorf("unit gate has %d records after resume, want 2: the failed gate must re-run", n)
	}
}

// Scenario: A gate named like a phase is not confused with it — at the
// engine: the agent build phase re-runs on resume although a gate named
// build passed.
func TestResumeRerunsAFailedAgentPhaseDespiteAPassedGateOfTheSameName(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(tempDir, "fleet"))
	repoDir := filepath.Join(tempDir, "svc")
	newGitRepo(t, repoDir)

	agent := filepath.Join(tempDir, "bin", "goose")
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	proj := &config.Project{
		Name:     "p",
		Defaults: config.ProjectDefaults{Agent: agent},
		Repos: map[string]config.Repo{
			"svc": {Path: repoDir, Factory: &config.FactoryConfig{
				Pipeline: []string{"build", "test"},
				Gates:    map[string]string{"build": "true"},
			}},
		},
	}
	e := newEngine(t, proj)
	e.Resume = true
	createTestRun(t, e, "p", "run-named", "failed")
	seedPhases(t, e, "run-named", newSeq().
		add("svc", "build", "agent", "failed").
		add("svc", "build", "code", "passed").recs)

	if err := e.RunStage(context.Background(), "run-named", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage on resume: %v", err)
	}
	agentRecs := recordsNamed(t, e, "run-named", "build", "agent")
	if len(agentRecs) != 2 {
		t.Fatalf("agent build has %d records after resume, want 2: the passed build gate was taken for the failed build phase", len(agentRecs))
	}
	if got := agentRecs[len(agentRecs)-1].Status; got != "passed" {
		t.Errorf("re-run agent build status = %q, want passed", got)
	}
	// The re-run build is a code change, so the build gate re-runs after it.
	if n := len(recordsNamed(t, e, "run-named", "build", "code")); n != 2 {
		t.Errorf("build gate has %d records after resume, want 2: the re-run build made its earlier pass stale", n)
	}
}

// A gate whose pass predates a fix re-runs on resume; one that passed after
// the newest code change is still skipped.
func TestResumeRerunsOnlyStaleGates(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(tempDir, "fleet"))
	repoDir := filepath.Join(tempDir, "svc")
	newGitRepo(t, repoDir)

	proj := &config.Project{
		Name: "p",
		Repos: map[string]config.Repo{
			"svc": {Path: repoDir, Factory: &config.FactoryConfig{
				Pipeline: []string{"test"},
				Gates:    map[string]string{"a-stale": "true", "b-fresh": "exit 1"}, // b-fresh fails if wrongly re-run
			}},
		},
	}
	e := newEngine(t, proj)
	e.Resume = true
	createTestRun(t, e, "p", "run-stale", "failed")
	seedPhases(t, e, "run-stale", newSeq().
		add("svc", "a-stale", "code", "passed").
		add("svc", "fix", "agent", "failed").
		add("svc", "b-fresh", "code", "passed").recs)

	if err := e.RunStage(context.Background(), "run-stale", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage on resume: %v; the fresh gate b-fresh was re-run", err)
	}
	if n := len(recordsNamed(t, e, "run-stale", "a-stale", "code")); n != 2 {
		t.Errorf("a-stale has %d records after resume, want 2: its pass predates the fix", n)
	}
	if n := len(recordsNamed(t, e, "run-stale", "b-fresh", "code")); n != 1 {
		t.Errorf("b-fresh has %d records after resume, want 1: it passed after the fix", n)
	}
}
