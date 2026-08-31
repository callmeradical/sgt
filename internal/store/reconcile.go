package store

import "strings"

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

	// We build a list of run IDs to load all their running phases in one go,
	// avoiding N+1 queries. We batch the query because SQLite limits variables.
	var orphanIDs []interface{}
	for _, run := range orphans {
		orphanIDs = append(orphanIDs, run.ID)
	}

	// Move the runs themselves.
	for _, run := range orphans {
		if err := s.reconcileRun(run.ID); err != nil {
			return result, err
		}
		result.RunsReconciled++
	}

	chunkSize := 500
	for i := 0; i < len(orphanIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(orphanIDs) {
			end = len(orphanIDs)
		}

		chunk := orphanIDs[i:end]
		placeholders := make([]string, len(chunk))
		for j := range placeholders {
			placeholders[j] = "?"
		}

		query := "SELECT id, run_id FROM phases WHERE status = 'running' AND run_id IN (" + strings.Join(placeholders, ",") + ") ORDER BY created_at ASC"
		phRows, err := s.db.Query(query, chunk...)
		if err != nil {
			return result, err
		}

		type phaseUpdate struct {
			ID    string
			RunID string
		}
		var phasesToUpdate []phaseUpdate
		for phRows.Next() {
			var pu phaseUpdate
			if err := phRows.Scan(&pu.ID, &pu.RunID); err != nil {
				phRows.Close()
				return result, err
			}
			phasesToUpdate = append(phasesToUpdate, pu)
		}
		phRows.Close()
		if err := phRows.Err(); err != nil {
			return result, err
		}

		for _, pu := range phasesToUpdate {
			if _, err := s.db.Exec(
				`UPDATE phases SET status = 'interrupted' WHERE id = ? AND status = 'running'`,
				pu.ID,
			); err != nil {
				return result, err
			}
			if err := s.recordTransition(ChannelPhase, pu.ID, map[string]interface{}{
				"id":         pu.ID,
				"run_id":     pu.RunID,
				"status":     "interrupted",
				"reconciled": true,
			}); err != nil {
				return result, err
			}
			result.PhasesReconciled++
		}
	}

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