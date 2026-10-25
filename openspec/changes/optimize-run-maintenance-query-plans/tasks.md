# Tasks - Optimize run maintenance query plans

One repository, `sgt-v2`, so one task and no cross-repo merge order.

## Task 1 - Pin run-maintenance queries to useful indexes

Repository: `sgt-v2`. Depends on: nothing.

- Name the four production SQL sources used by run reconciliation, phase
  reconciliation, cleanup, and rotation.
- Add only the status/created-at and updated-at/status indexes.
- Remove the redundant ascending project/created-at index if present.
- Make cleanup deterministically use its ordering index.
- Remove phase reconciliation's unused cross-run ordering.
- Add exact-name query-plan tests rejecting TEMP B-TREE sorts.
- Prove both new indexes are installed idempotently on an older database.

Verification: `go build ./... && go vet ./internal/... && go test ./internal/... -count=1`.
