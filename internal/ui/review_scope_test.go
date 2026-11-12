package ui

// Tests for the review-scope slice of claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 6), scenario "The prompts directory
// is not treated as a repo": role-phase prompts live in <run>/.prompts/
// beside the run's worktrees, and neither progress sampling nor the fleet
// view may take that directory, or any other dot-directory, for a repo.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/plan"
	"github.com/callmeradical/sgt/internal/store"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

func writePlan(t *testing.T, worktree string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(worktree, ".sgt"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(plan.Plan{Items: []plan.PlanItem{{ID: "s-1", Scenario: "One", Status: plan.StatusPending}}})
	if err := os.WriteFile(filepath.Join(worktree, ".sgt", "plan.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePromptsDir(t *testing.T, runDir string) string {
	t.Helper()
	prompts := filepath.Join(runDir, ".prompts", "svc")
	if err := os.MkdirAll(prompts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prompts, "prompt_review_1.txt"), []byte("VERDICT: contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(runDir, ".prompts")
}

// Progress sampling records the run's real worktree and nothing for
// .prompts. The dot-directory is skipped by name, so even one that holds a
// plan file is never sampled as a repo.
func TestRunProgressSamplingSkipsThePromptsDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))
	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateRun(&store.RunRecord{ID: "run-dot", Project: "p", TaskID: "run-dot", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(base, "fleet", "run-dot")
	writePlan(t, filepath.Join(runDir, "svc"))
	writePlan(t, writePromptsDir(t, runDir))

	srv := NewServer(st, 0)
	seqBefore, _ := st.CurrentSequence()
	srv.appendRunProgress("run-dot")

	changes, err := st.ListChangesSince(seqBefore, 10)
	if err != nil {
		t.Fatal(err)
	}
	var repos []string
	for _, c := range changes {
		if c.Channel != store.ChannelProgress {
			continue
		}
		var p struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal(c.Payload, &p); err != nil {
			t.Fatalf("progress payload: %v", err)
		}
		repos = append(repos, p.Repo)
	}
	if len(repos) != 1 || repos[0] != "svc" {
		t.Errorf("progress recorded for repos %v, want only [svc]", repos)
	}
}

// GET /api/fleet lists the run's worktree and no lease for .prompts, even
// where the fleet root sits inside a git checkout, so git would answer for
// the .prompts directory too.
func TestFleetViewSkipsThePromptsDirectory(t *testing.T) {
	mux, _, _ := dispatchFixture(t)
	root := configuredFleetRoot(t)
	gitInit(t, root)

	runDir := filepath.Join(root, "run-dot")
	gitInit(t, filepath.Join(runDir, "svc"))
	writePromptsDir(t, runDir)

	var body struct {
		Leases []struct {
			TaskID string `json:"task_id"`
			Repo   string `json:"repo"`
		} `json:"leases"`
	}
	getJSON(t, mux, "GET", "/api/fleet", nil, &body)

	var repos []string
	for _, l := range body.Leases {
		if l.TaskID == "run-dot" {
			repos = append(repos, l.Repo)
		}
		if l.Repo == ".prompts" || l.TaskID == ".prompts" {
			t.Errorf("fleet view lists %+v", l)
		}
	}
	if len(repos) != 1 || repos[0] != "svc" {
		t.Errorf("fleet view lists repos %v for run-dot, want only [svc]", repos)
	}
}
