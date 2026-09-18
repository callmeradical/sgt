package main

// Tests for the four new cmd/sgt subcommands (dispatch, runs, run-details,
// create-pr), following internal/repopolicy's own precedent
// (TestMiseInstallLinksWikiDigestAndBuildsSgt) for "run the real artifact as
// a subprocess, don't reimplement it": every test here builds the real sgt
// binary via sgtBinary(t) (help_exit_test.go, this package) and runs it as a
// real subprocess against a real httptest.Server wrapping the real
// ui.NewServer(...).Handler() — never a fake server, never a stand-in for
// the binary.
import (
	"bytes"
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

// cliDispatchFixture stands up a real ui.NewServer(...).Handler() behind an
// httptest.Server and a project config naming the given repos as plain
// (non-git) directories — the same minimal shape internal/mcp's
// mcpDispatchFixture uses, since a dispatch's response and its
// run/intent/bullet rows are written before the async goroutine ever
// reaches a git check.
func cliDispatchFixture(t *testing.T, repos ...string) (st *store.Store, repoPaths map[string]string, addr string) {
	t.Helper()

	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))

	repoPaths = map[string]string{}
	projYAML := "name: clio\nrepos:\n"
	for _, name := range repos {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		repoPaths[name] = p
		projYAML += "  - name: " + name + "\n    path: " + p + "\n"
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "clio.yaml"), []byte(projYAML), 0o644); err != nil {
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

	return st, repoPaths, httpSrv.URL
}

// cliUIFixture is the minimal fixture for endpoints that need no project
// config at all (runs, run-details): a real store behind a real
// ui.NewServer(...).Handler().
func cliUIFixture(t *testing.T) (st *store.Store, addr string) {
	t.Helper()

	base := t.TempDir()
	var err error
	st, err = store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	httpSrv := httptest.NewServer(ui.NewServer(st, 0).Handler())
	t.Cleanup(httpSrv.Close)

	return st, httpSrv.URL
}

// cliCreatePRFixture builds a real git repo with a GitHub-shaped origin
// remote, a project naming it, and a store holding one intent with one
// bullet at the given status plus a run naming that intent — the minimal
// state handleCreatePR's seal guard needs — behind a real
// ui.NewServer(...).Handler(), mirroring internal/mcp's mcpCreatePRFixture.
func cliCreatePRFixture(t *testing.T, bulletStatus string) (st *store.Store, runID, addr string) {
	t.Helper()

	base := t.TempDir()
	repoPath := filepath.Join(base, "svc")
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
	projYAML := fmt.Sprintf("name: clicp\nrepos:\n  - name: svc\n    path: %s\n", repoPath)
	if err := os.WriteFile(filepath.Join(cfgDir, "clicp.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	var err error
	st, err = store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const intentID = "intent-clicp-1"
	if err := st.CreateIntent(&store.IntentRecord{ID: intentID, Project: "clicp", Statement: "s", Status: "approved"}); err != nil {
		t.Fatalf("creating intent: %v", err)
	}
	if err := st.CreateBullet(&store.BulletRecord{ID: "bullet-clicp-1", IntentID: intentID, Repo: "svc", Position: 1, Status: bulletStatus}); err != nil {
		t.Fatalf("creating bullet: %v", err)
	}
	runID = "run-clicp-1"
	if err := st.CreateRun(&store.RunRecord{ID: runID, Project: "clicp", TaskID: runID, Status: "passed", IntentID: intentID}); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	httpSrv := httptest.NewServer(ui.NewServer(st, 0).Handler())
	t.Cleanup(httpSrv.Close)

	return st, runID, httpSrv.URL
}

// waitForTerminalRunCLI blocks until a run leaves the running state.
func waitForTerminalRunCLI(t *testing.T, st *store.Store, runID string) {
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

// runCLI runs the compiled sgt binary as a real subprocess with SGT_UI_ADDR
// pointed at addr, and returns its stdout, stderr, and exit error (nil on
// success).
func runCLI(t *testing.T, bin, addr string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "SGT_UI_ADDR="+addr)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// sgt dispatch against a real fixture server must create the same
// run/intent/bullet rows an equivalent raw HTTP POST would.
func TestDispatchSubcommandCreatesSameStoreStateAsHTTPPostWould(t *testing.T) {
	bin := sgtBinary(t)
	st, repoPaths, addr := cliDispatchFixture(t, "svc")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["svc"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, bin, addr,
		"dispatch",
		"--project", "clio",
		"--brief", "add stripe webhooks",
		"--type", "feat",
		"--repo", "svc",
		"--change-id", changeID,
	)
	if err != nil {
		t.Fatalf("sgt dispatch failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	var resp sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(stdout), &resp); uerr != nil {
		t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
	}
	if resp.Status != "dispatched" {
		t.Fatalf("Status = %q, want dispatched", resp.Status)
	}
	if resp.TaskID == "" {
		t.Fatal("TaskID is empty")
	}

	run, err := st.GetRun(resp.TaskID)
	if err != nil {
		t.Fatalf("reading run %s: %v", resp.TaskID, err)
	}
	bullets, err := st.ListBulletsForIntent(run.IntentID)
	if err != nil {
		t.Fatalf("listing bullets: %v", err)
	}
	if len(bullets) != 1 || bullets[0].Repo != "svc" || bullets[0].Position != 1 {
		t.Errorf("bullets = %+v, want exactly one bullet for repo svc at position 1", bullets)
	}

	waitForTerminalRunCLI(t, st, resp.TaskID)
}

// sgt dispatch with no --repo at all must record a proposed plan and start
// no run — the same store state the HTTP no-repos path leaves.
func TestDispatchSubcommandWithNoRepoRecordsProposedPlanAndStartsNoRun(t *testing.T) {
	bin := sgtBinary(t)
	st, repoPaths, addr := cliDispatchFixture(t, "web", "api")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["api"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, bin, addr,
		"dispatch",
		"--project", "clio",
		"--brief", "add stripe webhooks",
		"--type", "feat",
		"--change-id", changeID,
	)
	if err != nil {
		t.Fatalf("sgt dispatch failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	var resp sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(stdout), &resp); uerr != nil {
		t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
	}
	if resp.Status != "proposed" {
		t.Errorf("Status = %q, want proposed", resp.Status)
	}
	if resp.TaskID != "" {
		t.Errorf("TaskID = %q, want empty for the proposed shape", resp.TaskID)
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("store holds %d runs after a no-repos dispatch, want 0", len(runs))
	}
	intents, err := st.ListIntentsForProject("clio")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].Status != "proposed" {
		t.Fatalf("intents = %+v, want exactly one proposed intent", intents)
	}
}

// Two `sgt dispatch` invocations with the same --request-id must produce
// exactly one run row in the actual database — checked directly against the
// store, not inferred from both subprocess invocations exiting 0. Mirrors
// internal/mcp's TestSgtDispatchCalledTwiceWithSameRequestIDProducesExactlyOneRunRow
// for the CLI surface (openspec/changes/cli-dispatch-subcommands/tasks.md).
func TestDispatchSubcommandCalledTwiceWithSameRequestIDProducesExactlyOneRunRow(t *testing.T) {
	bin := sgtBinary(t)
	st, repoPaths, addr := cliDispatchFixture(t, "svc")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["svc"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"dispatch",
		"--project", "clio",
		"--brief", "add stripe webhooks",
		"--type", "feat",
		"--repo", "svc",
		"--change-id", changeID,
		"--request-id", "retry-me",
	}

	firstOut, firstErr, err := runCLI(t, bin, addr, args...)
	if err != nil {
		t.Fatalf("first sgt dispatch failed: %v\nstdout=%s\nstderr=%s", err, firstOut, firstErr)
	}
	secondOut, secondErr, err := runCLI(t, bin, addr, args...)
	if err != nil {
		t.Fatalf("repeat sgt dispatch failed: %v\nstdout=%s\nstderr=%s", err, secondOut, secondErr)
	}

	var first, second sgtclient.DispatchResponse
	if uerr := json.Unmarshal([]byte(firstOut), &first); uerr != nil {
		t.Fatalf("decoding first stdout: %v; stdout=%s", uerr, firstOut)
	}
	if uerr := json.Unmarshal([]byte(secondOut), &second); uerr != nil {
		t.Fatalf("decoding repeat stdout: %v; stdout=%s", uerr, secondOut)
	}
	if second.TaskID != first.TaskID {
		t.Errorf("repeat task id = %q, want the original %q", second.TaskID, first.TaskID)
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("store holds %d runs for one request id, want 1: %+v", len(runs), runs)
	}

	waitForTerminalRunCLI(t, st, first.TaskID)
}

// sgt runs --project X and sgt runs (no flag) must return what handleRuns
// would for the same scoping: a named project only, versus every run.
func TestRunsSubcommandMatchesHandleRunsScoping(t *testing.T) {
	bin := sgtBinary(t)
	st, addr := cliUIFixture(t)

	if err := st.CreateRun(&store.RunRecord{ID: "run-p1-a", Project: "p1", TaskID: "run-p1-a", Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(&store.RunRecord{ID: "run-p1-b", Project: "p1", TaskID: "run-p1-b", Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(&store.RunRecord{ID: "run-p2-a", Project: "p2", TaskID: "run-p2-a", Status: "passed"}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, bin, addr, "runs", "--project", "p1")
	if err != nil {
		t.Fatalf("sgt runs --project p1 failed: %v\nstderr=%s", err, stderr)
	}
	var scoped []sgtclient.RunsResponseItem
	if uerr := json.Unmarshal([]byte(stdout), &scoped); uerr != nil {
		t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
	}
	if len(scoped) != 2 {
		t.Errorf("sgt runs --project p1 returned %d runs, want 2: %+v", len(scoped), scoped)
	}
	for _, r := range scoped {
		if r.Project != "p1" {
			t.Errorf("scoped run %q has project %q, want p1", r.ID, r.Project)
		}
	}

	stdout, stderr, err = runCLI(t, bin, addr, "runs")
	if err != nil {
		t.Fatalf("sgt runs failed: %v\nstderr=%s", err, stderr)
	}
	var all []sgtclient.RunsResponseItem
	if uerr := json.Unmarshal([]byte(stdout), &all); uerr != nil {
		t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
	}
	if len(all) != 3 {
		t.Errorf("sgt runs (no flag) returned %d runs, want 3: %+v", len(all), all)
	}
}

// sgt run-details <id> must return phases/envelopes/resume_skips for a run
// seeded directly into the fixture's store.
func TestRunDetailsSubcommandReturnsSeededPhasesAndEnvelopes(t *testing.T) {
	bin := sgtBinary(t)
	st, addr := cliUIFixture(t)

	const runID = "run-details-1"
	if err := st.CreateRun(&store.RunRecord{ID: runID, Project: "p1", TaskID: runID, Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPhase(&store.PhaseRecord{ID: "phase-1", RunID: runID, Repo: "svc", Name: "build", Kind: "agent", Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordEnvelope(&store.EnvelopeRecord{
		ID: "env-1", RunID: runID, Repo: "svc", Stage: "build", Summary: "did the thing",
		Type: "phase.completed", SchemaVersion: "1", Producer: "test", CorrelationID: runID,
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, bin, addr, "run-details", runID)
	if err != nil {
		t.Fatalf("sgt run-details failed: %v\nstderr=%s", err, stderr)
	}

	var resp sgtclient.RunDetailsResponse
	if uerr := json.Unmarshal([]byte(stdout), &resp); uerr != nil {
		t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
	}
	if resp.RunID != runID {
		t.Errorf("RunID = %q, want %q", resp.RunID, runID)
	}
	if len(resp.Phases) != 1 || resp.Phases[0].Name != "build" {
		t.Errorf("Phases = %+v, want one phase named build", resp.Phases)
	}
	if len(resp.Envelopes) != 1 || resp.Envelopes[0].Summary != "did the thing" {
		t.Errorf("Envelopes = %+v, want one envelope with the summary", resp.Envelopes)
	}
}

// sgt create-pr against a green bullet must succeed and seal it; against a
// non-green bullet it must fail with handleCreatePR's exact refusal text.
func TestCreatePRSubcommandGreenSucceedsNonGreenRefusedWithExactText(t *testing.T) {
	bin := sgtBinary(t)

	t.Run("green succeeds", func(t *testing.T) {
		st, runID, addr := cliCreatePRFixture(t, "green")
		orig := changerequest.Providers["github"]
		fake := &fakeChangeRequestProviderCLI{}
		changerequest.Providers["github"] = fake
		t.Cleanup(func() { changerequest.Providers["github"] = orig })

		stdout, stderr, err := runCLI(t, bin, addr,
			"create-pr", "--run-id", runID, "--project", "clicp", "--repo", "svc", "--title", "t", "--body", "b")
		if err != nil {
			t.Fatalf("sgt create-pr failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
		}
		var resp sgtclient.CreatePRResponse
		if uerr := json.Unmarshal([]byte(stdout), &resp); uerr != nil {
			t.Fatalf("decoding stdout: %v; stdout=%s", uerr, stdout)
		}
		if resp.Status != "created" {
			t.Errorf("Status = %q, want created", resp.Status)
		}
		if fake.createCalls != 1 {
			t.Errorf("provider Create called %d time(s), want 1", fake.createCalls)
		}

		bullets, err := st.ListBulletsForIntent("intent-clicp-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(bullets) != 1 || bullets[0].Status != "sealed" {
			t.Errorf("bullets = %+v, want sealed", bullets)
		}
	})

	t.Run("non-green refused", func(t *testing.T) {
		_, runID, addr := cliCreatePRFixture(t, "pending")
		fake := &fakeChangeRequestProviderCLI{}
		orig := changerequest.Providers["github"]
		changerequest.Providers["github"] = fake
		t.Cleanup(func() { changerequest.Providers["github"] = orig })

		stdout, stderr, err := runCLI(t, bin, addr,
			"create-pr", "--run-id", runID, "--project", "clicp", "--repo", "svc", "--title", "t", "--body", "b")
		if err == nil {
			t.Fatalf("expected sgt create-pr to fail for a non-green bullet; stdout=%s", stdout)
		}
		if !strings.Contains(stderr, "pending") || !strings.Contains(stderr, "not green") {
			t.Errorf("stderr = %q, want handleCreatePR's non-green refusal text", stderr)
		}
		if fake.createCalls != 0 {
			t.Errorf("gh pr create was invoked %d time(s) for a refused request, want 0", fake.createCalls)
		}
	})
}

// fakeChangeRequestProviderCLI stands in for changerequest.Provider so
// create-pr's success path never shells out to a real `gh`.
type fakeChangeRequestProviderCLI struct{ createCalls int }

func (f *fakeChangeRequestProviderCLI) Create(ctx context.Context, repoPath, base, head, title, body string) (string, error) {
	f.createCalls++
	return "https://github.com/example/repo/pull/1", nil
}

func (f *fakeChangeRequestProviderCLI) Status(ctx context.Context, repoPath, url string) (*changerequest.StatusResult, error) {
	return &changerequest.StatusResult{}, nil
}

// FindByHead is unused by this file's tests but required by the
// interface (added by fix/merged-dispatched-pull-request-leaves-bullet-green,
// issue #22) — nil, nil matches every other fake's own default.
func (f *fakeChangeRequestProviderCLI) FindByHead(ctx context.Context, repoPath, head string) (*changerequest.FoundRef, error) {
	return nil, nil
}

// Any subcommand run against an address nothing is listening on must exit
// nonzero with the actionable "not reachable... start it with `sgt ui`"
// message on stderr.
func TestSubcommandsAgainstUnreachableAddrExitNonzeroWithActionableMessage(t *testing.T) {
	bin := sgtBinary(t)

	srv := httptest.NewServer(nil)
	addr := srv.URL
	srv.Close() // nothing listens at addr any more

	cases := [][]string{
		{"dispatch", "--project", "p", "--brief", "b", "--type", "feat"},
		{"runs"},
		{"run-details", "some-run-id"},
		{"create-pr", "--run-id", "r", "--project", "p", "--repo", "svc"},
	}
	for _, args := range cases {
		t.Run(args[0], func(t *testing.T) {
			stdout, stderr, err := runCLI(t, bin, addr, args...)
			if err == nil {
				t.Fatalf("sgt %v: expected nonzero exit, got success; stdout=%s", args, stdout)
			}
			if !strings.Contains(stderr, addr) {
				t.Errorf("sgt %v: stderr %q does not name the address %q", args, stderr, addr)
			}
			if !strings.Contains(stderr, "sgt ui") {
				t.Errorf("sgt %v: stderr %q does not mention starting `sgt ui`", args, stderr)
			}
		})
	}
}

// sgt --help (and sgt with no args) must list all four new subcommands
// alongside the existing five.
func TestHelpListsAllFourNewSubcommands(t *testing.T) {
	bin := sgtBinary(t)

	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("sgt --help: exit error %v, output:\n%s", err, out)
	}
	for _, want := range []string{"sgt dispatch", "sgt runs", "sgt run-details", "sgt create-pr"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("sgt --help output does not mention %q:\n%s", want, out)
		}
	}
}
