# Design - Optimize run maintenance query plans

## Query and index ownership

Each maintenance query has one named SQL source shared by production and its
query-plan test. Static queries use constants; the dynamic phase `IN` query uses
one builder that accepts a run count and emits only bound placeholders.

- `ReconcileOrphanedRuns` filters one status and orders by `created_at`, served
  by `idx_runs_status_created_at(status, created_at ASC)`.
- Reconciliation's phase bulk fetch uses several `run_id` values. The
  `idx_phases_run_id_created_at` index serves each lookup but cannot produce one
  global `created_at` order across those values. Since phases are grouped by run
  before updates, the query omits the unused order and avoids a TEMP B-TREE.
- `RunsEligibleForCleanup` bounds and orders by `updated_at`; it uses
  `idx_runs_updated_at_status(updated_at ASC, status)`. Because SQLite prefers
  the status-leading reconciliation index on an empty, statistics-free store,
  the production query uses `INDEXED BY idx_runs_updated_at_status`.
- `RotateProject` filters by project and bounds/orders `created_at`. The existing
  `idx_runs_project_created_at(project, created_at DESC)` serves both directions
  because SQLite can scan an index backward.

## Migration

Both new indexes are created with `CREATE INDEX IF NOT EXISTS` during `Open`.
Reopening a legacy database twice must retain them, proving startup deployment is
idempotent. The migration also drops the redundant ascending project/created-at
index if an earlier revision created it.

## Verification

`TestHotListQueriesReadInIndexOrder` rejects a TEMP B-TREE and requires the exact
index name for each production query, including a two-run instance of the
dynamic phase fetch. A negative control appends the removed `ORDER BY
created_at ASC` to that same two-run SQL and requires the TEMP B-TREE to
reappear, proving the fixture reproduces the regression. Exact matching prevents
`idx_runs_project_created_at_asc` from falsely satisfying an assertion for
`idx_runs_project_created_at`.

## Rejected alternatives

**Add `(project, created_at ASC)`.** Rejected as duplicate write and storage cost;
the descending index already supports ascending traversal.

**Trust SQLite to choose `(updated_at, status)`.** Rejected because the
statistics-free startup plan chooses `(status, created_at)` and sorts separately.
