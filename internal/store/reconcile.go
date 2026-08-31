package store

// ReconcileResult reports what ReconcileOrphanedRuns changed.
//
// It is a value type rather than a pair of ints so callers can check individual
// fields without coupling to the order of return values, and so the struct can
// gain new fields (e.g. a list of run IDs) without changing every call site.
type ReconcileResult struct {
	// RunsReconciled is the number of runs moved from running to interrupted.
	RunsReconciled int
	// PhasesReconciled is the number of phases moved from running to interrupted
	// across all reconciled runs.
	PhasesReconciled int
}

// ReconcileOrphanedRuns moves every run currently marked running to interrupted
// and reconciles their running phases in the same call.
//
// A freshly started coordinator is driving no runs. Its in-flight registry is
// empty by construction, so any run the store reports as running is, from this
// process's view, unowned. That is the whole inference, and it is sound precisely
// because it is made at startup and nowhere else.
//
// Rules:
//   - Only running runs are touched; all terminal statuses (passed, failed,
//     cancelled, timed_out, interrupted) are left exactly as they are.
//   - For each reconciled run, every phase whose status is running is moved to
//     interrupted. Phases in any other status are untouched.
//   - A change-sequence entry is appended for each run and each phase that
//     transitions, so an operator can see what was recovered.
//   - When nothing is reconciled, no change is appended and no log line is
//     written; a permanent "0 runs recovered" line at every start trains
//     operators to ignore the log.
//
// Reconciliation MUST NOT be called mid-life. Applied to a running coordinator
// it would reconcile a live run out from under itself. The server's Start method
// calls this once, before the listener accepts connections, and nowhere else.
func (s *Store) ReconcileOrphanedRuns() (ReconcileResult, error) {
	// Load every run that is still marked running. The query is deliberately
	// narrow: terminal statuses are not read, so no terminal run can be
	// accidentally touched even if the caller ignores the result.
	rows, err := s.db.Query(
		`SELECT `+runColumns+` FROM runs WHERE status = 'running' ORDER BY created_at ASC`,
	)
	if err != nil {
		return ReconcileResult{}, err
	}
	var orphans []RunRecord
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return ReconcileResult{}, err
		}
		orphans = append(orphans, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReconcileResult{}, err
	}

	if len(orphans) == 0 {
		// Nothing to do. Return immediately without touching the change sequence.
		return ReconcileResult{}, nil
	}

	var result ReconcileResult
	var orphanIDs []string

	for _, run := range orphans {
		orphanIDs = append(orphanIDs, run.ID)
		// Move the run itself.
		if err := s.reconcileRun(run.ID); err != nil {
			return result, err
		}
		result.RunsReconciled++
	}

	// Move all running phases that belong to these runs in a single batch.
	n, err := s.reconcileRunningPhases(orphanIDs)
	if err != nil {
		return result, err
	}
	result.PhasesReconciled += n

	return result, nil
}

// reconcileRun moves one run from running to interrupted and appends a change.
func (s *Store) reconcileRun(runID string) error {
	_, err := s.db.Exec(
		`UPDATE runs SET status = 'interrupted', updated_at = datetime('now') WHERE id = ? AND status = 'running'`,
		runID,
	)
	if err != nil {
		return err
	}
	// Record: nothing judged this run. The coordinator stopped; record the
	// interruption, not a verdict. "reconciled" in the payload tells an operator
	// reading the change log what caused the transition.
	return s.recordTransition(ChannelRun, runID, map[string]interface{}{
		"transition": "status",
		"id":         runID,
		"status":     "interrupted",
		"terminal":   true,
		"reconciled": true,
	})
}

// reconcileRunningPhases moves every running phase of the given runIDs to interrupted,
// appends a change for each, and returns how many it moved.
//
// A phase stuck at running is neither passed nor re-run on resume — resume skips
// only phases holding a passed record. Leaving it would silently drop work.
func (s *Store) reconcileRunningPhases(runIDs []string) (int, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}

	type phaseToReconcile struct {
		id    string
		runID string
	}

	var phases []phaseToReconcile
	var totalReconciled int

	// Process runIDs in chunks to avoid SQLite bind parameter limits
	chunkSize := 500
	for i := 0; i < len(runIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(runIDs) {
			end = len(runIDs)
		}
		chunk := runIDs[i:end]

		// Build the IN clause with placeholders
		placeholders := ""
		args := make([]interface{}, len(chunk))
		for j, id := range chunk {
			if j > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args[j] = id
		}

		// Read the running phases before updating, so we can append individual
		// change records with their IDs. A bulk UPDATE with no read-back would
		// prevent per-phase change records, and a client following the sequence
		// would not know which phase moved.
		query := `SELECT id, run_id FROM phases WHERE status = 'running' AND run_id IN (` + placeholders + `) ORDER BY created_at ASC`
		phRows, err := s.db.Query(query, args...)
		if err != nil {
			return totalReconciled, err
		}

		for phRows.Next() {
			var p phaseToReconcile
			if err := phRows.Scan(&p.id, &p.runID); err != nil {
				phRows.Close()
				return totalReconciled, err
			}
			phases = append(phases, p)
		}
		phRows.Close()
		if err := phRows.Err(); err != nil {
			return totalReconciled, err
		}
	}

	for _, p := range phases {
		if _, err := s.db.Exec(
			`UPDATE phases SET status = 'interrupted' WHERE id = ? AND status = 'running'`,
			p.id,
		); err != nil {
			return totalReconciled, err
		}
		if err := s.recordTransition(ChannelPhase, p.id, map[string]interface{}{
			"id":         p.id,
			"run_id":     p.runID,
			"status":     "interrupted",
			"reconciled": true,
		}); err != nil {
			return totalReconciled, err
		}
		totalReconciled++
	}

	return totalReconciled, nil
}
