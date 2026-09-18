package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/changerequest"
	"github.com/callmeradical/sgt/internal/sgtclient"
	"github.com/callmeradical/sgt/internal/store"
	"github.com/callmeradical/sgt/internal/ui"
)

// sgt_dispatch and sgt_create_pr must call the exact same code
// handleDispatch/handleCreatePR do — not a reimplementation — so every test
// in this file drives them against a REAL ui.NewServer(...).Handler()
// wrapped in an httptest.Server, never a fake, and asserts on the resulting
// store rows directly. SGT_UI_ADDR points sgtclient at that server, exactly
// as it would point at an operator's real `sgt ui`.

// fakeChangeRequestProvider is a swappable stand-in for changerequest.Provider,
// replicated here (rather than imported from internal/ui, which does not
// export it) per design.md's "or replicate the minimal fake provider install
// there."
type fakeChangeRequestProvider struct {
	createFn    func(ctx context.Context, repoPath, base, head, title, body string) (string, error)
	createCalls int
}

func (f *fakeChangeRequestProvider) Create(ctx context.Context, repoPath, base, head, title, body string) (string, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, repoPath, base, head, title, body)
	}
	return "https://github.com/example/repo/pull/1", nil
}

func (f *fakeChangeRequestProvider) Status(ctx context.Context, repoPath, url string) (*changerequest.StatusResult, error) {
	return &changerequest.StatusResult{}, nil
}

// installFakeGitHubProvider swaps changerequest.Providers["github"] for fake
// and restores the real one when the test ends.
func installFakeGitHubProvider(t *testing.T, fake *fakeChangeRequestProvider) {
	t.Helper()
	orig := changerequest.Providers["github"]
	changerequest.Providers["github"] = fake
	t.Cleanup(func() { changerequest.Providers["github"] = orig })
}

// mcpDispatchFixture stands up a real ui.NewServer(...).Handler() behind an
// httptest.Server, points SGT_UI_ADDR at it so sgt_dispatch reaches it
// exactly as an operator's `sgt ui` would, and returns the MCP server
// (backed by the SAME store the HTTP handler writes to) for direct row
// assertions.
//
// Repos are plain (non-git) directories — the same choice internal/ui's own
// dispatchFixtureRepos makes: a dispatch's response and its run/intent/bullet
// rows are written before the async goroutine ever reaches prepareWorktree's
// git check, so a plain directory is enough for what this file asserts on.
func mcpDispatchFixture(t *testing.T, repos ...string) (s *MCPServer, st *store.Store, repoPaths map[string]string, uiAddr string) {
	t.Helper()

	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))

	repoPaths = map[string]string{}
	projYAML := "name: mcpo\nrepos:\n"
	for _, name := range repos {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		repoPaths[name] = p
		projYAML += "  - name: " + name + "\n    path: " + p + "\n"
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "mcpo.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(base, "t.db")
	var err error
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	httpSrv := httptest.NewServer(ui.NewServer(st, 0).Handler())
	t.Cleanup(httpSrv.Close)
	t.Setenv("SGT_UI_ADDR", httpSrv.URL)

	return NewMCPServer(st), st, repoPaths, httpSrv.URL
}

// mcpCreatePRFixture builds a server backed by a fresh store holding one
// intent with one bullet at the given status, and a run naming that intent —
// the minimal setup handleCreatePR's seal guard needs — wired the same way
// mcpDispatchFixture is (real ui.Handler behind httptest.Server, SGT_UI_ADDR
// pointed at it). The repo is a real git repository with a GitHub-shaped
// origin remote, so a green bullet's request reaches changerequest.Providers.
func mcpCreatePRFixture(t *testing.T, bulletStatus string) (s *MCPServer, st *store.Store, runID, repoPath, uiAddr string) {
	t.Helper()

	base := t.TempDir()
	repoPath = filepath.Join(base, "svc")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@example.com")
	runGit("config", "user.name", "t")
	runGit("remote", "add", "origin", "https://github.com/example/repo.git")

	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	projYAML := fmt.Sprintf("name: mcpcp\nrepos:\n  - name: svc\n    path: %s\n", repoPath)
	if err := os.WriteFile(filepath.Join(cfgDir, "mcpcp.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(base, "t.db")
	var err error
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const intentID = "intent-mcpcp-1"
	if err := st.CreateIntent(&store.IntentRecord{ID: intentID, Project: "mcpcp", Statement: "s", Status: "approved"}); err != nil {
		t.Fatalf("creating intent: %v", err)
	}
	if err := st.CreateBullet(&store.BulletRecord{ID: "bullet-mcpcp-1", IntentID: intentID, Repo: "svc", Position: 1, Status: bulletStatus}); err != nil {
		t.Fatalf("creating bullet: %v", err)
	}
	runID = "run-mcpcp-1"
	if err := st.CreateRun(&store.RunRecord{ID: runID, Project: "mcpcp", TaskID: runID, Status: "passed", IntentID: intentID}); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	httpSrv := httptest.NewServer(ui.NewServer(st, 0).Handler())
	t.Cleanup(httpSrv.Close)
	t.Setenv("SGT_UI_ADDR", httpSrv.URL)

	return NewMCPServer(st), st, runID, repoPath, httpSrv.URL
}

// waitForTerminalRunMCP blocks until a run leaves the running state, so a
// test's assertions are not racing the dispatch goroutine, and so that
// goroutine cannot still be writing to the store after the test's t.Cleanup
// closes it.
func waitForTerminalRunMCP(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err != nil {
			t.Fatalf("reading run %s: %v", runID, err)
		}
		switch run.Status {
		case "passed", "failed", "cancelled":
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal status within the deadline", runID)
}

// The tool list must advertise both new tools — a client cannot call what
// tools/list never told it exists.
func TestToolListIncludesSgtDispatchAndSgtCreatePR(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"sgt_dispatch", "sgt_create_pr"} {
		if !names[want] {
			t.Errorf("Tools() does not advertise %q", want)
		}
	}
}

// sgt_dispatch with explicit repos must create the same run/intent/bullet
// rows a POST /api/dispatch call with identical fields would.
func TestSgtDispatchWithExplicitReposCreatesTheSameRowsAsHTTPWould(t *testing.T) {
	s, st, repoPaths, _ := mcpDispatchFixture(t, "svc")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["svc"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	text, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks",
		"repos": []interface{}{"svc"}, "type": "feat", "change_id": changeID,
	})
	if err != nil {
		t.Fatalf("sgt_dispatch returned an error: %v", err)
	}

	var resp sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(text), &resp); uerr != nil {
		t.Fatalf("decoding sgt_dispatch result: %v; text=%s", uerr, text)
	}
	if resp.Status != "dispatched" {
		t.Errorf("Status = %q, want dispatched", resp.Status)
	}
	if resp.TaskID == "" {
		t.Fatal("TaskID is empty")
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("store holds %d runs, want 1: %+v", len(runs), runs)
	}
	if runs[0].ID != resp.TaskID {
		t.Errorf("stored run id = %q, want %q", runs[0].ID, resp.TaskID)
	}

	intents, err := st.ListIntentsForProject("mcpo")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 {
		t.Fatalf("store holds %d intents, want 1", len(intents))
	}

	bullets, err := st.ListBulletsForIntent(intents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bullets) != 1 || bullets[0].Repo != "svc" {
		t.Errorf("bullets = %+v, want one bullet for repo svc", bullets)
	}

	waitForTerminalRunMCP(t, st, resp.TaskID)
}

// sgt_dispatch with no repos must record a proposed plan and start no run —
// the same store state POST /api/dispatch's no-repos path leaves.
func TestSgtDispatchWithNoReposRecordsAProposedPlanAndStartsNoRun(t *testing.T) {
	s, st, repoPaths, _ := mcpDispatchFixture(t, "web", "api")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["api"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	text, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks", "type": "feat", "change_id": changeID,
	})
	if err != nil {
		t.Fatalf("sgt_dispatch returned an error: %v", err)
	}

	var resp sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(text), &resp); uerr != nil {
		t.Fatalf("decoding sgt_dispatch result: %v; text=%s", uerr, text)
	}
	if resp.Status != "proposed" {
		t.Errorf("Status = %q, want proposed", resp.Status)
	}
	if resp.IntentID == "" {
		t.Error("IntentID is empty")
	}
	if resp.TaskID != "" {
		t.Errorf("TaskID = %q, want empty for the proposed shape", resp.TaskID)
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("store holds %d runs, want 0 for a no-repos dispatch", len(runs))
	}

	intents, err := st.ListIntentsForProject("mcpo")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].Status != "proposed" {
		t.Fatalf("intents = %+v, want exactly one proposed intent", intents)
	}
}

// Two sgt_dispatch calls with the same request_id must produce exactly one
// run row in the actual database — checked directly, not inferred from both
// calls returning success.
func TestSgtDispatchCalledTwiceWithSameRequestIDProducesExactlyOneRunRow(t *testing.T) {
	s, st, repoPaths, _ := mcpDispatchFixture(t, "svc")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["svc"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	args := map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks",
		"repos": []interface{}{"svc"}, "type": "feat", "change_id": changeID, "request_id": "retry-me",
	}

	first, err := s.executeTool("sgt_dispatch", args)
	if err != nil {
		t.Fatalf("first sgt_dispatch returned an error: %v", err)
	}
	second, err := s.executeTool("sgt_dispatch", args)
	if err != nil {
		t.Fatalf("repeat sgt_dispatch returned an error: %v", err)
	}

	var a, b sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(first), &a); uerr != nil {
		t.Fatal(uerr)
	}
	if uerr := json.Unmarshal([]byte(second), &b); uerr != nil {
		t.Fatal(uerr)
	}
	if a.TaskID != b.TaskID {
		t.Errorf("repeat task id = %q, want the original %q", b.TaskID, a.TaskID)
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("store holds %d runs for one request id, want 1: %+v", len(runs), runs)
	}

	waitForTerminalRunMCP(t, st, a.TaskID)
}

// sgt_dispatch with an unrecognized type must be refused with the exact
// message validateWorkType produces over HTTP (internal/ui/bulletstate.go).
func TestSgtDispatchWithAnUnrecognizedTypeIsRefusedWithValidateWorkTypesExactMessage(t *testing.T) {
	s, _, _, _ := mcpDispatchFixture(t, "svc")

	_, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks", "type": "bogus",
	})
	if err == nil {
		t.Fatal("expected an error for an unrecognized type, got nil")
	}
	want := `invalid or missing type "bogus": must be one of chore, docs, feat, fix, refactor, test`
	if err.Error() != want {
		t.Errorf("error = %q, want exactly %q", err.Error(), want)
	}
}

// sgt_dispatch naming a change_id that does not exist on disk must be
// refused with resolveChange's exact message (O3, internal/ui/openspec.go)
// — the same refusal POST /api/dispatch produces for identical input — and
// must create no run. Mirrors internal/ui's own
// TestDispatchWithUnknownChangeIDIsRejectedAndCreatesNoRun.
func TestSgtDispatchWithUnknownChangeIDIsRefusedAndCreatesNoRun(t *testing.T) {
	s, st, repoPaths, _ := mcpDispatchFixture(t, "svc")

	_, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks",
		"repos": []interface{}{"svc"}, "type": "feat", "change_id": "no-such-change",
	})
	if err == nil {
		t.Fatal("expected an error for an unknown change_id, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-change") {
		t.Errorf("error does not name the change: %v", err)
	}
	wantPath := filepath.Join(repoPaths["svc"], "openspec", "changes", "no-such-change")
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error does not name the missing path %s: %v", wantPath, err)
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("store holds %d runs after a rejected dispatch, want 0", len(runs))
	}
}

// sgt_create_pr against a green bullet must seal it and call the provider
// seam — the same seal-then-provider-call sequence handleCreatePR runs.
func TestSgtCreatePRAgainstGreenBulletSealsItAndCallsProvider(t *testing.T) {
	s, st, runID, _, _ := mcpCreatePRFixture(t, "green")
	fake := &fakeChangeRequestProvider{}
	installFakeGitHubProvider(t, fake)

	text, err := s.executeTool("sgt_create_pr", map[string]interface{}{
		"run_id": runID, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
	})
	if err != nil {
		t.Fatalf("sgt_create_pr returned an error: %v", err)
	}
	if fake.createCalls != 1 {
		t.Errorf("provider Create called %d time(s), want 1", fake.createCalls)
	}

	var resp sgtclient.CreatePRResponse
	if uerr := json.Unmarshal([]byte(text), &resp); uerr != nil {
		t.Fatalf("decoding sgt_create_pr result: %v; text=%s", uerr, text)
	}
	if resp.Status != "created" {
		t.Errorf("Status = %q, want created", resp.Status)
	}
	if resp.PRURL == "" {
		t.Error("PRURL is empty")
	}

	bullets, err := st.ListBulletsForIntent("intent-mcpcp-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bullets) != 1 || bullets[0].Status != "sealed" {
		t.Errorf("expected the bullet to be sealed, got %+v", bullets)
	}
}

// sgt_create_pr against a non-green bullet must be refused with
// handleCreatePR's exact SealBulletForRun refusal text, and must not invoke
// the provider — mirrors internal/ui's
// TestCreatePRForNonGreenBulletIsRefusedAndNeverInvokesGH.
func TestSgtCreatePRAgainstNonGreenBulletIsRefusedAndNeverInvokesGH(t *testing.T) {
	for _, status := range []string{"pending", "red", "sealed", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, _, runID, _, _ := mcpCreatePRFixture(t, status)
			fake := &fakeChangeRequestProvider{}
			installFakeGitHubProvider(t, fake)

			_, err := s.executeTool("sgt_create_pr", map[string]interface{}{
				"run_id": runID, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
			})
			if err == nil {
				t.Fatal("expected an error for a non-green bullet, got nil")
			}
			if !strings.Contains(err.Error(), status) {
				t.Errorf("error does not name the bullet's actual status %q: %v", status, err)
			}
			if fake.createCalls != 0 {
				t.Errorf("gh pr create was invoked %d time(s) for a refused request, want 0", fake.createCalls)
			}
		})
	}
}
