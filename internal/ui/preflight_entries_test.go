package ui

// Tests for specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles) at the entry points that restart or begin work on an
// existing record: plan approval, POST /api/run-resume and POST /api/run-fix
// (design decision 5's placement table). Each one preflights a roles-on
// project before it writes anything, refuses with the preflight's message
// when the plugin check fails, carries a passing probe's roles and version
// onto the work it starts, and never probes a project whose roles do not
// apply.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

// passingProbeOutput is a plugin list naming an enabled user-scope gauntlet
// install of the given version, with both role files.
func passingProbeOutput(t *testing.T, version string) []byte {
	t.Helper()
	return pluginListJSON(t, pluginEntryJSON{
		ID: "gauntlet@gauntlet", Version: version, Scope: "user", Enabled: true,
		InstallPath: gauntletInstall(t, "alpha", "beta"),
	})
}

// waitUntilReleased blocks until the server no longer drives runID. The
// terminal status is written before the run goroutine releases the run, and
// a resume or fix requested in between is refused as "already executing".
func (f *rolesFixture) waitUntilReleased(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.srv.isRunActive(runID) {
		if time.Now().After(deadline) {
			t.Fatalf("run %s was never released", runID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// failedBlockedRun returns the id of a failed run whose bullet is blocked and
// whose worktree is still on disk, dispatched with the fixture's (passing)
// probe. The dispatch leaves the run to fail by whatever the fixture's
// pipeline does.
func (f *rolesFixture) failedBlockedRun(t *testing.T) (runID, intentID string) {
	t.Helper()
	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d; body=%s", code, body)
	}
	runID = dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)
	if got := runStatus(t, f.st, runID); got != "failed" {
		t.Fatalf("run reached %q after dispatch; the fixture must fail", got)
	}
	f.waitUntilReleased(t, runID)

	run, err := f.st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.IntentID == "" {
		t.Fatalf("run %s has no intent", runID)
	}
	waitForBulletStatus(t, f.st, run.IntentID, "blocked")
	if info, err := os.Stat(filepath.Join(f.fleet, runID, "svc")); err != nil || !info.IsDir() {
		t.Fatalf("the failed run's worktree is not on disk: %v", err)
	}
	return runID, run.IntentID
}

// proposePlan records a proposed plan for the fixture's project by
// dispatching with no repos, and returns its intent id.
func (f *rolesFixture) proposePlan(t *testing.T) string {
	t.Helper()
	code, body := f.dispatch(t, map[string]interface{}{"repos": nil})
	if code != http.StatusAccepted {
		t.Fatalf("proposal status = %d, want 202; body=%s", code, body)
	}
	var resp struct {
		IntentID string `json:"intent_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.IntentID == "" {
		t.Fatalf("proposal response has no intent_id: %v; body=%s", err, body)
	}
	return resp.IntentID
}

func (f *rolesFixture) approve(t *testing.T, intentID string) (int, string) {
	t.Helper()
	w := doPlanAction(t, f.mux, http.MethodPost, "/api/plans/"+intentID+"/approve")
	return w.Code, w.Body.String()
}

func (f *rolesFixture) post(t *testing.T, path, runID string) (int, string) {
	t.Helper()
	w := postJSON(t, f.mux, path, fmt.Sprintf(`{"id":%q}`, runID))
	return w.Code, w.Body.String()
}

// failClaudeWhenAsked makes the fixture's stub claude exit with $STUB_EXIT when
// that is set, after logging its invocation.
func (f *rolesFixture) failClaudeWhenAsked(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(f.claude)
	if err != nil {
		t.Fatal(err)
	}
	const pass = "echo 'VERDICT: PASS'\n"
	script := string(data)
	if !strings.Contains(script, pass) {
		t.Fatalf("the fixture's stub claude no longer prints %q; update this helper", pass)
	}
	script = strings.Replace(script, pass, "if [ -n \"$STUB_EXIT\" ]; then exit \"$STUB_EXIT\"; fi\n"+pass, 1)
	if err := os.WriteFile(f.claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stubGoose writes a goose that always exits 0 next to the stub claude and
// returns its absolute path, so a non-claude project never runs a real
// harness.
func (f *rolesFixture) stubGoose(t *testing.T) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(f.claude), "goose")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func bulletsOf(t *testing.T, st *store.Store, intentID string) []store.BulletRecord {
	t.Helper()
	bullets, err := st.ListBulletsForIntent(intentID)
	if err != nil {
		t.Fatal(err)
	}
	return bullets
}

func runsForIntent(t *testing.T, st *store.Store, intentID string) []store.RunRecord {
	t.Helper()
	runs, err := st.ListRecentRuns(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.RunRecord
	for _, r := range runs {
		if r.IntentID == intentID {
			out = append(out, r)
		}
	}
	return out
}

func phasesOf(t *testing.T, st *store.Store, runID string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	return phases
}

// assertRunUntouched checks a refused resume or fix changed nothing: the run
// is still failed and not driven, its bullets are still blocked, it has no
// phase record it did not have before, and no agent started.
func (f *rolesFixture) assertRunUntouched(t *testing.T, runID, intentID string, phasesBefore []store.PhaseRecord, callsBefore int) {
	t.Helper()
	if got := runStatus(t, f.st, runID); got != "failed" {
		t.Errorf("run status = %q after the refusal, want failed", got)
	}
	if f.srv.isRunActive(runID) {
		t.Errorf("a refused request left run %s registered as executing", runID)
	}
	for _, b := range bulletsOf(t, f.st, intentID) {
		if b.Status != "blocked" {
			t.Errorf("bullet %s (%s) status = %q after the refusal, want blocked", b.ID, b.Repo, b.Status)
		}
	}
	if after := phasesOf(t, f.st, runID); !reflect.DeepEqual(after, phasesBefore) {
		t.Errorf("the refusal changed the run's phase records:\nbefore %+v\nafter  %+v", phasesBefore, after)
	}
	for _, p := range phasesOf(t, f.st, runID) {
		if p.Name == "fix" {
			t.Errorf("a refused request left a fix phase record: %+v", p)
		}
	}
	if calls := len(f.claudeCalls(t)); calls != callsBefore {
		t.Errorf("a refused request started claude %d time(s)", calls-callsBefore)
	}
}

// Scenario: A resume without the plugin leaves the run untouched.
func TestARolesResumeWithoutThePluginLeavesTheRunUntouched(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", markerGate)
	runID, intentID := f.failedBlockedRun(t)
	phasesBefore := phasesOf(t, f.st, runID)
	callsBefore := len(f.claudeCalls(t))
	probesBefore := f.probe.count()

	f.probe.set(pluginListJSON(t), nil) // the plugin is gone
	code, body := f.post(t, "/api/run-resume", runID)
	if code != http.StatusBadRequest {
		t.Fatalf("resume status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != noPluginMessage {
		t.Errorf("body = %q, want %q", strings.TrimSpace(body), noPluginMessage)
	}
	if f.probe.count() != probesBefore+1 {
		t.Errorf("the resume probed %d time(s), want 1", f.probe.count()-probesBefore)
	}
	f.assertRunUntouched(t, runID, intentID, phasesBefore, callsBefore)
}

// Scenario: A fix without the plugin leaves the run untouched.
func TestARolesFixWithoutThePluginLeavesTheRunUntouched(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", markerGate)
	runID, intentID := f.failedBlockedRun(t)
	phasesBefore := phasesOf(t, f.st, runID)
	callsBefore := len(f.claudeCalls(t))
	probesBefore := f.probe.count()

	f.probe.set(pluginListJSON(t), nil) // the plugin is gone
	code, body := f.post(t, "/api/run-fix", runID)
	if code != http.StatusBadRequest {
		t.Fatalf("run-fix status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != noPluginMessage {
		t.Errorf("body = %q, want %q", strings.TrimSpace(body), noPluginMessage)
	}
	if f.probe.count() != probesBefore+1 {
		t.Errorf("the fix probed %d time(s), want 1", f.probe.count()-probesBefore)
	}
	f.assertRunUntouched(t, runID, intentID, phasesBefore, callsBefore)
}

// Scenario: An approval without the plugin leaves the plan proposed.
func TestARolesApprovalWithoutThePluginLeavesThePlanProposed(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	intentID := f.proposePlan(t)
	bulletsBefore := bulletsOf(t, f.st, intentID)
	if len(bulletsBefore) == 0 {
		t.Fatal("the proposed plan has no bullets")
	}

	f.probe.set(pluginListJSON(t), nil) // no plugin
	code, body := f.approve(t, intentID)
	if code != http.StatusBadRequest {
		t.Fatalf("approve status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != noPluginMessage {
		t.Errorf("body = %q, want %q", strings.TrimSpace(body), noPluginMessage)
	}
	if f.probe.count() != 1 {
		t.Errorf("the approval probed %d time(s), want 1", f.probe.count())
	}

	intent, err := f.st.GetIntent(intentID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != "proposed" {
		t.Errorf("intent status = %q after the refusal, want proposed", intent.Status)
	}
	if after := bulletsOf(t, f.st, intentID); !reflect.DeepEqual(after, bulletsBefore) {
		t.Errorf("the refusal changed the plan's bullets:\nbefore %+v\nafter  %+v", bulletsBefore, after)
	}
	if runs := runsForIntent(t, f.st, intentID); len(runs) != 0 {
		t.Errorf("a refused approval left %d run(s): %+v", len(runs), runs)
	}
	if wts := worktreesUnder(t, f.fleet); len(wts) != 0 {
		t.Errorf("a refused approval left worktrees: %v", wts)
	}
	if br := runBranches(t, f.repoPath); len(br) != 0 {
		t.Errorf("a refused approval left run branches: %v", br)
	}
	if calls := f.claudeCalls(t); len(calls) != 0 {
		t.Errorf("a refused approval started claude %d time(s)", len(calls))
	}
}

// A passing approval runs the plan's work with the roles and version its own
// probe reported.
func TestAPassingRolesApprovalRecordsTheProbedVersion(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	intentID := f.proposePlan(t)

	f.probe.set(passingProbeOutput(t, "0.2.0"), nil)
	code, body := f.approve(t, intentID)
	if code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)
	if f.probe.count() != 1 {
		t.Errorf("the approval probed %d time(s), want 1", f.probe.count())
	}

	if role, ok := argAfter(callForPhase(t, f.claudeCalls(t), "build"), "--agent"); !ok || role != "gauntlet:alpha" {
		t.Errorf("build ran with --agent %q (present=%v), want gauntlet:alpha", role, ok)
	}
	agent, role, version := provenanceOf(t, phaseRecord(t, f.st, runID, "build"))
	if agent != f.claude || role != "gauntlet:alpha" || version != "0.2.0" {
		t.Errorf("build payload agent/role/plugin_version = %v/%v/%v, want %s/gauntlet:alpha/0.2.0", agent, role, version, f.claude)
	}
}

// Scenario: a passing resume's next role phase records the plugin version
// the resume's own probe reported.
func TestAPassingRolesResumeRecordsTheProbedVersionOnItsNextRolePhase(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	f.failClaudeWhenAsked(t)
	t.Setenv("STUB_EXIT", "1")
	runID, _ := f.failedBlockedRun(t)
	probesBefore := f.probe.count()

	t.Setenv("STUB_EXIT", "")
	f.probe.set(passingProbeOutput(t, "0.2.0"), nil)
	code, body := f.post(t, "/api/run-resume", runID)
	if code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body=%s", code, body)
	}
	if status := waitForRunFixTerminal(t, f.st, runID); status != "passed" {
		t.Fatalf("run status = %q after the resume, want passed", status)
	}
	f.waitUntilReleased(t, runID)
	if f.probe.count() != probesBefore+1 {
		t.Errorf("the resume probed %d time(s), want 1", f.probe.count()-probesBefore)
	}

	var resumed []store.PhaseRecord
	for _, p := range phasesOf(t, f.st, runID) {
		if p.Name == "build" && p.Kind == "agent" && p.Status == "passed" {
			resumed = append(resumed, p)
		}
	}
	if len(resumed) != 1 {
		t.Fatalf("expected one passed build record after the resume, got %d", len(resumed))
	}
	agent, role, version := provenanceOf(t, resumed[0])
	if agent != f.claude || role != "gauntlet:alpha" || version != "0.2.0" {
		t.Errorf("resumed build payload agent/role/plugin_version = %v/%v/%v, want %s/gauntlet:alpha/0.2.0", agent, role, version, f.claude)
	}
}

// A passing run-fix runs its fix phase with the roles and version its own
// probe reported.
func TestAPassingRolesFixRecordsTheProbedVersionOnItsFixPhase(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", markerGate)
	runID, _ := f.failedBlockedRun(t)
	probesBefore := f.probe.count()

	t.Setenv("STUB_TOUCH", ".sgt/fixed.marker")
	f.probe.set(passingProbeOutput(t, "0.2.0"), nil)
	code, body := f.post(t, "/api/run-fix", runID)
	if code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", code, body)
	}
	if status := waitForRunFixTerminal(t, f.st, runID); status != "passed" {
		t.Fatalf("run status = %q after the fix, want passed", status)
	}
	f.waitUntilReleased(t, runID)
	if f.probe.count() != probesBefore+1 {
		t.Errorf("the fix probed %d time(s), want 1", f.probe.count()-probesBefore)
	}

	fix := onlyCall(t, f.claudeCalls(t))
	if role, ok := argAfter(fix, "--agent"); !ok || role != "gauntlet:alpha" {
		t.Errorf("fix ran with --agent %q (present=%v), want gauntlet:alpha", role, ok)
	}
	agent, role, version := provenanceOf(t, phaseRecord(t, f.st, runID, "fix"))
	if agent != f.claude || role != "gauntlet:alpha" || version != "0.2.0" {
		t.Errorf("fix payload agent/role/plugin_version = %v/%v/%v, want %s/gauntlet:alpha/0.2.0", agent, role, version, f.claude)
	}
}

// Requirement: Requests that do not use roles are never probed — at
// approval, resume and run-fix, for an opted-out claude project and a
// non-claude project alike. One failed run serves both the resume and the
// fix, and the fix budget is one cycle, to keep the package's wall time down.
func TestApprovalResumeAndFixNeverProbeAProjectWhoseRolesDoNotApply(t *testing.T) {
	for _, kind := range []string{"opted out", "non-claude"} {
		t.Run(kind+"/approve", func(t *testing.T) {
			f := newNeverProbedFixture(t, kind, "factory:\n  pipeline: [build]")
			intentID := f.proposePlan(t)
			code, body := f.approve(t, intentID)
			if code != http.StatusOK {
				t.Fatalf("approve status = %d, want 200; body=%s", code, body)
			}
			runID := dispatchedRunID(t, body)
			waitForTerminalRun(t, f.st, runID)
			f.waitUntilReleased(t, runID)
			if f.probe.count() != 0 {
				t.Errorf("the approval probed %d time(s), want none", f.probe.count())
			}
		})
		t.Run(kind+"/resume and run-fix", func(t *testing.T) {
			f := newNeverProbedFixture(t, kind, markerGate)
			runID, _ := f.failedBlockedRun(t)
			for _, path := range []string{"/api/run-resume", "/api/run-fix"} {
				code, body := f.post(t, path, runID)
				if code != http.StatusOK {
					t.Fatalf("%s status = %d, want 200; body=%s", path, code, body)
				}
				if status := waitForRunFixTerminal(t, f.st, runID); status != "failed" {
					t.Fatalf("run status = %q after %s, want failed (the gate still fails)", status, path)
				}
				f.waitUntilReleased(t, runID)
				if f.probe.count() != 0 {
					t.Errorf("%s probed %d time(s), want none", path, f.probe.count())
				}
			}
		})
	}
}

// newNeverProbedFixture builds a rolesFixture whose roles do not apply —
// "opted out" is claude with gauntlet_roles: false, "non-claude" is a stub
// goose — with a one-cycle fix budget, and whose probe refuses if it is ever
// asked.
func newNeverProbedFixture(t *testing.T, kind, factoryYAML string) *rolesFixture {
	t.Helper()
	var f *rolesFixture
	switch kind {
	case "opted out":
		f = newRolesFixture(t, "agent: $CLAUDE\ngauntlet_roles: false\nfix_retries: 1", factoryYAML)
	case "non-claude":
		f = newRolesFixture(t, "agent: $CLAUDE\nfix_retries: 1", factoryYAML)
		goose := f.stubGoose(t)
		cfg := filepath.Join(os.Getenv("SGT_CONFIG"), "roles.yaml")
		data, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		yaml := strings.Replace(string(data), "agent: "+f.claude, "agent: "+goose, 1)
		if yaml == string(data) {
			t.Fatal("could not switch the fixture's agent to goose")
		}
		if err := os.WriteFile(cfg, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown fixture kind %q", kind)
	}
	f.probe.set(nil, errors.New("must not be probed"))
	return f
}
