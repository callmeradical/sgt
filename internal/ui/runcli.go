package ui

// `sgt run <project>` (design decision 10). The CLI owns no stage loop and
// writes no status of its own: it runs the project's DAG through executeRun,
// the same execution path a dispatch uses, so a CLI run gets the gauntlet
// preflight and roles, committed agent output, the delivery record, and the
// one terminal status write — and answers with what that write stored.

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/naming"
	"github.com/callmeradical/sgt/internal/store"
)

// RunProjectCLI runs proj's DAG stages synchronously through executeRun and
// returns the run's stored terminal status and the reason recorded with it
// ("" for a status that carries none).
//
// probe replaces the server's GauntletProbe when non-nil; nil keeps the
// default `claude plugin list --json` probe.
//
// A project with no DAG stages, a refused gauntlet preflight, or a failure
// to record the run returns an error, and the first two write nothing. A run
// that executed and failed is not an error: it is reported by status.
func RunProjectCLI(ctx context.Context, st *store.Store, proj *config.Project, probe ProbeFunc) (status, reason string, err error) {
	if proj.DAG == nil || len(proj.DAG.Stages) == 0 {
		return "", "", fmt.Errorf("project %s has no dag stages; nothing to run (use sgt dispatch to run its repositories)", proj.Name)
	}

	srv := NewServer(st, 0)
	if probe != nil {
		srv.GauntletProbe = probe
	}

	roles, version, err := srv.gauntletPreflight(ctx, proj)
	if err != nil {
		return "", "", err
	}
	pf := preflightResult{Roles: roles, Version: version}

	// The same generator the dispatch handler uses, so a CLI run and a
	// dispatched run cannot collide on the runs primary key. A CLI run
	// carries no intent and no brief.
	taskID := naming.RunID()
	if err := st.CreateRun(&store.RunRecord{
		ID:      taskID,
		Project: proj.Name,
		TaskID:  taskID,
		Status:  "running",
	}); err != nil {
		return "", "", fmt.Errorf("recording the run for project %s: %w", proj.Name, err)
	}

	// dag.FleetRoot is the single authority for the fleet root (D7).
	router := handoff.NewRouter(filepath.Join(dag.FleetRoot(), taskID, "handoff"))
	engine := dag.NewEngine(proj, st, router)
	engine.GauntletRoles = pf.Roles
	engine.GauntletVersion = pf.Version

	var repos []string
	for _, stage := range proj.DAG.Stages {
		repos = append(repos, stage.Repos...)
	}

	runCtx, cancel := context.WithCancel(ctx)
	srv.registerRun(taskID, cancel)
	srv.awaitTerminalReason(taskID)
	srv.executeRun(runCtx, cancel, engine, proj, taskID, "", repos, "", pf)

	reason, _ = srv.takeTerminalReason(taskID)
	run, err := st.GetRun(taskID)
	if err != nil {
		return "", reason, fmt.Errorf("reading the outcome of run %s: %w", taskID, err)
	}
	return run.Status, reason, nil
}

// awaitTerminalReason marks runID as having a caller that will read the
// reason its terminal status is recorded with. Only awaited runs have their
// reason kept, so the long-lived dashboard server, which records every run's
// outcome through the same function and never reads the reasons back, holds
// nothing for them.
func (srv *Server) awaitTerminalReason(runID string) {
	srv.reasonsMu.Lock()
	defer srv.reasonsMu.Unlock()
	if srv.terminalReasons == nil {
		srv.terminalReasons = map[string]string{}
	}
	srv.terminalReasons[runID] = ""
}

// keepTerminalReason records reason for runID if a caller is awaiting it.
func (srv *Server) keepTerminalReason(runID, reason string) {
	srv.reasonsMu.Lock()
	defer srv.reasonsMu.Unlock()
	if _, awaited := srv.terminalReasons[runID]; awaited {
		srv.terminalReasons[runID] = reason
	}
}

// takeTerminalReason returns the reason recorded for an awaited run and
// stops holding it. ok is false when runID was not awaited.
func (srv *Server) takeTerminalReason(runID string) (reason string, ok bool) {
	srv.reasonsMu.Lock()
	defer srv.reasonsMu.Unlock()
	reason, ok = srv.terminalReasons[runID]
	delete(srv.terminalReasons, runID)
	return reason, ok
}
