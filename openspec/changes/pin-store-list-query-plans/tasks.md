# Tasks - Pin store list query plans

One repository, `sgt-v2`, so one task and no cross-repo merge order.

## Task 1 - Pin existing list queries to their indexes

Repository: `sgt-v2`. Depends on: nothing.

- Extract the three production SQL statements into package constants.
- Add each constant and intended index to the query-plan suite.
- Keep `ListIntentsByStatus`'s exported documentation attached to the method.

Verification: `go build ./... && go vet ./internal/... && go test ./internal/... -count=1`.
