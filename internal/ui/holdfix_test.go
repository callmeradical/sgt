package ui

// Tests for specs/gauntlet-roles/spec.md, requirement "An operator fix on a
// HOLD uses only the gap", scenarios "A HOLD fix prompt carries only the gap"
// and "Role prompts are not left in the worktree"
// (claude-is-the-default-harness-and-runs-gauntlet-roles, design decision 7,
// tasks 7.1 and 7.2): POST /api/run-fix on a run whose review held, driven
// end to end with real git repos and a stub claude that plays alpha and beta
// by its --agent value.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

const (
	holdChangeID = "hold-change"
	testGap      = "GAP-RETRIES-ARE-UNTESTED"
	// betaReasoning is printed by the stub beta before every verdict, so a
	// prompt that carries any of beta's output carries this marker.
	betaReasoning = "BETA-REASONING"
	// fixOutput is printed by the stub alpha on a failing attempt.
	fixOutput = "FIX-OUTPUT"
)

// holdClaude is the stub claude. It reads its role from --agent, logs its
// prompt (the last argument) to $STUB_DIR/<who>.prompts, and acts on line N
// of $STUB_DIR/<who>.seq for its Nth call:
//
//	alpha: ok (default) | fail | touch:<path> | handoff
//	beta:  pass (default) | hold:<gap> | none | exit1
//
// Its logs live outside every worktree.
const holdClaude = `#!/bin/sh
role=""; prev=""; prompt=""
for a in "$@"; do
  if [ "$prev" = "--agent" ]; then role="$a"; fi
  prev="$a"; prompt="$a"
done
case "$role" in
gauntlet:alpha) who=alpha ;;
gauntlet:beta) who=beta ;;
*) who=other ;;
esac
printf '%s\036' "$prompt" >> "$STUB_DIR/$who.prompts"
n=0
if [ -f "$STUB_DIR/$who.n" ]; then n=$(cat "$STUB_DIR/$who.n"); fi
n=$((n+1))
echo "$n" > "$STUB_DIR/$who.n"
action=""
if [ -f "$STUB_DIR/$who.seq" ]; then action=$(sed -n "${n}p" "$STUB_DIR/$who.seq"); fi
case "$who:$action" in
alpha:fail) echo "` + fixOutput + `-$n"; exit 1 ;;
alpha:touch:*) f="${action#touch:}"; mkdir -p "$(dirname "$f")"; touch "$f"; echo "touched $f"; exit 0 ;;
alpha:handoff) echo "saved my place"; echo "HANDOFF: $STUB_DIR/handoff-$n.md"; exit 0 ;;
alpha:*) echo "alpha attempt $n done"; exit 0 ;;
beta:hold:*) echo "` + betaReasoning + `-$n: the change misses the bar"; echo "VERDICT: HOLD: ${action#hold:}"; exit 0 ;;
beta:none) echo "` + betaReasoning + `-$n: undecided"; exit 0 ;;
beta:exit1) echo "` + betaReasoning + `-$n: crashed"; exit 1 ;;
beta:*) echo "` + betaReasoning + `-$n: fine"; echo "VERDICT: PASS"; exit 0 ;;
esac
exit 0
`

type holdEnv struct {
	srv     *Server
	mux     http.Handler
	st      *store.Store
	stubDir string
	fleet   string
	claude  string
}

// holdRepo is one repo of a holdEnv project: its pipeline and its gate.
type holdRepo struct {
	name     string
	pipeline string // e.g. "[build, test, review]"
	gate     string // the one gate "unit"
}

// newHoldEnv builds project "hf" over fresh git repos, with the stub claude
// named explicitly as the agent, retries 0 and the given fix_retries. dag,
// when non-empty, is appended verbatim as the project's dag: block.
func newHoldEnv(t *testing.T, fixRetries int, dagYAML string, repos ...holdRepo) holdEnv {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	stubDir := filepath.Join(base, "stub")
	binDir := filepath.Join(base, "bin")
	for _, d := range []string{cfgDir, stubDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fleet := filepath.Join(base, "fleet")
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", fleet)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SGT_WIKI_ROOT", filepath.Join(base, "wiki"))
	t.Setenv("SGT_ARTIFACTS_ROOT", filepath.Join(base, "artifacts"))
	t.Setenv("STUB_DIR", stubDir)

	claude := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claude, []byte(holdClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var b strings.Builder
	fmt.Fprintf(&b, "name: hf\ndefaults:\n  agent: %s\n  retries: 0\n  fix_retries: %d\nrepos:\n", claude, fixRetries)
	for _, r := range repos {
		p := filepath.Join(base, r.name)
		initGitRepo(t, p)
		if err := os.MkdirAll(filepath.Join(p, "openspec", "changes", holdChangeID), 0o755); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "  - name: %s\n    path: %s\n    factory:\n      pipeline: %s\n      gates:\n        unit: %q\n",
			r.name, p, r.pipeline, r.gate)
	}
	b.WriteString(dagYAML)
	if err := os.WriteFile(filepath.Join(cfgDir, "hf.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(st, 0)
	return holdEnv{srv: srv, mux: srv.Handler(), st: st, stubDir: stubDir, fleet: fleet, claude: claude}
}

// svcRepo is the one-repo fixture most scenarios use.
func svcRepo(gate string) holdRepo {
	return holdRepo{name: "svc", pipeline: "[build, test, review]", gate: gate}
}

// script sets the stub's per-call actions for who ("alpha" or "beta").
func (env holdEnv) script(t *testing.T, who string, actions ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.stubDir, who+".seq"), []byte(strings.Join(actions, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// prompts returns every prompt who received, in call order.
func (env holdEnv) prompts(t *testing.T, who string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.stubDir, who+".prompts"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\036"), "\036")
}

// fixPrompts returns the alpha prompts that were fix prompts: every alpha
// prompt that is not a build phase's.
func (env holdEnv) fixPrompts(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, p := range env.prompts(t, "alpha") {
		if !strings.HasPrefix(p, "# Phase objective: build\n") {
			out = append(out, p)
		}
	}
	return out
}

func (env holdEnv) waitIdle(t *testing.T, runID string) string {
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

// dispatchFailed dispatches repos and waits for the run to settle failed.
func (env holdEnv) dispatchFailed(t *testing.T, repos ...string) string {
	t.Helper()
	w := postDispatch(t, env.mux, dispatchBody(t, map[string]interface{}{
		"project": "hf", "brief": "retry failed webhook deliveries", "change_id": holdChangeID,
		"repos": repos, "type": "feat",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	runID := dispatchedRunID(t, w.Body.String())
	if status := env.waitIdle(t, runID); status != "failed" {
		t.Fatalf("dispatched run settled %q, want failed", status)
	}
	return runID
}

// fix posts run-fix and, on a 200, waits for the corrective loop to settle.
func (env holdEnv) fix(t *testing.T, runID string) (code int, body, status string) {
	t.Helper()
	w := postJSON(t, env.mux, "/api/run-fix", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		return w.Code, w.Body.String(), ""
	}
	return w.Code, w.Body.String(), env.waitIdle(t, runID)
}

func (env holdEnv) mustFix(t *testing.T, runID string) string {
	t.Helper()
	code, body, status := env.fix(t, runID)
	if code != http.StatusOK {
		t.Fatalf("run-fix status = %d, want 200; body=%s", code, body)
	}
	return status
}

func (env holdEnv) bullet(t *testing.T, runID, repo string) store.BulletRecord {
	t.Helper()
	run, err := env.st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	bullets, err := env.st.ListBulletsForIntent(run.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bullets {
		if b.Repo == repo {
			return b
		}
	}
	t.Fatalf("run %s has no bullet for %s", runID, repo)
	return store.BulletRecord{}
}

func fixRecords(t *testing.T, st *store.Store, runID string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Name == "fix" {
			out = append(out, p)
		}
	}
	return out
}

// assertGapOnly checks a fix prompt built on a HOLD: it carries the gap and
// the canonical brief, and nothing from beta's output, no verdict contract,
// and no output from any fix phase.
func assertGapOnly(t *testing.T, label, prompt, gap string) {
	t.Helper()
	if !strings.Contains(prompt, gap) {
		t.Errorf("%s: fix prompt does not carry the gap %q:\n%s", label, gap, prompt)
	}
	if !strings.Contains(prompt, "# Intent\n\nretry failed webhook deliveries") || !strings.Contains(prompt, "Repo: svc") {
		t.Errorf("%s: fix prompt does not carry the repo's intent brief:\n%s", label, prompt)
	}
	for _, banned := range []string{betaReasoning, "VERDICT:", fixOutput, "Recorded output"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("%s: fix prompt contains %q:\n%s", label, banned, prompt)
		}
	}
}

// dispatchHeld dispatches svc and has its first review hold with gap.
func (env holdEnv) dispatchHeld(t *testing.T, gap string) string {
	t.Helper()
	runID := env.dispatchFailed(t, "svc")
	rec := phaseRecord(t, env.st, runID, "review")
	if v, _ := payloadField(t, rec, "gauntlet_verdict"); v != "hold" {
		t.Fatalf("the dispatched review recorded verdict %q, want hold; record %+v", v, rec)
	}
	if b := env.bullet(t, runID, "svc"); b.Status != "blocked" || !strings.Contains(b.BlockedReason, gap) {
		t.Fatalf("precondition: bullet is %q with reason %q, want blocked naming %q", b.Status, b.BlockedReason, gap)
	}
	return runID
}

func payloadField(t *testing.T, rec store.PhaseRecord, key string) (string, bool) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Payload, &m); err != nil {
		t.Fatalf("decoding payload of %s: %v", rec.Name, err)
	}
	s, ok := m[key].(string)
	return s, ok
}

// --- Scenario: An operator gate fix that reaches a HOLD uses the gap -------

func TestHoldFixAnOperatorGateFixThatReachesAHoldUsesTheGap(t *testing.T) {
	env := newHoldEnv(t, 2, "", svcRepo("test -f .sgt/fixed.marker"))
	env.script(t, "alpha", "ok", "touch:.sgt/fixed.marker", "ok")
	env.script(t, "beta", "hold:"+testGap, "pass")
	runID := env.dispatchFailed(t, "svc")

	if status := env.mustFix(t, runID); status != "passed" {
		t.Fatalf("run settled %q, want passed once cycle 2 corrects the HOLD", status)
	}
	fixes := env.fixPrompts(t)
	if len(fixes) != 2 {
		t.Fatalf("got %d fix prompts, want 2 (the gate, then the HOLD)", len(fixes))
	}
	if !strings.Contains(fixes[0], `"unit"`) {
		t.Errorf("cycle 1's fix prompt is not the gate fix prompt:\n%s", fixes[0])
	}
	assertGapOnly(t, "cycle 2", fixes[1], testGap)
}

// --- Scenario: A failed HOLD fix keeps the gap -----------------------------

func TestHoldFixAFailedHoldFixKeepsTheGap(t *testing.T) {
	env := newHoldEnv(t, 2, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "fail", "ok")
	env.script(t, "beta", "hold:"+testGap, "pass")
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "passed" {
		t.Fatalf("run settled %q, want passed once cycle 2's fix succeeds", status)
	}
	fixes := env.fixPrompts(t)
	if len(fixes) != 2 {
		t.Fatalf("got %d fix prompts, want 2", len(fixes))
	}
	assertGapOnly(t, "cycle 1", fixes[0], testGap)
	assertGapOnly(t, "cycle 2", fixes[1], testGap)
}

// --- Scenario: A repeated operator fix still uses the gap ------------------

func TestHoldFixARepeatedOperatorFixStillUsesTheGap(t *testing.T) {
	env := newHoldEnv(t, 1, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "fail", "ok")
	env.script(t, "beta", "hold:"+testGap, "pass")
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "failed" {
		t.Fatalf("first correction settled %q, want failed (its only fix fails)", status)
	}
	fixes := fixRecords(t, env.st, runID)
	if len(fixes) != 1 || fixes[0].Status != "failed" {
		t.Fatalf("first correction's fix records = %+v, want one failed", fixes)
	}

	if status := env.mustFix(t, runID); status != "passed" {
		t.Fatalf("repeated correction settled %q, want passed", status)
	}
	prompts := env.fixPrompts(t)
	if len(prompts) != 2 {
		t.Fatalf("got %d fix prompts, want 2", len(prompts))
	}
	assertGapOnly(t, "repeated run-fix", prompts[1], testGap)
}

// --- Scenario: The next beta does not see the previous verdict -------------

func TestHoldFixTheNextBetaDoesNotSeeThePreviousVerdict(t *testing.T) {
	env := newHoldEnv(t, 1, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "ok")
	env.script(t, "beta", "hold:"+testGap, "pass")
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "passed" {
		t.Fatalf("run settled %q, want passed", status)
	}

	// The cycle-1 fix prompt renders the brief as the store held it when
	// cycle 1 began.
	fixes := env.fixPrompts(t)
	if len(fixes) != 1 {
		t.Fatalf("got %d fix prompts, want 1", len(fixes))
	}
	if !strings.Contains(fixes[0], "Status: pending\n") {
		t.Errorf("the bullet was not pending when cycle 1 began; fix prompt:\n%s", fixes[0])
	}

	reviews := env.prompts(t, "beta")
	if len(reviews) != 2 {
		t.Fatalf("got %d review prompts, want 2", len(reviews))
	}
	for _, line := range strings.Split(reviews[1], "\n") {
		if strings.HasPrefix(line, "Blocked reason") && strings.Contains(line, testGap) {
			t.Errorf("cycle 1's review brief carries the previous HOLD: %q", line)
		}
	}
	if !strings.Contains(reviews[1], "Status: pending\n") {
		t.Errorf("cycle 1's review brief does not show the bullet pending:\n%s", reviews[1])
	}
}

// --- Scenario: run-fix refuses a review that produced no verdict -----------

func TestRunFixRefusesAReviewThatProducedNoVerdict(t *testing.T) {
	for _, tc := range []struct{ name, beta string }{
		{"no verdict line", "none"},
		{"reviewer process failed", "exit1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHoldEnv(t, 2, "", svcRepo("true"))
			env.script(t, "alpha", "ok")
			env.script(t, "beta", tc.beta)
			runID := env.dispatchFailed(t, "svc")
			before := env.bullet(t, runID, "svc")

			code, body, _ := env.fix(t, runID)
			if code != http.StatusConflict {
				t.Fatalf("run-fix status = %d, want 409; body=%s", code, body)
			}
			want := "the review on svc produced no verdict; resume the run to review again"
			if !strings.Contains(body, want) {
				t.Errorf("run-fix body = %q, want it to contain %q", body, want)
			}
			assertRefusalChangedNothing(t, env, runID, before)
		})
	}
}

// --- Scenario: run-fix refuses a handed-off build ---------------------------

func TestRunFixRefusesAHandedOffBuild(t *testing.T) {
	env := newHoldEnv(t, 2, "", svcRepo("true"))
	env.script(t, "alpha", "handoff")
	runID := env.dispatchFailed(t, "svc")
	if p, ok := payloadField(t, phaseRecord(t, env.st, runID, "build"), "handoff_path"); !ok || p == "" {
		t.Fatalf("precondition: the build recorded no handoff_path")
	}
	before := env.bullet(t, runID, "svc")

	code, body, _ := env.fix(t, runID)
	if code != http.StatusConflict {
		t.Fatalf("run-fix status = %d, want 409; body=%s", code, body)
	}
	want := "the build on svc handed off; resume the run to continue"
	if !strings.Contains(body, want) {
		t.Errorf("run-fix body = %q, want it to contain %q", body, want)
	}
	assertRefusalChangedNothing(t, env, runID, before)
}

// --- Scenario: run-fix refuses when only unrun repos remain ----------------

func TestRunFixRefusesWhenOnlyUnrunReposRemain(t *testing.T) {
	env := newHoldEnv(t, 2, `dag:
  name: two-stage
  stages:
    - name: stage-a
      repos: [a]
    - name: stage-b
      repos: [b]
      after: [stage-a]
    - name: stage-c
      repos: [c]
      after: [stage-b]
`,
		holdRepo{name: "a", pipeline: "[test]", gate: "test -f .sgt/a-ok"},
		holdRepo{name: "b", pipeline: "[test]", gate: "true"},
		holdRepo{name: "c", pipeline: "[test]", gate: "true"},
	)
	env.script(t, "alpha", "touch:.sgt/a-ok")
	runID := env.dispatchFailed(t, "a", "b", "c")

	// The first correction fixes a, and the guard leaves b and c for a
	// resume.
	if status := env.mustFix(t, runID); status != "failed" {
		t.Fatalf("first correction settled %q, want failed (b and c have not run)", status)
	}
	before := env.bullet(t, runID, "a")

	code, body, _ := env.fix(t, runID)
	if code != http.StatusConflict {
		t.Fatalf("run-fix status = %d, want 409; body=%s", code, body)
	}
	want := "nothing to fix in a; b, c have not run yet — resume the run"
	if !strings.Contains(body, want) {
		t.Errorf("run-fix body = %q, want it to contain %q", body, want)
	}
	if n := len(fixRecords(t, env.st, runID)); n != 1 {
		t.Errorf("fix records = %d after the refusal, want only the first correction's 1", n)
	}
	if run, _ := env.st.GetRun(runID); run.Status != "failed" {
		t.Errorf("run status = %q after the refusal, want failed", run.Status)
	}
	if after := env.bullet(t, runID, "a"); after.Status != before.Status || after.BlockedReason != before.BlockedReason {
		t.Errorf("the refusal changed the bullet: %+v -> %+v", before, after)
	}
}

// assertRefusalChangedNothing checks a refused run-fix recorded no fix
// phase and left the run and its bullet as they were.
func assertRefusalChangedNothing(t *testing.T, env holdEnv, runID string, before store.BulletRecord) {
	t.Helper()
	if n := len(fixRecords(t, env.st, runID)); n != 0 {
		t.Errorf("a refused run-fix recorded %d fix phases, want 0", n)
	}
	if run, _ := env.st.GetRun(runID); run.Status != "failed" {
		t.Errorf("run status = %q after the refusal, want failed", run.Status)
	}
	if after := env.bullet(t, runID, "svc"); after.Status != before.Status || after.BlockedReason != before.BlockedReason {
		t.Errorf("the refusal changed the bullet: %+v -> %+v", before, after)
	}
}

// --- Scenario: Exhaustion on a HOLD names the review -----------------------

func TestHoldFixExhaustionOnAHoldNamesTheReview(t *testing.T) {
	env := newHoldEnv(t, 1, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "ok")
	env.script(t, "beta", "hold:"+testGap, "hold:still missing")
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "failed" {
		t.Fatalf("run settled %q, want failed", status)
	}
	want := "corrective fix budget exhausted (1/1 attempts) on svc; review still HOLD: still missing"
	if b := env.bullet(t, runID, "svc"); b.BlockedReason != want {
		t.Errorf("blocked reason = %q, want %q", b.BlockedReason, want)
	}
}

// When the last cycle's fix itself failed, the exhaustion reason still names
// the HOLD the correction was working on.
func TestHoldFixExhaustionAfterAFailedFixNamesTheHold(t *testing.T) {
	env := newHoldEnv(t, 1, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "fail")
	env.script(t, "beta", "hold:"+testGap)
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "failed" {
		t.Fatalf("run settled %q, want failed", status)
	}
	want := "corrective fix budget exhausted (1/1 attempts) on svc; review still HOLD: " + testGap
	if b := env.bullet(t, runID, "svc"); b.BlockedReason != want {
		t.Errorf("blocked reason = %q, want %q", b.BlockedReason, want)
	}
}

// A gate correction's exhaustion names the repo.
func TestRunFixExhaustionOnAGateNamesTheRepo(t *testing.T) {
	env := newHoldEnv(t, 1, "", holdRepo{name: "svc", pipeline: "[test]", gate: "false"})
	runID := env.dispatchFailed(t, "svc")

	if status := env.mustFix(t, runID); status != "failed" {
		t.Fatalf("run settled %q, want failed", status)
	}
	want := "corrective fix budget exhausted (1/1 attempts) on svc; gate still failing"
	if b := env.bullet(t, runID, "svc"); b.BlockedReason != want {
		t.Errorf("blocked reason = %q, want %q", b.BlockedReason, want)
	}
}

// --- Scenario: Role prompts are not left in the worktree -------------------

func TestHoldFixRolePromptsAreNotLeftInTheWorktree(t *testing.T) {
	env := newHoldEnv(t, 1, "", svcRepo("true"))
	env.script(t, "alpha", "ok", "ok")
	env.script(t, "beta", "hold:"+testGap, "pass")
	runID := env.dispatchHeld(t, testGap)

	if status := env.mustFix(t, runID); status != "passed" {
		t.Fatalf("run settled %q, want passed", status)
	}

	worktree := filepath.Join(env.fleet, runID, "svc")
	err := filepath.WalkDir(worktree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, banned := range []string{"VERDICT:", testGap} {
			if strings.Contains(string(data), banned) {
				t.Errorf("worktree file %s contains %q", path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	envs, err := env.st.ListEnvelopesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range envs {
		if e.Stage != "fix" && e.Stage != "review" || e.PhaseID == "" {
			continue
		}
		if e.Stage == "fix" && e.Repo != "svc" {
			continue
		}
		seen[e.Stage]++
		for _, a := range e.Artifacts {
			if !filepath.IsAbs(a) || a == worktree || strings.HasPrefix(a, worktree+string(filepath.Separator)) {
				t.Errorf("%s envelope artifact %q is not a path outside the worktree %s", e.Stage, a, worktree)
			}
		}
	}
	if seen["fix"] == 0 || seen["review"] < 2 {
		t.Fatalf("envelopes seen by stage = %v, want a fix and both reviews", seen)
	}
}

// --- Scenario: A HOLD fix prompt carries only the gap ----------------------

func TestHoldFixPromptCarriesOnlyTheGap(t *testing.T) {
	payload, err := json.Marshal(map[string]interface{}{
		"raw_output":       betaReasoning + ": long reasoning\nVERDICT: HOLD: " + testGap,
		"gauntlet_verdict": "hold",
		"gauntlet_gap":     testGap,
		"role":             "gauntlet:beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := store.PhaseRecord{
		Repo: "svc", Name: "review", Kind: "agent", Status: "failed",
		Error: "gauntlet:beta HOLD: " + testGap, Payload: payload,
	}
	const brief = "# Intent\n\nretry failed webhook deliveries\n\n# Bullet\n\nRepo: svc\n"

	prompt := basisFixPrompt(failure, brief)
	if !strings.HasPrefix(prompt, brief) {
		t.Errorf("prompt does not open with the canonical brief:\n%s", prompt)
	}
	if !strings.Contains(prompt, testGap) {
		t.Errorf("prompt does not carry the gap:\n%s", prompt)
	}
	for _, banned := range []string{betaReasoning, "VERDICT:"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("prompt contains %q:\n%s", banned, prompt)
		}
	}
	if got := holdFixPrompt(brief, testGap); got != prompt {
		t.Errorf("a HOLD basis is not holdFixPrompt(brief, gap):\n%s\nwant:\n%s", prompt, got)
	}
}

// A review failure that is not a HOLD never puts beta's raw output in the
// fix prompt; the phase's sgt-authored error stands in for it.
func TestAReviewFixPromptWithoutAGapUsesTheRecordedError(t *testing.T) {
	payload, err := json.Marshal(map[string]interface{}{
		"raw_output":       betaReasoning + ": rambling",
		"gauntlet_verdict": "pass",
		"role":             "gauntlet:beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := store.PhaseRecord{
		Repo: "svc", Name: "review", Kind: "agent", Status: "failed",
		Error: "review wrote an envelope sgt did not author", Payload: payload,
	}
	prompt := basisFixPrompt(failure, "brief")
	if strings.Contains(prompt, betaReasoning) {
		t.Errorf("prompt carries the review's raw output:\n%s", prompt)
	}
	if !strings.Contains(prompt, failure.Error) {
		t.Errorf("prompt does not carry the recorded error %q:\n%s", failure.Error, prompt)
	}
}

// --- The basis looks back past fix records ---------------------------------

func TestFixBasisLooksBackPastFixRecords(t *testing.T) {
	hold := store.PhaseRecord{ID: "r", Repo: "svc", Name: "review", Status: "failed"}
	gate := store.PhaseRecord{ID: "g", Repo: "svc", Name: "unit", Kind: "code", Status: "failed"}
	fix := func(id string) store.PhaseRecord {
		return store.PhaseRecord{ID: id, Repo: "svc", Name: "fix", Status: "failed"}
	}
	passedFix := store.PhaseRecord{ID: "pf", Repo: "svc", Name: "fix", Status: "passed"}
	other := store.PhaseRecord{ID: "o", Repo: "other", Name: "unit", Kind: "code", Status: "failed"}

	for _, tc := range []struct {
		name   string
		phases []store.PhaseRecord
		want   string
	}{
		{"a failed review is its own basis", []store.PhaseRecord{gate, passedFix, hold}, "r"},
		{"failed fixes look back to the HOLD", []store.PhaseRecord{hold, fix("f1"), fix("f2")}, "r"},
		{"a later gate failure is the basis", []store.PhaseRecord{hold, passedFix, gate}, "g"},
		{"another repo's failure is ignored", []store.PhaseRecord{hold, fix("f1"), other}, "r"},
		{"only fixes leaves the newest fix", []store.PhaseRecord{fix("f1"), fix("f2")}, "f2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fixBasis(tc.phases, "svc")
			if !ok || got.ID != tc.want {
				t.Errorf("fixBasis = %q (ok %v), want %q", got.ID, ok, tc.want)
			}
		})
	}
	if _, ok := fixBasis([]store.PhaseRecord{other}, "svc"); ok {
		t.Error("fixBasis found a basis for a repo with no failures")
	}
}
