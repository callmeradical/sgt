package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// queryPlan returns the EXPLAIN QUERY PLAN detail lines SQLite chooses for
// query against st's real schema. Every placeholder is bound to a dummy value:
// the plan depends on the schema, not on the arguments.
func queryPlan(t *testing.T, st *Store, query string) []string {
	t.Helper()
	args := make([]any, strings.Count(query, "?"))
	for i := range args {
		args[i] = "x"
	}
	rows, err := st.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v\nquery: %s", err, query)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scanning plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading plan: %v", err)
	}
	return plan
}

// indexNames returns the names of every explicitly created index on table.
func indexNames(t *testing.T, st *Store, table string) map[string]bool {
	t.Helper()
	rows, err := st.db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL`, table,
	)
	if err != nil {
		t.Fatalf("listing indexes on %s: %v", table, err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning index name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading index names: %v", err)
	}
	return names
}

// The dashboard, the run-detail view and every delivery attempt read these
// lists on each request. With only a single-column index on the filter column,
// SQLite finds the rows but must then sort them in a TEMP B-TREE on every call
// (or, for deliveries, scan the whole table). Each query must instead read its
// rows straight out of an index already in the order the query asks for.
//
// The SQL is the package's own named constant for each method, so a change to
// a query's WHERE or ORDER BY that no longer matches its index fails here.
func TestHotListQueriesReadInIndexOrder(t *testing.T) {
	st, _ := openTestStore(t)

	cases := []struct {
		method string
		query  string
		index  string
	}{
		{"ListPhasesForRun", listPhasesForRunSQL, "idx_phases_run_id_created_at"},
		{"ListEnvelopesForRun", listEnvelopesForRunSQL, "idx_envelopes_run_id_created_at"},
		{"GetLatestEnvelope", latestEnvelopeSQL, "idx_envelopes_run_id_created_at"},
		{"ListBulletsForIntent", listBulletsForIntentSQL, "idx_bullets_intent_id_position_created_at_id"},
		{"ListArtifactsForRun", listArtifactsForRunSQL, "idx_artifacts_run_id_captured_at_id"},
		{"ListRecentRuns", listRecentRunsSQL, "idx_runs_created_at"},
		{"ListDeliveryHistory", listDeliveryHistorySQL, "idx_deliveries_idempotency_key_created_at_id"},
		{"latestDeliveryState", latestDeliveryStateSQL, "idx_deliveries_idempotency_key_created_at_id"},
		{"hasDeadLetterRow", countDeliveriesInStateSQL, "idx_deliveries_idempotency_key_created_at_id"},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			plan := queryPlan(t, st, c.query)
			joined := strings.Join(plan, " | ")
			if strings.Contains(joined, "TEMP B-TREE") {
				t.Errorf("%s sorts in a TEMP B-TREE instead of reading index order: %s", c.method, joined)
			}
			if !strings.Contains(joined, "INDEX "+c.index) {
				t.Errorf("%s does not use %s: %s", c.method, c.index, joined)
			}
		})
	}
}

// Every installation that predates the composite indexes has the old
// single-column ones. Opening such a database must leave it with the
// composites and without the indexes they replace — a leftover single-column
// index is pure write cost, since its composite serves every lookup it did —
// and its rows must still read back.
func TestOpenReplacesSingleColumnForeignKeyIndexesOnAnOlderDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-indexes.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	// Rewind the index set to what main shipped before this change.
	legacy := []string{
		`DROP INDEX IF EXISTS idx_phases_run_id_created_at`,
		`DROP INDEX IF EXISTS idx_envelopes_run_id_created_at`,
		`DROP INDEX IF EXISTS idx_bullets_intent_id_position_created_at_id`,
		`DROP INDEX IF EXISTS idx_artifacts_run_id_captured_at_id`,
		`DROP INDEX IF EXISTS idx_deliveries_idempotency_key_created_at_id`,
		`DROP INDEX IF EXISTS idx_runs_created_at`,
		`CREATE INDEX IF NOT EXISTS idx_phases_run_id ON phases(run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_envelopes_run_id ON envelopes(run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bullets_intent_id ON bullets(intent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_run_id ON artifacts(run_id)`,
	}
	for _, ddl := range legacy {
		if _, err := st.db.Exec(ddl); err != nil {
			t.Fatalf("simulating legacy indexes (%s): %v", ddl, err)
		}
	}
	if err := st.CreateRun(&RunRecord{ID: "old-run", Project: "p", TaskID: "t", Status: "passed"}); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	if err := st.RecordPhase(&PhaseRecord{ID: "old-phase", RunID: "old-run", Repo: "api", Name: "test", Kind: "code", Status: "passed"}); err != nil {
		t.Fatalf("seeding phase: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Open twice: the migration must be idempotent across reopens.
	for i := 0; i < 2; i++ {
		upgraded, err := Open(dbPath)
		if err != nil {
			t.Fatalf("reopen %d of older database failed: %v", i+1, err)
		}

		want := map[string]struct{ present, absent string }{
			"phases":    {"idx_phases_run_id_created_at", "idx_phases_run_id"},
			"envelopes": {"idx_envelopes_run_id_created_at", "idx_envelopes_run_id"},
			"bullets":   {"idx_bullets_intent_id_position_created_at_id", "idx_bullets_intent_id"},
			"artifacts": {"idx_artifacts_run_id_captured_at_id", "idx_artifacts_run_id"},
		}
		for table, w := range want {
			names := indexNames(t, upgraded, table)
			if !names[w.present] {
				t.Errorf("reopen %d: %s is missing %s; have %v", i+1, table, w.present, names)
			}
			if names[w.absent] {
				t.Errorf("reopen %d: %s still has the superseded %s", i+1, table, w.absent)
			}
		}
		deliveries := indexNames(t, upgraded, "deliveries")
		// envelope_id stays single-column: ListDeliveriesForRun orders across
		// several envelopes, so no envelope_id-leading composite can serve its
		// sort, and the plain index is what the join and retention delete use.
		for _, name := range []string{"idx_deliveries_idempotency_key_created_at_id", "idx_deliveries_envelope_id"} {
			if !deliveries[name] {
				t.Errorf("reopen %d: deliveries is missing %s; have %v", i+1, name, deliveries)
			}
		}
		if !indexNames(t, upgraded, "runs")["idx_runs_created_at"] {
			t.Errorf("reopen %d: runs is missing idx_runs_created_at", i+1)
		}

		phases, err := upgraded.ListPhasesForRun("old-run")
		if err != nil {
			t.Fatalf("reopen %d: listing phases after upgrade: %v", i+1, err)
		}
		if len(phases) != 1 || phases[0].ID != "old-phase" {
			t.Errorf("reopen %d: legacy phase not readable after upgrade: %+v", i+1, phases)
		}
		if err := upgraded.Close(); err != nil {
			t.Fatalf("reopen %d: close failed: %v", i+1, err)
		}
	}
}
