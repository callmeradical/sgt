# Proposal - Optimize run maintenance query plans

## Why

The startup reconciliation, fleet cleanup, and retention paths read an
unbounded `runs` table. Their ordered queries must not degrade into full scans
or temporary B-tree sorts as run history grows.

## What Changes

- Add a `(status, created_at)` index for startup reconciliation.
- Add an `(updated_at, status)` index and make fleet cleanup use it
  deterministically.
- Reuse the existing `(project, created_at DESC)` index for retention rotation;
  SQLite can scan it backward, so any duplicate ascending index is removed.
- Pin all three production SQL statements with `EXPLAIN QUERY PLAN` tests.

## Capabilities

### New Capabilities

- `store-query-performance`: run-maintenance queries read in index order and
  their plans are regression-tested.

### Modified Capabilities

(none - method results, API shapes, and retention policy are unchanged)

## Impact

- Affected code: `internal/store/store.go`, `internal/store/reconcile.go`,
  `internal/store/retention.go`, and `internal/store/index_test.go`.
- No PRD change: this changes query planning, not product behavior or policy.
