package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/handoff"
)

func (srv *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Actually stop the work, then record it. Previously this only wrote the status
	// column, which the still-running dispatch goroutine later overwrote with
	// "passed" while its agents kept writing to disk.
	stopped := srv.cancelRun(req.ID)

	// recordTerminalRun, not a bare status write: it also advances any bullet
	// left "blocked" by an earlier attempt back to "pending" (issue #20). A
	// run this process is not actively driving (stopped == false) has no
	// live goroutine left to ever reach that reconciliation on its own, so
	// the handler must not depend on one existing.
	if err := srv.recordTerminalRun(req.ID, "cancelled"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "cancelled",
		"id":          req.ID,
		"was_running": stopped,
		"note":        cancelNote(stopped),
	})
}

func (srv *Server) handleRunDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := srv.Store.DeleteRun(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": req.ID})
}

// handleRunResume re-enters an existing run instead of starting a new one.
//
// A run that dies leaves its worktree, its branch and its commits on disk, and
// before this there was no way to pick any of it up — the work was orphaned and
// the only recovery was a human merging the branch by hand. Run sgt-1787427981
// was killed at the former default agent timeout having already committed a
// change whose build and tests passed.
//
// Resume reuses the run id, so it reuses the worktree and branch (prepareWorktree
// returns an existing worktree untouched and no longer resets the branch), and
// skips phases that already hold a passed record. The run record is reused rather
// than copied: a second row would split one piece of work across two runs and two
// branches.
func (srv *Server) handleRunResume(w http.ResponseWriter, r *http.Request) {
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

	run, err := srv.Store.GetRun(strings.TrimSpace(req.ID))
	if err != nil || run == nil {
		http.Error(w, fmt.Sprintf("no run %q", req.ID), http.StatusNotFound)
		return
	}

	if !isResumable(run.Status) {
		http.Error(w, fmt.Sprintf(
			"run %s is %s and cannot be resumed; resumable statuses are %s",
			run.ID, run.Status, strings.Join(ResumableStatuses, ", ")),
			http.StatusConflict)
		return
	}

	// Refuse if this process is already driving the run. The status check above is
	// not sufficient on its own: a run registered as in-flight may not have written
	// its status yet.
	if srv.isRunActive(run.ID) {
		http.Error(w, fmt.Sprintf("run %s is already executing", run.ID), http.StatusConflict)
		return
	}

	proj, err := config.LoadProject(run.Project)
	if err != nil {
		http.Error(w, fmt.Sprintf("loading project %s: %v", run.Project, err), http.StatusBadRequest)
		return
	}

	// Resume runs the same body as a dispatch. The repository list is recovered
	// from the phase records where possible, so a resume targets what the original
	// run targeted rather than re-deriving it from configuration that may have
	// changed since.
	repos := srv.reposForRun(run.ID)

	// Clear a stale "blocked" disposition on the bullets this resume targets
	// before anything else, synchronously, in this handler — never inside
	// the goroutine below. RenderIntentBrief (internal/store/brief.go) reads
	// a bullet's live Status/BlockedReason directly, so the first prompt a
	// resumed phase renders would otherwise tell the agent it is still
	// blocked for a reason from the attempt that just ended, contradicting
	// the fresh attempt actively in flight (issue #20). A bullet that is not
	// "blocked" (e.g. already "pending", or a repo this resume does not
	// target) is left untouched — this is a targeted reconciliation, not the
	// whole-intent sweep recordTerminalRun does for a run's own outcome.
	if run.IntentID != "" {
		targeted := make(map[string]bool, len(repos))
		for _, r := range repos {
			targeted[r] = true
		}
		if bullets, berr := srv.Store.ListBulletsForIntent(run.IntentID); berr == nil {
			for _, b := range bullets {
				if b.Status == "blocked" && targeted[b.Repo] {
					if aerr := srv.Store.AdvanceBulletStatus(b.ID, "pending", ""); aerr != nil {
						log.Printf("sgt: resume: clearing blocked state for bullet %q: %v", b.ID, aerr)
					}
				}
			}
		}
	}

	router := handoff.NewRouter(filepath.Join(dag.FleetRoot(), run.ID, "handoff"))
	engine := dag.NewEngine(proj, srv.Store, router)
	engine.Resume = true

	_ = srv.Store.UpdateRunStatus(run.ID, "running")

	ctx, cancel := context.WithCancel(context.Background())
	srv.registerRun(run.ID, cancel)
	// Resume does not carry the change dir: the worktree (and its seeded
	// plan.json) already exists from the original dispatch. Pass empty so
	// SeedPlan is not re-run on resume, which would overwrite agent progress.
	go srv.executeRun(ctx, cancel, engine, proj, run.ID, run.Brief, repos, "")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "resumed",
		"task_id": run.ID,
		"run_id":  run.ID,
		"project": proj.Name,
		"skipped": srv.passedPhaseNames(run.ID),
	})
}
