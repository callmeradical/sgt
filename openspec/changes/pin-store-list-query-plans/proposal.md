# Proposal - Pin store list query plans

## Why

Three frequently used project/status list queries already have suitable
composite indexes, but their plans are absent from the regression suite. A
future SQL edit could silently reintroduce a scan or temporary B-tree sort.

## What Changes

- Give the three production queries named SQL constants.
- Add them to `TestHotListQueriesReadInIndexOrder` with their existing indexes.
- Preserve the exported `ListIntentsByStatus` documentation while placing its
  query constant nearby.

## Capabilities

### New Capabilities

- `store-query-performance`: hot list-query plans are regression-tested against
  the indexes intended to serve them.

### Modified Capabilities

(none - query results and public interfaces are unchanged)

## Impact

- Affected code: `internal/store/store.go`, `internal/store/index_test.go`, and
  the corresponding `.jules/bolt.md` guidance.
- No PRD change: this adds regression coverage without changing product behavior.
