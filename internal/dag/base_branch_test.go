package dag

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
)

// Scenario: "A run's first worktree creation records its real base branch"
// (specs/change-request-merge/spec.md). newGitRepo checks the source repo
// out on "main" — prepareWorktree must durably record that as the run's
// BaseBranch, not guess at it later.
func TestPrepareWorktreeRecordsTheSourceRepoRealBaseBranch(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "svc")
	newGitRepo(t, src)
	t.Setenv("SGT_FLEET_DIR", t.TempDir())

	proj := &config.Project{Name: "p", Repos: map[string]config.Repo{"svc": {Path: src}}}
	eng := newEngine(t, proj)
	createTestRun(t, eng, proj.Name, "run-basebranch-1", "running")

	if _, _, err := eng.prepareWorktree(ctx, src, "run-basebranch-1", "svc"); err != nil {
		t.Fatalf("prepareWorktree: %v", err)
	}

	run, err := eng.Store.GetRun("run-basebranch-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseBranch != "main" {
		t.Errorf("run.BaseBranch = %q, want %q (the source repo's actual checked-out branch)", run.BaseBranch, "main")
	}
}

// Scenario: "Resuming a run does not overwrite its recorded base branch"
// (specs/change-request-merge/spec.md). A run whose worktree was removed
// but whose branch survived is resumed after the source repo's checked-out
// branch has since changed — the recorded BaseBranch must stay what the
// first attempt captured, not silently follow the operator's checkout.
func TestResumingARunDoesNotOverwriteItsRecordedBaseBranch(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "svc")
	newGitRepo(t, src)
	t.Setenv("SGT_FLEET_DIR", t.TempDir())

	proj := &config.Project{Name: "p", Repos: map[string]config.Repo{"svc": {Path: src}}}
	eng := newEngine(t, proj)
	createTestRun(t, eng, proj.Name, "run-basebranch-resume-1", "running")

	wt, _, err := eng.prepareWorktree(ctx, src, "run-basebranch-resume-1", "svc")
	if err != nil {
		t.Fatalf("first prepareWorktree: %v", err)
	}

	run, err := eng.Store.GetRun("run-basebranch-resume-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseBranch != "main" {
		t.Fatalf("run.BaseBranch after first attempt = %q, want %q", run.BaseBranch, "main")
	}

	// The worktree goes away, same as TestPrepareWorktreeDoesNotDiscardCommitsOnAnExistingBranch.
	git(t, src, "worktree", "remove", "--force", wt)

	// The operator's own checkout has since moved on to a different branch.
	git(t, src, "checkout", "-q", "-b", "some-other-branch")

	if _, _, err := eng.prepareWorktree(ctx, src, "run-basebranch-resume-1", "svc"); err != nil {
		t.Fatalf("resumed prepareWorktree: %v", err)
	}

	run, err = eng.Store.GetRun("run-basebranch-resume-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseBranch != "main" {
		t.Errorf("run.BaseBranch after resume = %q, want unchanged %q even though the source checkout moved to %q", run.BaseBranch, "main", "some-other-branch")
	}
}

// Scenario: "Dispatch bases new work on the repository's default branch, not
// the operator's own checkout" (specs/change-request-merge/spec.md). The
// source repo's default branch is "main", but the operator has since checked
// out a feature branch with its own unrelated commit. A dispatch with no
// explicit base must never read that checkout as the base: the new run
// branch must start from "main", and the feature branch's commit must not
// leak into it. The operator's checkout must also be left exactly where it
// was.
func TestPrepareWorktreeUsesRepositoryDefaultBranchNotOperatorsCheckout(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "svc")
	newGitRepo(t, src)

	// The operator's own working copy moves on to a feature branch carrying a
	// commit that must never appear in newly dispatched work.
	git(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feature-only.txt"), []byte("wip\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "feature-only work")

	t.Setenv("SGT_FLEET_DIR", t.TempDir())

	proj := &config.Project{Name: "p", Repos: map[string]config.Repo{"svc": {Path: src}}}
	eng := newEngine(t, proj)
	createTestRun(t, eng, proj.Name, "run-basebranch-default-1", "running")

	wt, _, err := eng.prepareWorktree(ctx, src, "run-basebranch-default-1", "svc")
	if err != nil {
		t.Fatalf("prepareWorktree: %v", err)
	}

	run, err := eng.Store.GetRun("run-basebranch-default-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseBranch != "main" {
		t.Errorf("run.BaseBranch = %q, want %q (the repository's default branch, not the operator's checkout)", run.BaseBranch, "main")
	}

	if _, err := os.Stat(filepath.Join(wt, "feature-only.txt")); err == nil {
		t.Error("worktree contains feature-only.txt: the operator's feature-branch commit leaked into newly dispatched work")
	}

	// The operator's checkout must be untouched: still on "feature", not
	// reset or switched by the dispatch.
	if got := gitOutput(ctx, src, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Errorf("source checkout is on %q; dispatch must not read or mutate the operator's checkout", got)
	}
}
