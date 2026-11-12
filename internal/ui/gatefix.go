package ui

// handleRunFix and the corrective-loop orchestration it starts
// (a-failed-gate-is-corrected-in-place). A failed gate today either gets a
// verbatim retry (POST /api/run-resume, which only helps a genuine flake) or
// a brand-new dispatch that discards the failed run's own worktree and
// branch. This is a third path: fix the underlying cause in place, in the
// run's existing worktree, then re-run from the failing phase onward.
//
// Only the first cycle is operator-triggered (this endpoint). Every cycle
// after that is entered automatically by runFixCycles, up to
// Project.ResolvedFixRetries — mirroring how RunAgentPhase already retries
// within its own turn, one level up: a whole gate-fix-retest cycle instead
// of one phase invocation.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/runner"
	"github.com/callmeradical/sgt/internal/store"
)

// handleRunFix answers POST /api/run-fix ({"id": "<run-id>"}), starting a
// corrective cycle for a failed/blocked run.
//
// Preconditions mirror handleRunResume exactly (resumable status, not
// already active, project loads), plus one more: the worktree belonging to
// the run's last failed phase must still exist. A corrective cycle re-enters
// that worktree; it does not recreate it. A run whose worktree has already
// been reclaimed has no in-place fix to offer — a fresh dispatch is the
// existing answer for that case (out of scope per proposal.md).
func (srv *Server) handleRunFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		http.Error(w, "invalid request body: an id is required", http.StatusBadRequest)
		return
	}
	runID := strings.TrimSpace(req.ID)

	run, err := srv.Store.GetRun(runID)
	if err != nil || run == nil {
		http.Error(w, fmt.Sprintf("no run %q", runID), http.StatusNotFound)
		return
	}

	if !isResumable(run.Status) {
		http.Error(w, fmt.Sprintf(
			"run %s is %s and cannot be fixed in place; resumable statuses are %s",
			run.ID, run.Status, strings.Join(ResumableStatuses, ", ")),
			http.StatusConflict)
		return
	}

	if srv.isRunActive(run.ID) {
		http.Error(w, fmt.Sprintf("run %s is already executing", run.ID), http.StatusConflict)
		return
	}

	proj, err := config.LoadProject(run.Project)
	if err != nil {
		http.Error(w, fmt.Sprintf("loading project %s: %v", run.Project, err), http.StatusBadRequest)
		return
	}
	// The gauntlet plugin preflight (design decision 5), before
	// UpdateRunStatus and before any bullet leaves "blocked", so a refusal
	// leaves the run and its bullets exactly as they were.
	roles, version, err := srv.gauntletPreflight(r.Context(), proj)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pf := preflightResult{Roles: roles, Version: version}

	failure, ok := srv.lastFailedPhase(runID, "")
	if !ok {
		http.Error(w, fmt.Sprintf("run %s has no failed phase to correct", runID), http.StatusConflict)
		return
	}

	// A failure a fix cannot correct is refused before anything changes
	// (design decision 7): the run stays as it was, and no fix is recorded.
	phases, err := srv.Store.ListPhasesForRun(runID)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading the phases of run %s: %v", runID, err), http.StatusInternalServerError)
		return
	}
	if refusal := srv.fixRefusal(run, proj, phases, failure.Repo, pf.Roles); refusal != "" {
		http.Error(w, refusal, http.StatusConflict)
		return
	}

	// A corrective cycle re-enters the exact worktree the failure happened
	// in; it never creates one. If the fleet has already reclaimed it, there
	// is nothing to fix in place — a fresh dispatch is the existing answer.
	worktree := dag.FleetDir(runID, failure.Repo)
	if info, statErr := os.Stat(worktree); statErr != nil || !info.IsDir() {
		http.Error(w, fmt.Sprintf(
			"run %s's worktree for repo %s no longer exists at %s; a fix in place requires the original worktree — dispatch a fresh run instead",
			runID, failure.Repo, worktree),
			http.StatusConflict)
		return
	}

	// Before cycle 1, the repo's blocked bullet goes back to pending, the
	// same reconciliation resume does (issue #20): RenderIntentBrief reads
	// the bullet's live status, so otherwise the next beta's brief would
	// carry the previous HOLD as its "Blocked reason".
	srv.clearBlockedBullet(run, failure.Repo)

	_ = srv.Store.UpdateRunStatus(run.ID, "running")

	ctx, cancel := context.WithCancel(context.Background())
	srv.registerRun(run.ID, cancel)

	go srv.runFixCycles(ctx, cancel, proj, run.ID, failure.Repo, pf)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "fixing",
		"task_id": run.ID,
		"run_id":  run.ID,
		"project": proj.Name,
		"repo":    failure.Repo,
	})
}

// runFixCycles drives the corrective loop for runID, starting at cycle 1.
// The operator triggered cycle 1 via handleRunFix; every cycle after that is
// entered automatically here, not by another call to POST /api/run-fix.
//
// Each cycle: build the fix agent's prompt from the last failed phase's own
// already-recorded, already-redacted output; run a "fix" agent phase in the
// run's existing worktree, using the run's own agent CLI and model (never a
// different one — proposal.md's "out of scope"); then re-enter RunStage with
// Resume=true, exactly as plain Resume does, so phasePassed skips whatever
// already passed and only the failing phase onward re-runs.
//
// If the re-attempted stage now passes, the terminal guard (verifyFix) runs
// over every repo the run is accountable for: when it holds, the run records
// its run.delivered envelope and concludes passed; otherwise it concludes
// failed with the guard's reason, which says to resume when the only gap is
// repos that have not run yet. If it
// fails again and cycles remain under the configured bound, the loop repeats
// automatically. If the bound is exhausted, the run falls back to its
// existing failed/blocked outcome, with a reason that says the fix budget
// was exhausted rather than repeating the original gate-failure text as if
// no correction had been attempted.
//
// pf is the caller's gauntlet preflight result: every cycle's fix phase and
// re-run stage carry its roles and version, and each cycle's engine its
// FixCycle.
func (srv *Server) runFixCycles(ctx context.Context, cancel context.CancelFunc, proj *config.Project, runID, repoName string, pf preflightResult) {
	defer cancel()
	defer srv.finishRun(runID)

	run, err := srv.Store.GetRun(runID)
	if err != nil || run == nil {
		return
	}

	limit := proj.ResolvedFixRetries(repoName)
	router := handoff.NewRouter(filepath.Join(dag.FleetRoot(), runID, "handoff"))
	worktree := dag.FleetDir(runID, repoName)

	for cycle := 1; cycle <= limit; cycle++ {
		if ctx.Err() != nil {
			srv.recordTerminalRun(runID, "cancelled")
			return
		}

		// Each cycle follows the failure it finds, re-derived from the store
		// (design decision 7), so a failed fix never replaces the failure it
		// was correcting, within this correction or across a repeated
		// run-fix.
		phases, err := srv.Store.ListPhasesForRun(runID)
		if err != nil {
			break
		}
		basis, ok := fixBasis(phases, repoName)
		if !ok {
			break
		}

		// Re-run only repoName's own stage, scoped to repoName alone -- never
		// the run's full multi-repo DAG. A corrective cycle is bound to one
		// repo for its entire lifetime (Review 043/044); re-running every
		// repo's stage each cycle let the loop's attention silently drift to
		// a repo it wasn't actually correcting. stageForRepo finds the one
		// stage repoName belongs to and returns a copy scoped to just that
		// repo, leaving every other repo in that stage (and every other
		// stage entirely) untouched by this cycle.
		stages := resolveStages(proj, srv.reposForRun(runID), run.Brief)
		stage, inStage := stageForRepo(stages, repoName)
		// A HOLD is corrected against the bar the review judged by: the
		// brief the build was given.
		brief := dag.PhaseBrief(srv.Store, runID, "build", &stage, repoName, proj.Repos[repoName])

		pr := &runner.PhaseRunner{
			Store:    srv.Store,
			Router:   router,
			Worktree: worktree,
			RepoName: repoName,
			RunID:    runID,
			AgentCLI: proj.Defaults.Agent,
			Model:    proj.Defaults.Model,
			FixCycle: cycle,

			GauntletRoles:   pf.Roles,
			GauntletVersion: pf.Version,
		}

		if _, _, err := pr.RunAgentPhase(ctx, "fix", basisFixPrompt(basis, brief), proj.ResolvedRetries(repoName)); err != nil {
			// The fix phase itself errored (not the gate it was meant to
			// address). Treat this as this cycle's failure and let the loop
			// continue to the next cycle rather than aborting the whole
			// corrective loop early.
			continue
		}

		if !inStage {
			// repoName isn't in any resolved stage -- should not happen,
			// since the run's own failure was recorded against repoName.
			// Treat it as this cycle's failure rather than running nothing
			// silently.
			continue
		}

		engine := dag.NewEngine(proj, srv.Store, router)
		engine.Resume = true
		engine.GauntletRoles = pf.Roles
		engine.GauntletVersion = pf.Version
		engine.FixCycle = cycle

		cycleFailed := engine.RunStage(ctx, runID, &stage) != nil

		// Commit whatever the fix and re-run produced, pass or fail, for the
		// same reason executeRun commits before reporting a dispatch's own
		// failure: an uncommitted worktree is eligible for reclaim, and real
		// agent work must survive that even when this cycle did not resolve
		// the gate. Only repoName's worktree could have changed this cycle.
		_, _, _ = dag.CommitRunOutput(context.Background(), runID, repoName, fmt.Sprintf("sgt: corrective fix cycle %d", cycle))

		if !cycleFailed {
			if ctx.Err() != nil {
				srv.recordTerminalRun(runID, "cancelled")
				return
			}
			// The cycle corrected repoName's own stage, and only that
			// stage: a corrective cycle never advances into another repo
			// (the "just the repo stage" decision). Before the run may be
			// passed, the terminal guard checks every repo it is
			// accountable for (design decisions 7 and 8).
			if ok, reason := srv.verifyFix(run, proj, repoName, pf.Roles); !ok {
				srv.recordTerminalRunWithReason(runID, "failed", reason)
				return
			}
			srv.recordDelivery(proj, runID)
			srv.recordTerminalRun(runID, "passed")
			return
		}
	}

	srv.recordTerminalRunWithReason(runID, "failed", srv.exhaustionReason(runID, repoName, limit))
}

// exhaustionReason is the reason a correction that ran out of cycles is
// failed with: it names the repo and the failure the last cycle left, a HOLD
// by its gap, anything else as today's gate failure.
func (srv *Server) exhaustionReason(runID, repoName string, limit int) string {
	reason := fmt.Sprintf("corrective fix budget exhausted (%d/%d attempts) on %s", limit, limit, repoName)
	if phases, err := srv.Store.ListPhasesForRun(runID); err == nil {
		if basis, ok := fixBasis(phases, repoName); ok {
			if gap, held := holdGap(basis); held {
				return reason + "; review still HOLD: " + gap
			}
		}
	}
	return reason + "; gate still failing"
}

// fixRefusal is why handleRunFix refuses to start a correction on repoName,
// or "" when a fix can correct its failure (design decision 7):
//
//   - the repo already passes the terminal guard on its own, so the run
//     failed only because other repos have not run, which a resume does;
//   - its failure is a gauntlet:beta review with no verdict, which a resume
//     reviews again (a process-level reviewer failure included);
//   - its failure is a build that handed off, which a resume continues.
func (srv *Server) fixRefusal(run *store.RunRecord, proj *config.Project, phases []store.PhaseRecord, repoName string, roles bool) string {
	if ok, _ := dag.RunVerified(phases, proj, []string{repoName}, roles); ok {
		if _, unrun := srv.unrunRepos(run, proj, phases, repoName); len(unrun) > 0 {
			return fmt.Sprintf("nothing to fix in %s; %s have not run yet — resume the run", repoName, strings.Join(unrun, ", "))
		}
		return fmt.Sprintf("nothing to fix in %s; resume the run", repoName)
	}
	basis, ok := fixBasis(phases, repoName)
	if !ok {
		return ""
	}
	switch {
	case isReview(basis) && payloadString(basis, "role") == betaRole && payloadString(basis, "gauntlet_verdict") == "":
		return fmt.Sprintf("the review on %s produced no verdict; resume the run to review again", repoName)
	case basis.Name == "build" && payloadString(basis, "handoff_path") != "":
		return fmt.Sprintf("the build on %s handed off; resume the run to continue", repoName)
	}
	return ""
}

// betaRole is the gauntlet role a review records as its payload "role".
const betaRole = "gauntlet:beta"

// clearBlockedBullet moves repoName's bullet on run's intent from blocked to
// pending. Any other bullet, and a bullet in any other status, is left
// alone.
func (srv *Server) clearBlockedBullet(run *store.RunRecord, repoName string) {
	if run.IntentID == "" {
		return
	}
	bullets, err := srv.Store.ListBulletsForIntent(run.IntentID)
	if err != nil {
		log.Printf("sgt: run-fix: loading the bullets of run %s: %v", run.ID, err)
		return
	}
	for _, b := range bullets {
		if b.Repo == repoName && b.Status == "blocked" {
			if err := srv.Store.AdvanceBulletStatus(b.ID, "pending", ""); err != nil {
				log.Printf("sgt: run-fix: clearing blocked state for bullet %q: %v", b.ID, err)
			}
		}
	}
}

// verifyFix is the terminal guard for a corrective cycle that passed
// repoName's stage. Accountable repos with no records that a resume can run
// are not failures of the correction: when every other repo verifies, the
// reason names repoName as corrected and those repos as left for a resume.
// Any other guard failure, including a bullet repo no stage can run, is
// reported as dag.RunVerified words it.
func (srv *Server) verifyFix(run *store.RunRecord, proj *config.Project, repoName string, roles bool) (bool, string) {
	phases, err := srv.Store.ListPhasesForRun(run.ID)
	if err != nil {
		return false, fmt.Sprintf("the phases of run %s could not be read to verify them: %v", run.ID, err)
	}
	accountable, unrun := srv.unrunRepos(run, proj, phases, repoName)
	left := map[string]bool{}
	for _, r := range unrun {
		left[r] = true
	}
	var checked []string
	for _, r := range accountable {
		if !left[r] {
			checked = append(checked, r)
		}
	}

	if ok, reason := dag.RunVerified(phases, proj, checked, roles); !ok {
		return false, reason
	}
	if len(unrun) > 0 {
		return false, fmt.Sprintf("corrected %s; %s have not run yet — resume the run to continue", repoName, strings.Join(unrun, ", "))
	}
	return true, ""
}

// unrunRepos returns the repos run is accountable for and, of those, the
// ones other than repoName that have no phase records but that a stage can
// run: the repos a resume would run next.
func (srv *Server) unrunRepos(run *store.RunRecord, proj *config.Project, phases []store.PhaseRecord, repoName string) (accountable, unrun []string) {
	accountable = srv.accountableRepos(run, proj)
	stages := resolveStages(proj, accountable, run.Brief)
	hasRecords := map[string]bool{}
	for _, p := range phases {
		hasRecords[p.Repo] = true
	}
	for _, r := range accountable {
		if _, runnable := stageForRepo(stages, r); runnable && r != repoName && !hasRecords[r] {
			unrun = append(unrun, r)
		}
	}
	return accountable, unrun
}

// stageForRepo finds the one stage among stages that repoName belongs to,
// and returns a copy scoped to repoName alone -- Name/After/Brief/TD kept
// as the original stage's, Repos replaced with just [repoName] -- so running
// it touches only repoName's worktree, never any other repo the original
// stage bundled alongside it.
func stageForRepo(stages []config.DAGStage, repoName string) (config.DAGStage, bool) {
	for _, s := range stages {
		for _, r := range s.Repos {
			if r == repoName {
				scoped := s
				scoped.Repos = []string{repoName}
				return scoped, true
			}
		}
	}
	return config.DAGStage{}, false
}

// lastFailedPhase returns the most recently recorded failed phase for runID.
// When repoName is non-empty, only that repo's phases are considered — the
// corrective loop (runFixCycles) is bound to one repo's worktree for its
// entire lifetime, so every cycle must keep reading that same repo's
// failure, never a different repo's that failed more recently (a run's
// "current" failure can otherwise drift to a repo the loop isn't actually
// running in). An empty repoName considers every repo the run touched — used
// once, by handleRunFix, to discover which repo to bind the loop to in the
// first place. ListPhasesForRun orders by created_at ascending, so the last
// match in iteration order is the most recent one.
func (srv *Server) lastFailedPhase(runID, repoName string) (store.PhaseRecord, bool) {
	phases, err := srv.Store.ListPhasesForRun(runID)
	if err != nil {
		return store.PhaseRecord{}, false
	}
	var last store.PhaseRecord
	found := false
	for _, p := range phases {
		if p.Status != "failed" {
			continue
		}
		if repoName != "" && p.Repo != repoName {
			continue
		}
		last = p
		found = true
	}
	return last, found
}

// fixPrompt builds the corrective agent's brief from failure's own recorded
// output. That output was already redacted and bounded at the source
// (RunCodeGate/RunAgentPhase call redact.Text/redact.Truncate before
// anything is recorded, and RecordPhase redacts again as a choke point) —
// this reads the recorded value as-is; it redacts nothing itself.
func fixPrompt(failure store.PhaseRecord) string {
	kind := "phase"
	if failure.Kind == "code" {
		kind = "gate"
	}
	output := phaseOutputText(failure)
	if isReview(failure) {
		// A review's output is the critic's; the builder never receives it
		// (design decision 7). sgt's own error text says why it failed.
		output = failure.Error
	}
	if output == "" {
		output = "(no output was recorded for this failure)"
	}
	return fmt.Sprintf(
		"The %s %q failed for repo %s. Fix the underlying cause in this worktree "+
			"so that %s passes when it is run again. Do not weaken or remove the %s "+
			"itself; make the real change it demands.\n\nRecorded output from the failing %s:\n%s",
		kind, failure.Name, failure.Repo, failure.Name, kind, kind, output,
	)
}

// fixBasis is the failed phase a corrective cycle on repoName corrects
// (design decision 7): the repo's newest failed phase, unless that is a fix,
// in which case the newest failed phase before it that is not a fix. So a
// failed fix never replaces the failure it was correcting, and the basis
// survives a repeated run-fix because it is read from the records. When the
// repo has only failed fixes, the newest of them is the basis. phases is in
// recorded order.
func fixBasis(phases []store.PhaseRecord, repoName string) (store.PhaseRecord, bool) {
	var newest store.PhaseRecord
	found := false
	for i := len(phases) - 1; i >= 0; i-- {
		p := phases[i]
		if p.Repo != repoName || p.Status != "failed" {
			continue
		}
		if p.Name != "fix" {
			return p, true
		}
		if !found {
			newest, found = p, true
		}
	}
	return newest, found
}

// basisFixPrompt is the fix agent's prompt for basis: holdFixPrompt for a
// review that held with a gap, else fixPrompt. brief is the canonical brief
// the HOLD was judged against.
func basisFixPrompt(basis store.PhaseRecord, brief string) string {
	if gap, held := holdGap(basis); held {
		return holdFixPrompt(brief, gap)
	}
	return fixPrompt(basis)
}

// holdGap returns the gap of a review that recorded a HOLD verdict with a
// gap.
func holdGap(p store.PhaseRecord) (string, bool) {
	if !isReview(p) || payloadString(p, "gauntlet_verdict") != "hold" {
		return "", false
	}
	gap := payloadString(p, "gauntlet_gap")
	return gap, gap != ""
}

// holdFixPrompt is the builder's prompt for correcting a HOLD: the
// canonical brief, then the gap as the task. It carries nothing else from the
// review — no output and no verdict contract — so the builder never sees
// the critic's bar or reasoning beyond the gap (design decision 7).
func holdFixPrompt(brief, gap string) string {
	return brief + "\n" +
		"## The gap to close\n\n" +
		"A review of this work against the brief above found this gap between the work and the brief. " +
		"Close it in this worktree; it is your task now:\n\n" +
		gap + "\n\n" +
		"Do not weaken or remove tests or gates to close it; make the real change the brief asks for.\n"
}

func isReview(p store.PhaseRecord) bool { return p.Name == "review" && p.Kind != "code" }

// payloadString reads a string key from p's recorded payload, "" when the
// payload or key is absent.
func payloadString(p store.PhaseRecord, key string) string {
	if len(p.Payload) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(p.Payload, &m); err != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// phaseOutputText extracts the human-readable output a phase's own recorded
// payload carries: GateResult.Output for a code gate, or the raw_output an
// agent phase's synthesized envelope carries. Both are already redacted at
// the source; this performs no redaction of its own.
func phaseOutputText(p store.PhaseRecord) string {
	if len(p.Payload) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(p.Payload, &m); err != nil {
		return ""
	}
	if out, ok := m["output"].(string); ok && out != "" {
		return out
	}
	if out, ok := m["raw_output"].(string); ok {
		return out
	}
	return ""
}
