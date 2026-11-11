package dag

// Tests for slice false-pass-order of change claude-is-the-default-harness-
// and-runs-gauntlet-roles: specs/verification-freshness/spec.md, "A blocking
// review is not skipped on resume". A review with a blocking finding is
// recorded failed (design decision 1a, rule 6), so resuming the run re-runs
// it rather than skipping a review that only ever "passed" by exit code.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// Scenario: A blocking review is not skipped on resume.
func TestBlockingReviewIsNotSkippedOnResume(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(tempDir, "fleet"))

	repoDir := filepath.Join(tempDir, "svc")
	newGitRepo(t, repoDir)

	// The goose stub reviews according to $REVIEW_MODE_FILE: "blocking" writes
	// a severity error finding, anything else a clean review. Any other phase
	// (build) just succeeds.
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modeFile := filepath.Join(tempDir, "review-mode")
	script := `#!/bin/sh
case "$*" in
*"Phase objective: review"*)
  mkdir -p .sgt
  if [ "$(cat "$REVIEW_MODE_FILE")" = blocking ]; then
    findings='[{"axis":"spec","severity":"error","summary":"retry path untested"}]'
  else
    findings='[]'
  fi
  printf '{"task_id":"t","repo":"svc","stage":"review","summary":"reviewed","payload":{"findings":%s}}\n' "$findings" > .sgt/envelope.json
  ;;
*) echo built ;;
esac
exit 0
`
	agentPath := filepath.Join(binDir, "goose")
	if err := os.WriteFile(agentPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("REVIEW_MODE_FILE", modeFile)
	if err := os.WriteFile(modeFile, []byte("blocking"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := &config.Project{
		Name:     "review-proj",
		Defaults: config.ProjectDefaults{Agent: agentPath},
		Repos: map[string]config.Repo{
			"svc": {Path: repoDir, Factory: &config.FactoryConfig{
				Pipeline: []string{"build", "review"},
			}},
		},
	}
	eng := newEngine(t, proj)
	runID := "run-review-resume-1"
	createTestRun(t, eng, proj.Name, runID, "running")
	stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}}

	if err := eng.RunStage(context.Background(), runID, stage); err == nil {
		t.Fatal("first run: RunStage passed with a blocking review finding")
	}
	reviews := agentRecords(t, eng.Store, runID, "review")
	if len(reviews) != 1 || reviews[0].Status != "failed" {
		t.Fatalf("first run review records = %+v, want one failed record", reviews)
	}

	// Resume with the reviewer now clean: the review re-runs and passes, and
	// the passed build is not re-run.
	if err := os.WriteFile(modeFile, []byte("clean"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng.Resume = true
	if err := eng.RunStage(context.Background(), runID, stage); err != nil {
		t.Fatalf("resume: RunStage failed after a clean review: %v", err)
	}
	reviews = agentRecords(t, eng.Store, runID, "review")
	if len(reviews) != 2 {
		t.Fatalf("review records after resume = %+v, want a fresh second record", reviews)
	}
	if reviews[1].Status != "passed" {
		t.Errorf("resumed review status = %q, want passed", reviews[1].Status)
	}
	if builds := agentRecords(t, eng.Store, runID, "build"); len(builds) != 1 {
		t.Errorf("build records = %d, want 1 (a passed build is not re-run on resume)", len(builds))
	}
}

// Resume never turns a still-blocking review into a pass.
func TestResumedReviewThatStillBlocksFailsTheStage(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(tempDir, "fleet"))

	repoDir := filepath.Join(tempDir, "svc")
	newGitRepo(t, repoDir)
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(binDir, "goose")
	findings := `[{"axis":"spec","severity":"error","summary":"retry path untested"}]`
	if err := os.WriteFile(agentPath, []byte(reviewAgentScript(findings)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	proj := &config.Project{
		Name:     "review-proj",
		Defaults: config.ProjectDefaults{Agent: agentPath},
		Repos: map[string]config.Repo{
			"svc": {Path: repoDir, Factory: &config.FactoryConfig{Pipeline: []string{"review"}}},
		},
	}
	eng := newEngine(t, proj)
	runID := "run-review-resume-2"
	createTestRun(t, eng, proj.Name, runID, "running")
	stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}}

	_ = eng.RunStage(context.Background(), runID, stage)
	eng.Resume = true
	if err := eng.RunStage(context.Background(), runID, stage); err == nil {
		t.Fatal("resume: RunStage passed while the review still reports a blocking finding")
	}
	for _, r := range agentRecords(t, eng.Store, runID, "review") {
		if r.Status == "passed" {
			t.Errorf("review record %s is passed while every review blocked", r.ID)
		}
	}
}

func agentRecords(t *testing.T, st *store.Store, runID, name string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == "agent" && p.Status != "running" {
			out = append(out, p)
		}
	}
	return out
}
