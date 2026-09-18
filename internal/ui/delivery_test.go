package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/store"
)

// fakeRunGetter is a swappable stand-in for runGetter so describeDelivery
// can be exercised against a run record with no store behind it.
type fakeRunGetter struct{ run *store.RunRecord }

func (f *fakeRunGetter) GetRun(id string) (*store.RunRecord, error) { return f.run, nil }

func runGitDelivery(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// Regression coverage for issue #21 ("Dispatch dirties source checkout and
// reports an unpushed commit as pushed"): Pushed must reflect whether HEAD
// is actually reachable from origin/<branch>, not merely whether that
// remote-tracking ref exists at all. The old check verified only the
// latter, so a commit made after an earlier push (e.g. a second
// CommitRunOutput pass) was reported as pushed when it never left the
// worktree.
func TestDescribeDeliveryReportsPushedOnlyWhenHEADIsOnTheRemote(t *testing.T) {
	fleetRoot := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", fleetRoot)

	const runID = "run-push-1"
	const repoName = "svc"
	const branch = "sgt/" + runID

	remote := t.TempDir()
	runGitDelivery(t, remote, "init", "--bare")

	wt := dag.FleetDir(runID, repoName)
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	runGitDelivery(t, wt, "init", "-b", branch)
	runGitDelivery(t, wt, "remote", "add", "origin", remote)
	commit := func(msg string) {
		cmd := exec.Command("git", "-C", wt, "commit", "--allow-empty", "-m", msg)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v: %s", err, out)
		}
	}
	commit("first, pushed")
	runGitDelivery(t, wt, "push", "origin", branch)

	dr := newDeliveryReporter(&fakeRunGetter{run: &store.RunRecord{
		ID: runID, Type: "", ChangeID: "", BaseBranch: branch,
	}})
	proj := &config.Project{Name: "cr", Repos: map[string]config.Repo{
		repoName: {Name: repoName, Path: filepath.Dir(filepath.Dir(wt))}, // any existing path; only used for CompareURL
	}}

	rep := dr.describeDelivery(proj, runID)
	if !rep.Pushed {
		t.Errorf("Pushed = false after HEAD was actually pushed to origin/%s, want true", branch)
	}

	// A second, local-only commit moves HEAD ahead of origin/<branch> again.
	commit("second, never pushed")
	rep = dr.describeDelivery(proj, runID)
	if rep.Pushed {
		t.Errorf("Pushed = true for a commit that was never pushed to origin/%s, want false", branch)
	}
}
