package ui

// Tests for specs/verification-freshness/spec.md, requirement "A verification
// counts only if it passed after the last code change" (claude-is-the-default-
// harness-and-runs-gauntlet-roles, design decision 8), at the HTTP seams:
// POST /api/run-fix (new gate records carrying their fix cycle),
// POST /api/run-resume (skipped) and GET /api/run-details (resume_skips).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

// freshnessOpts describes the one-repo project a freshness scenario runs.
type freshnessOpts struct {
	// agentName, when set, is written as defaults.agent verbatim and the stub
	// is put first on PATH under that name; otherwise defaults.agent is the
	// stub's absolute path.
	agentName  string
	pipeline   string // YAML flow list, e.g. "[test, review]"
	gates      map[string]string
	fixRetries int
	// script is the stub agent's body. Before it runs, $PHASE is set to
	// "review", "fix" or "work" from the prompt, and $N to how many times
	// that phase has been invoked in this worktree (1-based).
	script string
}

type freshnessEnv struct {
	srv       *Server
	mux       http.Handler
	st        *store.Store
	repoPath  string
	agentPath string
}

// freshnessFixture builds a project with one real git repo ("svc") and a
// goose stub agent, isolating every path a run reaching a terminal status
// touches.
func freshnessFixture(t *testing.T, o freshnessOpts) freshnessEnv {
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

	repoPath := filepath.Join(base, "svc")
	initGitRepo(t, repoPath)

	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(binDir, "goose")
	writeFreshnessAgent(t, agentPath, o.script)

	agent := agentPath
	if o.agentName != "" {
		agent = o.agentName
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	var gates strings.Builder
	names := make([]string, 0, len(o.gates))
	for name := range o.gates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&gates, "        %s: %q\n", name, o.gates[name])
	}
	fixRetries := ""
	if o.fixRetries != 0 {
		fixRetries = fmt.Sprintf("  fix_retries: %d\n", o.fixRetries)
	}
	projYAML := "name: o3\n" +
		"defaults:\n" +
		"  agent: " + agent + "\n" +
		fixRetries +
		"repos:\n" +
		"  - name: svc\n" +
		"    path: " + repoPath + "\n" +
		"    factory:\n" +
		"      pipeline: " + o.pipeline + "\n" +
		"      gates:\n" + gates.String()
	if err := os.WriteFile(filepath.Join(cfgDir, "o3.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(st, 0)
	return freshnessEnv{srv: srv, mux: srv.Handler(), st: st, repoPath: repoPath, agentPath: agentPath}
}

// waitUntilIdle blocks until this process no longer drives runID. A run's
// terminal status is written before its goroutine unregisters it, and the
// next run-fix or resume is refused until it has.
func (env freshnessEnv) waitUntilIdle(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for env.srv.isRunActive(runID) {
		if time.Now().After(deadline) {
			t.Fatalf("run %s is still registered as executing after reaching a terminal status", runID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dispatchAndFail is the package's dispatchAndFail, then waitUntilIdle.
func (env freshnessEnv) dispatchAndFail(t *testing.T) string {
	t.Helper()
	runID := dispatchAndFail(t, env.mux, env.st, env.repoPath)
	env.waitUntilIdle(t, runID)
	return runID
}

const freshnessAgentPrelude = `#!/bin/sh
case "$*" in
*'"review" phase'*) PHASE=review ;;
*"Fix the underlying cause"*) PHASE=fix ;;
*) PHASE=work ;;
esac
mkdir -p .sgt
N=0
if [ -f ".sgt/count-$PHASE" ]; then N=$(cat ".sgt/count-$PHASE"); fi
N=$((N+1))
echo "$N" > ".sgt/count-$PHASE"
`

func writeFreshnessAgent(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(freshnessAgentPrelude+script+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// reviewFailsUntil is a script fragment: the review phase exits 1 until the
// marker file exists, and otherwise passes with a PASS verdict.
func reviewFailsUntil(marker string) string {
	return `if [ "$PHASE" = review ]; then
  if [ -f ` + marker + ` ]; then echo "VERDICT: PASS"; exit 0; fi
  exit 1
fi
`
}

func startRunFix(t *testing.T, env freshnessEnv, runID string) string {
	t.Helper()
	w := postJSON(t, env.mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	status := waitForRunFixTerminal(t, env.st, runID)
	env.waitUntilIdle(t, runID)
	return status
}

func phaseRecords(t *testing.T, st *store.Store, runID, name, kind string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

func hasRecordInCycle(recs []store.PhaseRecord, cycle int) bool {
	for _, p := range recs {
		if p.FixCycle == cycle {
			return true
		}
	}
	return false
}

func cyclesOf(recs []store.PhaseRecord) []int {
	var out []int
	for _, p := range recs {
		out = append(out, p.FixCycle)
	}
	return out
}

// Scenario: A gate broken by a fix is re-run.
//
// Gate G passes, the review fails. The cycle-1 fix breaks G; the cycle-2 fix
// repairs it and makes the review pass. G must be re-run in cycle 2, not
// skipped on the strength of its pre-fix pass.
func TestFreshnessAGateBrokenByAFixIsRerun(t *testing.T) {
	env := freshnessFixture(t, freshnessOpts{
		pipeline:   "[test, review]",
		gates:      map[string]string{"g": "test ! -f .sgt/broken"},
		fixRetries: 3,
		script: reviewFailsUntil(".sgt/review-ok") + `
if [ "$PHASE" = fix ]; then
  if [ "$N" -eq 1 ]; then touch .sgt/broken; else rm -f .sgt/broken; touch .sgt/review-ok; fi
fi
`,
	})
	runID := env.dispatchAndFail(t)
	if recs := phaseRecords(t, env.st, runID, "g", "code"); len(recs) != 1 || recs[0].Status != "passed" {
		t.Fatalf("after dispatch, g records = %+v; this scenario needs g to have passed once", recs)
	}

	startRunFix(t, env, runID)

	g := phaseRecords(t, env.st, runID, "g", "code")
	if !hasRecordInCycle(g, 2) {
		t.Errorf("g has records in fix cycles %v, want one in cycle 2: the cycle-1 fix broke it, so its earlier pass must not be trusted", cyclesOf(g))
	}
}

// Scenario: A failed fix phase does not let stale gates count.
func TestFreshnessAFailedFixPhaseDoesNotLetStaleGatesCount(t *testing.T) {
	env := freshnessFixture(t, freshnessOpts{
		pipeline:   "[test, review]",
		gates:      map[string]string{"g": "true"},
		fixRetries: 3,
		script: reviewFailsUntil(".sgt/review-ok") + `
if [ "$PHASE" = fix ]; then
  if [ "$N" -eq 1 ]; then exit 1; fi
  touch .sgt/review-ok
fi
`,
	})
	runID := env.dispatchAndFail(t)

	startRunFix(t, env, runID)

	fixes := phaseRecords(t, env.st, runID, "fix", "agent")
	var failedFix *store.PhaseRecord
	for i := range fixes {
		if fixes[i].FixCycle == 1 && fixes[i].Status == "failed" {
			failedFix = &fixes[i]
		}
	}
	if failedFix == nil {
		t.Fatalf("no failed cycle-1 fix record; fix records: %+v", fixes)
	}
	var cycle2 *store.PhaseRecord
	for _, p := range phaseRecords(t, env.st, runID, "g", "code") {
		if p.FixCycle == 2 {
			rec := p
			cycle2 = &rec
		}
	}
	if cycle2 == nil {
		t.Fatalf("g has no fix-cycle-2 record (cycles %v): a failed fix may have changed the code, so g's earlier pass is stale",
			cyclesOf(phaseRecords(t, env.st, runID, "g", "code")))
	}
	if !cycle2.CreatedAt.After(failedFix.CreatedAt) {
		t.Errorf("g's cycle-2 record (%v) is not newer than the failed fix (%v)", cycle2.CreatedAt, failedFix.CreatedAt)
	}
}

// gatesAAndB is the two-gate fixture of "A gate fix re-runs every gate of its
// repo": a passes, b fails until the fix creates its marker.
func gatesAAndB(agentName string) freshnessOpts {
	return freshnessOpts{
		agentName:  agentName,
		pipeline:   "[test]",
		gates:      map[string]string{"a": "true", "b": "test -f .sgt/b-ok"},
		fixRetries: 2,
		script:     `if [ "$PHASE" = fix ]; then touch .sgt/b-ok; fi`,
	}
}

func assertBothGatesRanInCycle1(t *testing.T, env freshnessEnv, runID string) {
	t.Helper()
	for _, gate := range []string{"a", "b"} {
		recs := phaseRecords(t, env.st, runID, gate, "code")
		if !hasRecordInCycle(recs, 1) {
			t.Errorf("gate %s has records in fix cycles %v, want one in cycle 1: a fix re-runs every gate of its repo", gate, cyclesOf(recs))
		}
	}
}

// Scenario: A gate fix re-runs every gate of its repo.
func TestFreshnessAGateFixRerunsEveryGateOfItsRepo(t *testing.T) {
	env := freshnessFixture(t, gatesAAndB(""))
	runID := env.dispatchAndFail(t)

	if status := startRunFix(t, env, runID); status != "passed" {
		t.Errorf("run status = %q after the fix made b pass, want passed", status)
	}
	assertBothGatesRanInCycle1(t, env, runID)
}

// Scenario: The rule applies to named harnesses.
func TestFreshnessTheRuleAppliesToNamedHarnesses(t *testing.T) {
	env := freshnessFixture(t, gatesAAndB("goose"))
	runID := env.dispatchAndFail(t)

	startRunFix(t, env, runID)
	assertBothGatesRanInCycle1(t, env, runID)
}

// Scenario: Resume after an exhausted correction re-runs stale gates.
func TestFreshnessResumeAfterAnExhaustedCorrectionRerunsStaleGates(t *testing.T) {
	env := freshnessFixture(t, freshnessOpts{
		pipeline:   "[test, review]",
		gates:      map[string]string{"a": "true", "b": "test -f .sgt/b-ok"},
		fixRetries: 1,
		script: `if [ "$PHASE" = fix ]; then exit 1; fi
if [ "$PHASE" = review ]; then echo "VERDICT: PASS"; fi
`,
	})
	runID := env.dispatchAndFail(t)

	if status := startRunFix(t, env, runID); status != "failed" {
		t.Fatalf("run status = %q after a fix budget of 1 with a failing fix, want failed", status)
	}
	fixes := phaseRecords(t, env.st, runID, "fix", "agent")
	if len(fixes) == 0 || fixes[len(fixes)-1].Status != "failed" {
		t.Fatalf("the correction did not end on a failed fix; fix records: %+v", fixes)
	}
	lastFix := fixes[len(fixes)-1]

	// The operator repairs the worktree by hand, then resumes.
	worktree := filepath.Join(configuredFleetRoot(t), runID, "svc")
	if err := os.WriteFile(filepath.Join(worktree, ".sgt", "b-ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, env.mux, "/api/run-resume", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("resume status = %d; body=%s", w.Code, w.Body.String())
	}
	if status := waitForRunFixTerminal(t, env.st, runID); status != "passed" {
		t.Errorf("run status = %q after resume, want passed once every gate and the review pass", status)
	}

	for _, gate := range []string{"a", "b"} {
		recs := phaseRecords(t, env.st, runID, gate, "code")
		newest := recs[len(recs)-1]
		if !newest.CreatedAt.After(lastFix.CreatedAt) || newest.Status != "passed" {
			t.Errorf("gate %s's newest record (%s at %v) is not a pass newer than the failed fix (%v): resume must re-run stale gates",
				gate, newest.Status, newest.CreatedAt, lastFix.CreatedAt)
		}
	}
}

// ---- resume's skipped list and the run details' resume_skips ---------------

func seedRun(t *testing.T, st *store.Store, runID string, recs []store.PhaseRecord) {
	t.Helper()
	if err := st.CreateRun(&store.RunRecord{
		ID: runID, Project: "o3", TaskID: runID, Brief: "b", Status: "failed",
	}); err != nil {
		t.Fatal(err)
	}
	for i := range recs {
		rec := recs[i]
		rec.RunID = runID
		if err := st.RecordPhase(&rec); err != nil {
			t.Fatal(err)
		}
	}
}

func resumeSkipped(t *testing.T, mux http.Handler, runID string) []string {
	t.Helper()
	w := postJSON(t, mux, "/api/run-resume", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("resume status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding resume response %s: %v", w.Body.String(), err)
	}
	return resp.Skipped
}

func detailsResumeSkips(t *testing.T, mux http.Handler, runID string) []string {
	t.Helper()
	w := getRecorded(t, mux, "/api/run-details?id="+runID)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/run-details = %d; body=%s", w.Code, w.Body.String())
	}
	var details struct {
		ResumeSkips []string `json:"resume_skips"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &details); err != nil {
		t.Fatalf("decoding run details %s: %v", w.Body.String(), err)
	}
	return details.ResumeSkips
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Scenario: Resume's skipped list names only fresh passes.
func TestFreshnessResumeSkippedNamesOnlyFreshPasses(t *testing.T) {
	mux, st, _ := dispatchFixture(t)
	seedRun(t, st, "sgt-stale-review", []store.PhaseRecord{
		{ID: "p1", Repo: "svc", Name: "build", Kind: "agent", Status: "passed"},
		{ID: "p2", Repo: "svc", Name: "review", Kind: "agent", Status: "passed"},
		{ID: "p3", Repo: "svc", Name: "fix", Kind: "agent", Status: "passed"},
	})

	skipped := resumeSkipped(t, mux, "sgt-stale-review")
	if contains(skipped, "review") {
		t.Errorf("skipped = %v names review, whose pass predates a later fix", skipped)
	}
	if !contains(skipped, "build") {
		t.Errorf("skipped = %v does not name build, a passed work phase resume will not re-run", skipped)
	}
}

// Scenario: The run details' skip list agrees with resume.
//
// The seeded run has a fresh work pass (build), a gate whose pass a later fix
// made stale (lint), a gate that passed after the fix (unit), and a passed
// gate named like the failed agent phase (plan).
func TestFreshnessRunDetailsSkipListAgreesWithResume(t *testing.T) {
	mux, st, _ := dispatchFixture(t)
	seedRun(t, st, "sgt-agree", []store.PhaseRecord{
		{ID: "p1", Repo: "svc", Name: "plan", Kind: "agent", Status: "failed"},
		{ID: "p2", Repo: "svc", Name: "build", Kind: "agent", Status: "passed"},
		{ID: "p3", Repo: "svc", Name: "lint", Kind: "code", Status: "passed"},
		{ID: "p4", Repo: "svc", Name: "fix", Kind: "agent", Status: "failed"},
		{ID: "p5", Repo: "svc", Name: "unit", Kind: "code", Status: "passed"},
	})

	details := detailsResumeSkips(t, mux, "sgt-agree")
	skipped := resumeSkipped(t, mux, "sgt-agree")

	sortedCopy := func(in []string) []string {
		out := append([]string{}, in...)
		sort.Strings(out)
		return out
	}
	if got, want := strings.Join(sortedCopy(details), ","), strings.Join(sortedCopy(skipped), ","); got != want {
		t.Errorf("run details resume_skips = %v, resume skipped = %v; they must agree", details, skipped)
	}
	if got := strings.Join(sortedCopy(skipped), ","); got != "build,unit" {
		t.Errorf("skipped = %v, want build and unit: lint's pass predates the fix, and plan failed", skipped)
	}
}
