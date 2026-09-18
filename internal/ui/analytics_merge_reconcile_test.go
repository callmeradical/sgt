package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/changerequest"
	"github.com/callmeradical/sgt/internal/store"
)

// analyticsReconcileFixture mirrors mergeStatusFixture (merge_status_test.go):
// a server backed by a fresh store, one intent, and a real git repo with a
// GitHub remote wired into the run's project config, but returns projPath so
// a test can query /api/analytics?project=<projPath> directly.
func analyticsReconcileFixture(t *testing.T, baseBranch string) (srv *Server, mux http.Handler, runID, intentID, projPath string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics-merge.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init")
	runGit("remote", "add", "origin", "https://github.com/example/repo.git")

	projPath = filepath.Join(t.TempDir(), "proj.yaml")
	projYAML := fmt.Sprintf("name: analytics-mrg\nrepos:\n  svc:\n    path: %q\n", repoDir)
	if err := os.WriteFile(projPath, []byte(projYAML), 0644); err != nil {
		t.Fatal(err)
	}

	intentID = "intent-analytics-mrg-1"
	if err := st.CreateIntent(&store.IntentRecord{ID: intentID, Project: projPath, Statement: "s", Status: "in_progress"}); err != nil {
		t.Fatal(err)
	}
	runID = "run-analytics-mrg-1"
	if err := st.CreateRun(&store.RunRecord{
		ID: runID, Project: projPath, TaskID: runID, Status: "passed", IntentID: intentID, BaseBranch: baseBranch,
	}); err != nil {
		t.Fatal(err)
	}

	srv = NewServer(st, 0)
	return srv, srv.Handler(), runID, intentID, projPath
}

func getAnalytics(t *testing.T, mux http.Handler, project string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/analytics?project="+project, nil))
	return w
}

// Regression coverage for issue #11 ("Bullet merge status only refreshes
// when a run's pipeline view is opened, never automatically"): a sealed
// bullet's merge status must be reconciled just from loading a project's
// Work Analytics — no run's pipeline view, and no explicit
// /api/check-merge-status call naming that run, ever involved.
func TestGETAnalyticsReconcilesASealedBulletsMergeStatus(t *testing.T) {
	srv, mux, _, intentID, projPath := analyticsReconcileFixture(t, "main")

	const prURL = "https://github.com/example/repo/pull/55"
	if err := srv.Store.CreateBullet(&store.BulletRecord{ID: "b-analytics-1", IntentID: intentID, Repo: "svc", Position: 1, Status: "sealed", PRURL: prURL}); err != nil {
		t.Fatal(err)
	}

	installFakeGitHubProvider(t, &fakeChangeRequestProvider{
		statusFn: func(ctx context.Context, repoPath, url string) (*changerequest.StatusResult, error) {
			return &changerequest.StatusResult{Merged: true, MergedIntoBranch: "main"}, nil
		},
	})

	w := getAnalytics(t, mux, projPath)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	got, err := srv.Store.GetBullet("b-analytics-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "merged" {
		t.Errorf("bullet.Status = %q after loading analytics, want merged — nobody ever opened this run's pipeline view or called check-merge-status directly", got.Status)
	}
}

// Reconciling every project's bullets on one shared, unscoped ("" or "all")
// analytics request would burst a provider call per eligible bullet across
// the whole installation on a single page load — the same unconditional
// cost R7.5/observed-change-request-merge-state already rejected for a
// background timer. Only a request naming one specific project reconciles.
func TestGETAnalyticsForAllProjectsNeverCallsTheProvider(t *testing.T) {
	srv, mux, _, intentID, _ := analyticsReconcileFixture(t, "main")

	const prURL = "https://github.com/example/repo/pull/56"
	if err := srv.Store.CreateBullet(&store.BulletRecord{ID: "b-analytics-2", IntentID: intentID, Repo: "svc", Position: 1, Status: "sealed", PRURL: prURL}); err != nil {
		t.Fatal(err)
	}

	statusCalls := 0
	installFakeGitHubProvider(t, &fakeChangeRequestProvider{
		statusFn: func(ctx context.Context, repoPath, url string) (*changerequest.StatusResult, error) {
			statusCalls++
			return &changerequest.StatusResult{Merged: true, MergedIntoBranch: "main"}, nil
		},
	})

	for _, project := range []string{"", "all"} {
		w := getAnalytics(t, mux, project)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d for project=%q, want 200; body=%s", w.Code, project, w.Body.String())
		}
	}
	if statusCalls != 0 {
		t.Errorf("Status invoked %d time(s) for an unscoped analytics request, want 0", statusCalls)
	}

	got, err := srv.Store.GetBullet("b-analytics-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "sealed" {
		t.Errorf("bullet.Status = %q after an unscoped analytics request, want unchanged sealed", got.Status)
	}
}
