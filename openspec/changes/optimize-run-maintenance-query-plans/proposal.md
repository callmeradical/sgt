# Proposal - Optimize run maintenance query plans

## Why

The startup reconciliation, fleet cleanup, and retention paths read unbounded
`runs` and `phases` tables. Their queries must not degrade into full scans or
unnecessary temporary B-tree sorts as history grows.

## What Changes

- Add a `(status, created_at)` index for startup reconciliation.
- Add an `(updated_at, status)` index and make fleet cleanup use it
  deterministically.
- Reuse the existing `(project, created_at DESC)` index for retention rotation;
  SQLite can scan it backward, so any duplicate ascending index is removed.
- Remove the unused global phase ordering from reconciliation's multi-run
  bulk fetch.
- Pin all four production SQL statements with `EXPLAIN QUERY PLAN` tests.

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
