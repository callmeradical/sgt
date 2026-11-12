package ui

// Tests for specs/verification-freshness/spec.md, requirements "A run never
// ends passed without a passing result for every required phase" and "The
// test entry is satisfied by gate records" (claude-is-the-default-harness-
// and-runs-gauntlet-roles, design decisions 7 and 8): the terminal guard in
// executeRun (driven with a fake stageRunner for states the engine cannot
// produce) and, end to end, through POST /api/dispatch, POST /api/run-fix and
// POST /api/run-resume with real git repos and a stub agent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

const guardChangeID = "guard-change"

type guardEnv struct {
	srv       *Server
	mux       http.Handler
	st        *store.Store
	repos     map[string]string
	agentPath string
}

// newGuardEnv builds project "o3" from projYAML, in which "@AGENT@" is the
// stub agent's absolute path and "@<repo>@" is the path of a fresh git repo
// created for each name in repoNames. The stub agent runs script after
// freshnessAgentPrelude, so $PHASE is review, fix or work.
func newGuardEnv(t *testing.T, projYAML, script string, repoNames ...string) guardEnv {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SGT_WIKI_ROOT", filepath.Join(base, "wiki"))
	t.Setenv("SGT_ARTIFACTS_ROOT", filepath.Join(base, "artifacts"))

	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(binDir, "goose")
	writeFreshnessAgent(t, agentPath, script)

	repos := map[string]string{}
	pairs := []string{"@AGENT@", agentPath}
	for _, name := range repoNames {
		p := filepath.Join(base, name)
		initGitRepo(t, p)
		if err := os.MkdirAll(filepath.Join(p, "openspec", "changes", guardChangeID), 0o755); err != nil {
			t.Fatal(err)
		}
		repos[name] = p
		pairs = append(pairs, "@"+name+"@", p)
	}
	yaml := strings.NewReplacer(pairs...).Replace(projYAML)
	if err := os.WriteFile(filepath.Join(cfgDir, "o3.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(st, 0)
	return guardEnv{srv: srv, mux: srv.Handler(), st: st, repos: repos, agentPath: agentPath}
}

// waitIdle blocks until runID has a terminal status and this process no
// longer drives it, so its bullets and envelopes are final.
func (env guardEnv) waitIdle(t *testing.T, runID string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		run, err := env.st.GetRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		switch run.Status {
		case "passed", "failed", "cancelled":
			if !env.srv.isRunActive(runID) {
				return run.Status
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not settle (status %q)", runID, run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (env guardEnv) dispatch(t *testing.T, repos ...string) (runID, status string) {
	t.Helper()
	body := dispatchBody(t, map[string]interface{}{
		"project": "o3", "brief": "guard the terminal status", "change_id": guardChangeID,
		"repos": repos, "type": "feat",
	})
	w := postDispatch(t, env.mux, body)
	if w.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.TaskID, env.waitIdle(t, resp.TaskID)
}

func (env guardEnv) post(t *testing.T, path, runID string) string {
	t.Helper()
	w := postJSON(t, env.mux, path, fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%s", path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func (env guardEnv) bullets(t *testing.T, runID string) []store.BulletRecord {
	t.Helper()
	run, err := env.st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.IntentID == "" {
		t.Fatalf("run %s has no intent", runID)
	}
	bullets, err := env.st.ListBulletsForIntent(run.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	return bullets
}

func deliveredEnvelopes(t *testing.T, st *store.Store, runID string) int {
	t.Helper()
	envs, err := st.ListEnvelopesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range envs {
		if e.Type == "run.delivered" {
			n++
		}
	}
	return n
}

func recordsForRepo(t *testing.T, st *store.Store, runID, repo string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Repo == repo {
			out = append(out, p)
		}
	}
	return out
}

func assertPassedAndDelivered(t *testing.T, env guardEnv, runID, status string) {
	t.Helper()
	if status != "passed" {
		var reasons []string
		if run, err := env.st.GetRun(runID); err == nil && run.IntentID != "" {
			for _, b := range env.bullets(t, runID) {
				reasons = append(reasons, b.BlockedReason)
			}
		}
		t.Fatalf("run status = %q, want passed; blocked reasons: %q", status, reasons)
	}
	if n := deliveredEnvelopes(t, env.st, runID); n != 1 {
		t.Errorf("run.delivered envelopes = %d, want 1", n)
	}
}

// --- executeRun with a fake stage runner -----------------------------------

// seedingStageRunner writes the phase records chosen for each stage, in
// order, and reports the stage as passed: the end-of-run state the real
// engine would never leave, but the guard must still refuse.
type seedingStageRunner struct {
	st      *store.Store
	records map[string][]store.PhaseRecord // stage name -> records
}

func (f *seedingStageRunner) RunStage(ctx context.Context, runID string, stage *config.DAGStage) error {
	for i, rec := range f.records[stage.Name] {
		rec.RunID = runID
		rec.ID = fmt.Sprintf("%s-%s-%d-%s", runID, stage.Name, i, rec.Name)
		if err := f.st.RecordPhase(&rec); err != nil {
			return err
		}
		time.Sleep(2 * time.Millisecond) // distinct, ordered CreatedAt
	}
	return nil
}

// staleGateRun drives executeRun over a one-repo project whose stage "leaves"
// gate unit passed before a newer build record, and returns the store, the
// run id and the reason the run was recorded with.
func staleGateRun(t *testing.T) (st *store.Store, runID, reason string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SGT_WIKI_ROOT", filepath.Join(base, "wiki"))
	t.Setenv("SGT_ARTIFACTS_ROOT", filepath.Join(base, "artifacts"))

	proj := &config.Project{
		Name: "guard-fake",
		Repos: map[string]config.Repo{"svc": {Factory: &config.FactoryConfig{
			Pipeline: []string{"build", "test"},
			Gates:    map[string]string{"unit": "true"},
		}}},
	}
	var err error
	st, err = store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	runID = "sgt-guard-stale"
	if err := st.CreateIntent(&store.IntentRecord{ID: runID + "-intent", Project: proj.Name, Statement: "stale", Status: "in_progress"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBullet(&store.BulletRecord{ID: runID + "-b1", IntentID: runID + "-intent", Repo: "svc", Position: 1, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(&store.RunRecord{
		ID: runID, Project: proj.Name, TaskID: runID, Type: "feat", ChangeID: "c", IntentID: runID + "-intent", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	fake := &seedingStageRunner{st: st, records: map[string][]store.PhaseRecord{
		"custom-dispatch": {
			{Repo: "svc", Name: "build", Kind: "agent", Status: "passed"},
			{Repo: "svc", Name: "unit", Kind: "code", Status: "passed"},
			{Repo: "svc", Name: "build", Kind: "agent", Status: "passed"},
		},
	}}
	srv := NewServer(st, 0)
	srv.awaitTerminalReason(runID)
	ctx, cancel := context.WithCancel(context.Background())
	srv.executeRun(ctx, cancel, fake, proj, runID, "stale gate", []string{"svc"}, "", preflightResult{})
	reason, _ = srv.takeTerminalReason(runID)
	return st, runID, reason
}

// Scenario: A stale gate fails the run.
func TestTerminalGuardAStaleGateFailsTheRun(t *testing.T) {
	st, runID, reason := staleGateRun(t)

	if got := runStatus(t, st, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed: gate unit's pass predates the newest build", got)
	}
	want := "phase unit on svc has no passing result since the last code change"
	if reason != want {
		t.Errorf("terminal reason = %q, want %q", reason, want)
	}
	run, _ := st.GetRun(runID)
	bullets, err := st.ListBulletsForIntent(run.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bullets {
		if b.Status != "blocked" || b.BlockedReason != want {
			t.Errorf("bullet %s = %s (%q), want blocked with %q", b.ID, b.Status, b.BlockedReason, want)
		}
	}
}

// Scenario: A guard failure records no delivery.
func TestTerminalGuardAGuardFailureRecordsNoDelivery(t *testing.T) {
	st, runID, _ := staleGateRun(t)

	if got := runStatus(t, st, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}
	if n := deliveredEnvelopes(t, st, runID); n != 0 {
		t.Errorf("run.delivered envelopes = %d, want 0 for a run the guard failed", n)
	}
}

// --- end to end: dispatch --------------------------------------------------

func oneRepoYAML(pipeline string, gates ...string) string {
	y := "name: o3\ndefaults:\n  agent: @AGENT@\nrepos:\n  - name: svc\n    path: @svc@\n    factory:\n      pipeline: " + pipeline + "\n"
	if len(gates) > 0 {
		y += "      gates:\n"
		for _, g := range gates {
			y += "        " + g + "\n"
		}
	}
	return y
}

const reviewPasses = `if [ "$PHASE" = review ]; then echo "VERDICT: PASS"; fi
`

// Scenario: A review before test does not make gates stale.
func TestTerminalGuardAReviewBeforeTestDoesNotMakeGatesStale(t *testing.T) {
	env := newGuardEnv(t, oneRepoYAML("[build, review, test]", `unit: "true"`), reviewPasses, "svc")
	runID, status := env.dispatch(t, "svc")
	assertPassedAndDelivered(t, env, runID, status)
}

// Scenario: An agent phase after test is trusted.
func TestTerminalGuardAnAgentPhaseAfterTestIsTrusted(t *testing.T) {
	env := newGuardEnv(t, oneRepoYAML("[build, test, docs]", `unit: "true"`), "", "svc")
	runID, status := env.dispatch(t, "svc")
	assertPassedAndDelivered(t, env, runID, status)
	if recs := phaseRecords(t, env.st, runID, "docs", "agent"); len(recs) == 0 {
		t.Error("docs never ran; the scenario needs an agent phase after the gates")
	}
}

// Scenario: Gates are not required when the pipeline has no test.
func TestTerminalGuardGatesAreNotRequiredWhenThePipelineHasNoTest(t *testing.T) {
	env := newGuardEnv(t, oneRepoYAML("[plan, build]", `unit: "false"`), "", "svc")
	runID, status := env.dispatch(t, "svc")
	assertPassedAndDelivered(t, env, runID, status)
}

// Scenario: Configured gates satisfy the test entry.
func TestTerminalGuardConfiguredGatesSatisfyTheTestEntry(t *testing.T) {
	env := newGuardEnv(t, oneRepoYAML("[build, test]", `lint: "true"`, `unit: "true"`), "", "svc")
	runID, status := env.dispatch(t, "svc")
	assertPassedAndDelivered(t, env, runID, status)
	if recs := phaseRecords(t, env.st, runID, "test", "code"); len(recs) != 0 {
		t.Errorf("%d record(s) named test, want none: the configured gates are the test entry", len(recs))
	}
}

// Scenario: A default pipeline with every phase passing passes.
func TestTerminalGuardADefaultPipelineWithEveryPhasePassingPasses(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  gates:\n    unit: \"true\"")
	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	env := guardEnv{srv: f.srv, mux: f.mux, st: f.st}
	status := env.waitIdle(t, runID)
	assertPassedAndDelivered(t, env, runID, status)
	for _, name := range []string{"plan", "build", "review"} {
		if len(phaseRecords(t, f.st, runID, name, "agent")) == 0 {
			t.Errorf("no %s record; the default pipeline with roles on runs it", name)
		}
	}
}

// Scenario: A bullet repo outside the DAG is reported, not left for resume.
func TestTerminalGuardABulletRepoOutsideTheDAGIsReportedNotLeftForResume(t *testing.T) {
	yaml := `name: o3
defaults:
  agent: @AGENT@
repos:
  - name: svc
    path: @svc@
    factory:
      pipeline: [build, test]
      gates:
        unit: "true"
  - name: x
    path: @x@
    factory:
      pipeline: [build, test]
      gates:
        unit: "true"
dag:
  name: svc-only
  stages:
    - name: stage-svc
      repos: [svc]
`
	env := newGuardEnv(t, yaml, "", "svc", "x")
	runID, status := env.dispatch(t, "svc", "x")

	if status != "failed" {
		t.Fatalf("run status = %q, want failed: x has a bullet no stage can run", status)
	}
	want := "repo x has a bullet but is in no stage of this project's DAG; it cannot run"
	for _, b := range env.bullets(t, runID) {
		if b.Status != "blocked" || b.BlockedReason != want {
			t.Errorf("bullet %s (%s) = %s %q, want blocked %q", b.ID, b.Repo, b.Status, b.BlockedReason, want)
		}
		if strings.Contains(strings.ToLower(b.BlockedReason), "resume") {
			t.Errorf("bullet %s's reason %q says to resume; no resume can run x", b.ID, b.BlockedReason)
		}
	}
	if n := deliveredEnvelopes(t, env.st, runID); n != 0 {
		t.Errorf("run.delivered envelopes = %d, want 0", n)
	}
}

// --- end to end: run-fix and resume -----------------------------------------

// twoRepoYAML is a non-DAG project over a and b. a's gate passes only once
// its fix has written .sgt/a-ok; b's gate always passes.
const twoRepoYAML = `name: o3
defaults:
  agent: @AGENT@
  fix_retries: 2
repos:
  - name: a
    path: @a@
    factory:
      pipeline: [build, test]
      gates:
        unit: "test -f .sgt/a-ok"
  - name: b
    path: @b@
    factory:
      pipeline: [build, test]
      gates:
        unit: "true"
`

const fixWritesAOK = `if [ "$PHASE" = fix ]; then touch .sgt/a-ok; fi
`

// dispatchAThenCorrect dispatches over [a, b], which fails on a before b
// runs, then corrects a with run-fix.
func dispatchAThenCorrect(t *testing.T, env guardEnv) (runID, status string) {
	t.Helper()
	runID, status = env.dispatch(t, "a", "b")
	if status != "failed" {
		t.Fatalf("dispatch status = %q, want failed on a's gate", status)
	}
	if n := len(recordsForRepo(t, env.st, runID, "b")); n != 0 {
		t.Fatalf("b has %d record(s) after the dispatch, want 0: the fixture needs b never to have run", n)
	}
	env.post(t, "/api/run-fix", runID)
	return runID, env.waitIdle(t, runID)
}

// Scenario: An unrun repo fails the run.
func TestTerminalGuardAnUnrunRepoFailsTheRun(t *testing.T) {
	env := newGuardEnv(t, twoRepoYAML, fixWritesAOK, "a", "b")
	runID, status := dispatchAThenCorrect(t, env)

	if n := len(recordsForRepo(t, env.st, runID, "b")); n != 0 {
		t.Errorf("b has %d record(s) after a's correction, want 0: a fix cycle runs only its own repo's stage", n)
	}
	if status != "failed" {
		t.Fatalf("run status = %q, want failed: b has not run", status)
	}
	want := "corrected a; b have not run yet — resume the run to continue"
	for _, b := range env.bullets(t, runID) {
		if b.Status == "green" {
			t.Errorf("bullet %s (%s) is green, but b has no work behind it", b.ID, b.Repo)
		}
		if b.BlockedReason != want {
			t.Errorf("bullet %s (%s) reason = %q, want %q", b.ID, b.Repo, b.BlockedReason, want)
		}
	}
	if n := deliveredEnvelopes(t, env.st, runID); n != 0 {
		t.Errorf("run.delivered envelopes = %d, want 0", n)
	}
}

// Scenario: Resume after "has not run yet" runs the remaining repo.
func TestTerminalGuardResumeAfterHasNotRunYetRunsTheRemainingRepo(t *testing.T) {
	env := newGuardEnv(t, twoRepoYAML, fixWritesAOK, "a", "b")
	runID, status := dispatchAThenCorrect(t, env)
	if status != "failed" {
		t.Fatalf("run status after the correction = %q, want failed", status)
	}
	aBefore := len(recordsForRepo(t, env.st, runID, "a"))

	env.post(t, "/api/run-resume", runID)
	// Resume clears the blocked bullets of every repo it will run, b
	// included, before the run starts.
	for _, b := range env.bullets(t, runID) {
		if b.Status == "blocked" {
			t.Errorf("bullet %s (%s) is still blocked once the resume started", b.ID, b.Repo)
		}
	}
	status = env.waitIdle(t, runID)

	if !hasRepoRecord(recordsForRepo(t, env.st, runID, "b"), "build", "agent") {
		t.Error("b has no build record after the resume")
	}
	if !hasRepoRecord(recordsForRepo(t, env.st, runID, "b"), "unit", "code") {
		t.Error("b has no gate record after the resume")
	}
	if aAfter := len(recordsForRepo(t, env.st, runID, "a")); aAfter != aBefore {
		t.Errorf("a has %d record(s) after the resume, want %d: its passed phases are skipped", aAfter, aBefore)
	}
	assertPassedAndDelivered(t, env, runID, status)
	for _, b := range env.bullets(t, runID) {
		if b.Status != "green" {
			t.Errorf("bullet %s (%s) = %s, want green", b.ID, b.Repo, b.Status)
		}
	}
}

func hasRepoRecord(recs []store.PhaseRecord, name, kind string) bool {
	for _, p := range recs {
		if p.Name == name && p.Kind == kind {
			return true
		}
	}
	return false
}

// Scenario: A passed run-fix records delivery.
func TestTerminalGuardAPassedRunFixRecordsDelivery(t *testing.T) {
	yaml := oneRepoYAML("[build, test]", `unit: "test -f .sgt/svc-ok"`)
	env := newGuardEnv(t, yaml, `if [ "$PHASE" = fix ]; then touch .sgt/svc-ok; fi
`, "svc")
	runID, status := env.dispatch(t, "svc")
	if status != "failed" {
		t.Fatalf("dispatch status = %q, want failed", status)
	}
	if n := deliveredEnvelopes(t, env.st, runID); n != 0 {
		t.Fatalf("run.delivered envelopes after the failed dispatch = %d, want 0", n)
	}

	env.post(t, "/api/run-fix", runID)
	status = env.waitIdle(t, runID)
	assertPassedAndDelivered(t, env, runID, status)
}

// Scenario: A failed fix later made good does not fail the run.
//
// The only fix exits 1 (after writing the marker the gate needs), so the
// correction is exhausted with the repo's newest fix failed. The resume then
// re-runs the now-stale gate, which passes, and the run passes.
func TestTerminalGuardAFailedFixLaterMadeGoodDoesNotFailTheRun(t *testing.T) {
	yaml := "name: o3\ndefaults:\n  agent: @AGENT@\n  fix_retries: 1\nrepos:\n  - name: svc\n    path: @svc@\n    factory:\n      pipeline: [build, test]\n      gates:\n        unit: \"test -f .sgt/svc-ok\"\n"
	env := newGuardEnv(t, yaml, `if [ "$PHASE" = fix ]; then touch .sgt/svc-ok; exit 1; fi
`, "svc")
	runID, status := env.dispatch(t, "svc")
	if status != "failed" {
		t.Fatalf("dispatch status = %q, want failed", status)
	}
	env.post(t, "/api/run-fix", runID)
	if status = env.waitIdle(t, runID); status != "failed" {
		t.Fatalf("run-fix status = %q, want failed: its only fix failed", status)
	}
	fixes := phaseRecords(t, env.st, runID, "fix", "agent")
	if len(fixes) == 0 || fixes[len(fixes)-1].Status != "failed" {
		t.Fatalf("newest fix record = %v, want failed", fixes)
	}

	env.post(t, "/api/run-resume", runID)
	status = env.waitIdle(t, runID)
	assertPassedAndDelivered(t, env, runID, status)
}
