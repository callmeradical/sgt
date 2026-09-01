package ui

// Tests for specs/gate-fix-loop/spec.md (a-failed-gate-is-corrected-in-place):
// POST /api/run-fix starts a corrective cycle for a failed run in its real,
// existing worktree; the corrective loop repeats automatically until it
// passes or exhausts its configured budget; and every phase it records
// carries which cycle it belongs to.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

// gateFixFixture builds a project with one real git repo, a single-gate
// factory pipeline, and a fake "goose" agent standing in for both the run's
// implementation phases and its corrective "fix" phase — the same fake-CLI
// pattern dispatch_idempotency_test.go already uses. gateCmd is the repo's
// only configured gate. fixRetries, when non-zero, is written as the
// project's defaults.fix_retries.
func gateFixFixture(t *testing.T, gateCmd string, fixRetries int) (mux http.Handler, st *store.Store, repoPath, agentPath string) {
	t.Helper()

	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))

	repoPath = filepath.Join(base, "svc")
	initGitRepo(t, repoPath)

	agentPath = fakeSucceedingAgent(t, base)
	agent := agentPath

	fixRetriesLine := ""
	if fixRetries != 0 {
		fixRetriesLine = fmt.Sprintf("  fix_retries: %d\n", fixRetries)
	}

	projYAML := "name: o3\n" +
		"defaults:\n" +
		"  agent: " + agent + "\n" +
		fixRetriesLine +
		"repos:\n" +
		"  - name: svc\n" +
		"    path: " + repoPath + "\n" +
		"    factory:\n" +
		"      pipeline: [test]\n" +
		"      gates:\n" +
		"        unit: " + fmt.Sprintf("%q", gateCmd) + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "o3.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	return NewServer(st, 0).Handler(), st, repoPath, agentPath
}

// fakeSucceedingAgent writes a fake "goose" agent CLI that always exits 0.
// It stands in for whatever the run's own implementation phases and its
// corrective "fix" phase invoke; the gate — not the agent — is what this
// test suite drives pass/fail through.
func fakeSucceedingAgent(t *testing.T, dir string) string {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(binDir, "goose")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// dispatchAndFail runs a real dispatch against gateFixFixture's project and
// waits for it to reach the terminal "failed" status (the gate's command
// fails on the first, pre-fix attempt in every scenario below).
func dispatchAndFail(t *testing.T, mux http.Handler, st *store.Store, repoPath string) (runID string) {
	t.Helper()
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPath, "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	body := dispatchBody(t, map[string]interface{}{
		"project": "o3", "brief": "fix the gate", "change_id": changeID,
		"repos": []string{"svc"}, "type": "fix",
	})
	w := postDispatch(t, mux, body)
	if w.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	waitForTerminalRun(t, st, resp.TaskID)
	if got := runStatus(t, st, resp.TaskID); got != "failed" {
		t.Fatalf("run reached %q after dispatch; this suite needs the gate to fail on its first attempt", got)
	}
	return resp.TaskID
}

// waitForRunFixTerminal blocks until runID leaves "running", including the
// intermediate running state the corrective loop itself sets — unlike
// waitForTerminalRun's dispatch-only cases, a run-fix run starts already
// "failed" and is set back to "running" by handleRunFix before the loop begins.
func waitForRunFixTerminal(t *testing.T, st *store.Store, runID string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err != nil {
			t.Fatalf("reading run %s: %v", runID, err)
		}
		if run.Status == "passed" || run.Status == "failed" || run.Status == "cancelled" {
			return run.Status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal status within the deadline", runID)
	return ""
}

// fixScript replaces the fake agent binary with a script whose body runs
// once per invocation — used by scenarios that need the fix phase to have an
// observable effect on the worktree (creating a marker file the gate then
// checks for), rather than just exiting 0.
func writeFixAgent(t *testing.T, agentPath, body string) {
	t.Helper()
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// --- Scenario: starting a fix cycle re-enters the existing worktree --------

func TestRunFixReentersTheExistingWorktree(t *testing.T) {
	mux, st, repoPath, agentPath := gateFixFixture(t, "test -f .sgt/fixed.marker", 0)
	runID := dispatchAndFail(t, mux, st, repoPath)

	before := worktreesUnder(t, configuredFleetRoot(t))
	if len(before) != 1 {
		t.Fatalf("worktrees before run-fix = %v, want exactly 1", before)
	}
	branchBefore := gitCurrentBranch(t, before[0])

	// Make the fix phase create the marker the gate checks for, so the
	// cycle passes without a second cycle's involvement.
	writeFixAgent(t, agentPath, "touch .sgt/fixed.marker")

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	status := waitForRunFixTerminal(t, st, runID)
	if status != "passed" {
		t.Fatalf("run status = %q after the fix created the marker the gate checks for, want passed", status)
	}

	after := worktreesUnder(t, configuredFleetRoot(t))
	if len(after) != 1 {
		t.Fatalf("worktrees after run-fix = %v, want still exactly 1 (re-entered, not recreated)", after)
	}
	if before[0] != after[0] {
		t.Errorf("run-fix used a different worktree path: before=%s after=%s", before[0], after[0])
	}
	if got := gitCurrentBranch(t, after[0]); got != branchBefore {
		t.Errorf("run-fix switched the worktree's branch: before=%s after=%s", branchBefore, got)
	}
}

// gitCurrentBranch reads the real branch checked out at dir.
func gitCurrentBranch(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// --- Scenario: a run whose worktree no longer exists cannot be fixed -------

func TestRunFixRefusesWhenTheWorktreeIsGone(t *testing.T) {
	mux, st, repoPath, _ := gateFixFixture(t, "false", 0)
	runID := dispatchAndFail(t, mux, st, repoPath)

	root := configuredFleetRoot(t)
	if err := os.RemoveAll(filepath.Join(root, runID)); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no longer exists") {
		t.Errorf("refusal does not name that the worktree is gone: %s", w.Body.String())
	}
}

// --- Scenario: a non-resumable run cannot be fixed in place -----------------

func TestRunFixRefusesANonResumableRun(t *testing.T) {
	mux, st, _ := dispatchFixture(t)
	if err := st.CreateRun(&store.RunRecord{
		ID: "sgt-live", Project: "o3", TaskID: "sgt-live", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, mux, "/api/run-fix", `{"id":"sgt-live"}`)
	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want a refusal; a running run must not be fixed in place")
	}
	if !strings.Contains(w.Body.String(), "running") {
		t.Errorf("refusal does not say why: %s", w.Body.String())
	}
}

func TestRunFixRejectsAnUnknownRun(t *testing.T) {
	mux, _, _ := dispatchFixture(t)
	w := postJSON(t, mux, "/api/run-fix", `{"id":"sgt-nope"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// --- Scenario: the corrective agent receives the real, already-redacted failure

func TestRunFixPromptNamesTheRealFailingGateAndItsOutput(t *testing.T) {
	mux, st, repoPath, _ := gateFixFixture(t, "echo 'boom: assertion failed at line 42'; exit 1", 2)
	runID := dispatchAndFail(t, mux, st, repoPath)

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// The prompt file is written before the fake agent runs, so it is safe
	// to poll for shortly after accepting the request rather than waiting
	// for the whole run to conclude.
	worktree := filepath.Join(configuredFleetRoot(t), runID, "svc")
	promptPath := filepath.Join(worktree, ".sgt", "prompt_fix.txt")
	deadline := time.Now().Add(10 * time.Second)
	var prompt []byte
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(promptPath); err == nil {
			prompt = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if prompt == nil {
		t.Fatalf("fix prompt file %s was never written", promptPath)
	}
	if !strings.Contains(string(prompt), "unit") {
		t.Errorf("fix prompt does not name the failing gate %q: %s", "unit", prompt)
	}
	if !strings.Contains(string(prompt), "boom: assertion failed at line 42") {
		t.Errorf("fix prompt does not carry the gate's real recorded output: %s", prompt)
	}

	waitForRunFixTerminal(t, st, runID)
}

// --- Scenario: a failing cycle triggers another cycle automatically, and ---
// --- a passing cycle concludes the run passed -------------------------------

// counterFixAgent is a fix agent whose corrective effect only takes hold on
// its second invocation: it increments a counter file and only creates the
// gate's marker once the counter reaches 2. This forces exactly one cycle to
// fail and the next to pass, without any second call to POST /api/run-fix.
const counterFixAgentBody = `
n=0
if [ -f .sgt/fix-attempts ]; then n=$(cat .sgt/fix-attempts); fi
n=$((n+1))
echo "$n" > .sgt/fix-attempts
if [ "$n" -ge 2 ]; then touch .sgt/fixed.marker; fi
`

func TestRunFixRepeatsAutomaticallyUntilItPasses(t *testing.T) {
	mux, st, repoPath, agentPath := gateFixFixture(t, "test -f .sgt/fixed.marker", 0)
	runID := dispatchAndFail(t, mux, st, repoPath)

	writeFixAgent(t, agentPath, counterFixAgentBody)

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	status := waitForRunFixTerminal(t, st, runID)
	if status != "passed" {
		t.Fatalf("run status = %q, want passed once the second corrective cycle creates the marker", status)
	}

	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cyclesSeen := map[int]bool{}
	for _, p := range phases {
		if p.FixCycle > 0 {
			cyclesSeen[p.FixCycle] = true
		}
	}
	if !cyclesSeen[1] || !cyclesSeen[2] {
		t.Fatalf("expected phases recorded for both fix_cycle 1 and 2, got cycles %v (all phases: %+v)", cyclesSeen, phases)
	}
}

// Every phase across at least two corrective cycles is queryable by its real
// fix_cycle value, via the same endpoint the dashboard reads.
func TestRunDetailsCarriesFixCycleAcrossMultipleCycles(t *testing.T) {
	mux, st, repoPath, agentPath := gateFixFixture(t, "test -f .sgt/fixed.marker", 0)
	runID := dispatchAndFail(t, mux, st, repoPath)

	writeFixAgent(t, agentPath, counterFixAgentBody)

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	waitForRunFixTerminal(t, st, runID)

	dw := getRecorded(t, mux, "/api/run-details?id="+runID)
	if dw.Code != http.StatusOK {
		t.Fatalf("GET /api/run-details = %d, want 200; body=%s", dw.Code, dw.Body.String())
	}
	var details struct {
		Phases []struct {
			Name     string `json:"name"`
			FixCycle int    `json:"fix_cycle"`
		} `json:"phases"`
		FixRetriesLimit int `json:"fix_retries_limit"`
	}
	if err := json.Unmarshal(dw.Body.Bytes(), &details); err != nil {
		t.Fatalf("decoding run details: %v; body=%s", err, dw.Body.String())
	}

	byCycle := map[int]int{}
	for _, p := range details.Phases {
		byCycle[p.FixCycle]++
	}
	if byCycle[0] == 0 {
		t.Errorf("no phase carried fix_cycle 0 (the run's own original attempt): %+v", details.Phases)
	}
	if byCycle[1] == 0 {
		t.Errorf("no phase carried fix_cycle 1: %+v", details.Phases)
	}
	if byCycle[2] == 0 {
		t.Errorf("no phase carried fix_cycle 2: %+v", details.Phases)
	}
	if details.FixRetriesLimit != 5 {
		t.Errorf("fix_retries_limit = %d, want the built-in default of 5", details.FixRetriesLimit)
	}
}

// --- Scenario: exhausting the budget falls back to requiring a human -------

func TestRunFixExhaustingTheBudgetFallsBackToFailedWithADistinctReason(t *testing.T) {
	mux, st, repoPath, _ := gateFixFixture(t, "false", 2)
	runID := dispatchAndFail(t, mux, st, repoPath)

	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	originalReason := srvBlockedReason(t, mux, st, run.IntentID)

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	status := waitForRunFixTerminal(t, st, runID)
	if status != "failed" {
		t.Fatalf("run status = %q, want failed (the gate never passes, budget is 2)", status)
	}

	reason := srvBlockedReason(t, mux, st, run.IntentID)
	if !strings.Contains(reason, "corrective fix budget exhausted") {
		t.Errorf("blocked reason does not name budget exhaustion: %q", reason)
	}
	if !strings.Contains(reason, "2/2") {
		t.Errorf("blocked reason does not name the configured bound: %q", reason)
	}
	if reason == originalReason {
		t.Errorf("blocked reason repeats the pre-fix reason verbatim, want an explicit exhaustion reason: %q", reason)
	}

	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	cyclesSeen := map[int]bool{}
	for _, p := range phases {
		if p.FixCycle > 0 {
			cyclesSeen[p.FixCycle] = true
		}
	}
	if !cyclesSeen[1] || !cyclesSeen[2] {
		t.Errorf("expected phases for both configured cycles 1 and 2, got %v", cyclesSeen)
	}
	if cyclesSeen[3] {
		t.Errorf("a third cycle ran despite a configured bound of 2")
	}
}

// srvBlockedReason reads the current blocked reason for intentID's bullets,
// waiting for AdvanceBulletsForRun's write (a separate store write from the
// run's own terminal-status write) to land.
func srvBlockedReason(t *testing.T, mux http.Handler, st *store.Store, intentID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bullets, err := st.ListBulletsForIntent(intentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(bullets) > 0 && bullets[0].Status == "blocked" {
			return bullets[0].BlockedReason
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

// lastFailedPhase must stay scoped to the repo a corrective loop is bound
// to: runFixCycles fixes its worktree/RepoName once, for the loop's entire
// lifetime (Review 043), so every cycle re-reading "the current failure"
// with no repo filter could drift the loop's attention to a different
// repo's more-recently-failed phase while still executing inside the first
// repo's worktree. Passing a non-empty repoName must never return a more
// recent failure belonging to a different repo.
func TestLastFailedPhaseStaysScopedToTheGivenRepo(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(st, 0)

	if err := st.CreateRun(&store.RunRecord{ID: "run-multi", Project: "p", TaskID: "run-multi", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPhase(&store.PhaseRecord{ID: "p-a", RunID: "run-multi", Repo: "repo-a", Name: "test", Kind: "code", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	// repo-b's failure is recorded after repo-a's, so an unscoped scan would
	// report it as "the" most recent failure.
	if err := st.RecordPhase(&store.PhaseRecord{ID: "p-b", RunID: "run-multi", Repo: "repo-b", Name: "test", Kind: "code", Status: "failed"}); err != nil {
		t.Fatal(err)
	}

	got, ok := srv.lastFailedPhase("run-multi", "repo-a")
	if !ok {
		t.Fatal("expected a failure scoped to repo-a")
	}
	if got.Repo != "repo-a" {
		t.Errorf("lastFailedPhase(runID, %q) returned repo %q, want repo-a untouched by repo-b's later failure", "repo-a", got.Repo)
	}

	// The empty-repoName form (used once, by handleRunFix, to discover which
	// repo to bind a new corrective loop to) is unaffected: it still finds
	// the true most recent failure across every repo.
	unscoped, ok := srv.lastFailedPhase("run-multi", "")
	if !ok || unscoped.Repo != "repo-b" {
		t.Errorf("lastFailedPhase(runID, \"\") = %+v, want repo-b (the true most recent failure)", unscoped)
	}
}

// phaseCountByRepo tallies runID's currently recorded phases per repo.
func phaseCountByRepo(t *testing.T, st *store.Store, runID string) map[string]int {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, p := range phases {
		counts[p.Repo]++
	}
	return counts
}

// TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet is the real
// regression case behind Review 043/044 and the follow-up decision (the
// user, directly: "we dont want to run the full cycle each time. just the
// repo stage") to keep a corrective cycle scoped to its bound repo's own
// stage, never the run's full multi-stage DAG.
//
// Two DAG stages: stage-svc (repos: [svc]) then stage-other (repos:
// [other], after: [stage-svc]). svc's gate fails on the very first
// attempt, so executeRun aborts before stage-other ever runs at all -- not
// "already passed and skipped by Resume" (which a single-stage fixture
// can't distinguish, since phasePassed already skips an already-passed
// repo regardless of how the stages are scoped), but genuinely never
// attempted. Fixing svc's gate and letting the corrective cycle pass must
// NOT then advance into stage-other within that same cycle: a corrective
// cycle corrects one repo's gate, nothing more.
func TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))

	svcPath := filepath.Join(base, "svc")
	otherPath := filepath.Join(base, "other")
	initGitRepo(t, svcPath)
	initGitRepo(t, otherPath)

	agentPath := fakeSucceedingAgent(t, base)

	projYAML := fmt.Sprintf(`name: o3
defaults:
  agent: %s
  fix_retries: 2
repos:
  - name: svc
    path: %s
    factory:
      pipeline: [test]
      gates:
        unit: "test -f .sgt/svc-ok"
  - name: other
    path: %s
    factory:
      pipeline: [test]
      gates:
        unit: "true"
dag:
  name: two-stage
  stages:
    - name: stage-svc
      repos: [svc]
    - name: stage-other
      repos: [other]
      after: [stage-svc]
`, agentPath, svcPath, otherPath)
	if err := os.WriteFile(filepath.Join(cfgDir, "o3.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := NewServer(st, 0)
	mux := srv.Handler()

	proj, err := config.LoadProject("o3")
	if err != nil {
		t.Fatal(err)
	}

	const runID = "sgt-two-stage-test"
	if err := st.CreateRun(&store.RunRecord{
		ID: runID, Project: proj.Name, TaskID: runID, Type: "fix", ChangeID: "two-stage-change", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	router := handoff.NewRouter(filepath.Join(dag.FleetRoot(), runID, "handoff"))
	engine := dag.NewEngine(proj, st, router)
	ctx, cancel := context.WithCancel(context.Background())
	srv.executeRun(ctx, cancel, engine, proj, runID, "two stage test", nil, "")

	if got := runStatus(t, st, runID); got != "failed" {
		t.Fatalf("run status after the initial dispatch = %q, want failed (svc's gate must fail before any marker exists)", got)
	}
	before := phaseCountByRepo(t, st, runID)
	if before["other"] != 0 {
		t.Fatalf("expected other's stage to have never run before svc's gate passed, got %d phase(s) for other", before["other"])
	}
	if before["svc"] == 0 {
		t.Fatal("expected svc's own stage to have run (and failed)")
	}

	// The fix agent creates the marker svc's gate checks for, so the
	// corrective cycle's re-attempt genuinely passes.
	writeFixAgent(t, agentPath, "touch .sgt/svc-ok")

	w := postJSON(t, mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	status := waitForRunFixTerminal(t, st, runID)
	if status != "passed" {
		t.Fatalf("run status = %q, want passed once svc's marker makes its gate pass", status)
	}

	after := phaseCountByRepo(t, st, runID)
	if after["other"] != 0 {
		t.Errorf("other's phase count = %d after svc's corrective cycle passed, want 0: a corrective cycle bound to svc must never advance into a downstream repo's stage, even one that had never run yet", after["other"])
	}
}

// stageForRepo must find the one stage a repo belongs to and scope it down
// to that repo alone, leaving every other repo the stage bundled out; and
// must report false, not panic or return a stage naming some other repo,
// when no resolved stage mentions the given repo at all (Review 045 noted
// this fallback path had no direct test).
func TestStageForRepo(t *testing.T) {
	stages := []config.DAGStage{
		{Name: "stage-a", Repos: []string{"svc", "shared"}},
		{Name: "stage-b", Repos: []string{"other"}},
	}

	got, ok := stageForRepo(stages, "svc")
	if !ok {
		t.Fatal("expected a stage for svc")
	}
	if got.Name != "stage-a" {
		t.Errorf("stage name = %q, want stage-a", got.Name)
	}
	if len(got.Repos) != 1 || got.Repos[0] != "svc" {
		t.Errorf("scoped Repos = %v, want [svc] only -- shared must not survive the scoping", got.Repos)
	}

	if _, ok := stageForRepo(stages, "nonexistent"); ok {
		t.Error("expected ok=false for a repo no resolved stage mentions")
	}
}

