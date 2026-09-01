package ui

// Tests for specs/gate-fix-loop/spec.md (a-failed-gate-is-corrected-in-place):
// POST /api/run-fix starts a corrective cycle for a failed run in its real,
// existing worktree; the corrective loop repeats automatically until it
// passes or exhausts its configured budget; and every phase it records
// carries which cycle it belongs to.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
