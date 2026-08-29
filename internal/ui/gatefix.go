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

	failure, ok := srv.lastFailedPhase(runID, "")
	if !ok {
		http.Error(w, fmt.Sprintf("run %s has no failed phase to correct", runID), http.StatusConflict)
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

	_ = srv.Store.UpdateRunStatus(run.ID, "running")

	ctx, cancel := context.WithCancel(context.Background())
	srv.registerRun(run.ID, cancel)

	go srv.runFixCycles(ctx, cancel, proj, run.ID, failure.Repo)

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
// If the re-attempted stage now passes, the run concludes passed. If it
// fails again and cycles remain under the configured bound, the loop repeats
// automatically. If the bound is exhausted, the run falls back to its
// existing failed/blocked outcome, with a reason that says the fix budget
// was exhausted rather than repeating the original gate-failure text as if
// no correction had been attempted.
func (srv *Server) runFixCycles(ctx context.Context, cancel context.CancelFunc, proj *config.Project, runID, repoName string) {
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

		failure, ok := srv.lastFailedPhase(runID, repoName)
		if !ok {
			break
		}

		pr := &runner.PhaseRunner{
			Store:    srv.Store,
			Router:   router,
			Worktree: worktree,
			RepoName: repoName,
			RunID:    runID,
			AgentCLI: proj.Defaults.Agent,
			Model:    proj.Defaults.Model,
			FixCycle: cycle,
		}

		if _, _, err := pr.RunAgentPhase(ctx, "fix", fixPrompt(failure), proj.ResolvedRetries(repoName)); err != nil {
			// The fix phase itself errored (not the gate it was meant to
			// address). Treat this as this cycle's failure and let the loop
			// continue to the next cycle rather than aborting the whole
			// corrective loop early.
			continue
		}

		repos := srv.reposForRun(runID)
		stages := resolveStages(proj, repos, run.Brief)

		engine := dag.NewEngine(proj, srv.Store, router)
		engine.Resume = true

		cycleFailed := false
		for i := range stages {
			if err := engine.RunStage(ctx, runID, &stages[i]); err != nil {
				cycleFailed = true
				break
			}
		}

		// Commit whatever the fix and re-run produced, pass or fail, for the
		// same reason executeRun commits before reporting a dispatch's own
		// failure: an uncommitted worktree is eligible for reclaim, and real
		// agent work must survive that even when this cycle did not resolve
		// the gate.
		for _, r := range repos {
			_, _, _ = dag.CommitRunOutput(context.Background(), runID, r, fmt.Sprintf("sgt: corrective fix cycle %d", cycle))
		}

		if !cycleFailed {
			if ctx.Err() != nil {
				srv.recordTerminalRun(runID, "cancelled")
				return
			}
			srv.recordTerminalRun(runID, "passed")
			return
		}
	}

	srv.recordTerminalRunWithReason(runID, "failed", fmt.Sprintf(
		"corrective fix budget exhausted (%d/%d attempts); gate still failing", limit, limit))
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
