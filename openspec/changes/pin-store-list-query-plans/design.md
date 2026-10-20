# Design - Pin store list query plans

## Shared production SQL

`ListRunsForProject`, `ListIntentsForProject`, and `ListIntentsByStatus` each use
a package-level SQL constant. The query-plan test consumes the same constant, so
the assertion cannot drift from production SQL.

The constants are paired with the existing indexes:

- `idx_runs_project_created_at`
- `idx_intents_project_created_at_id`
- `idx_intents_status_created_at_id`

## Documentation placement

The `listIntentsByStatusSQL` constant sits above the exported method's comment.
This keeps Go documentation attached to `ListIntentsByStatus`, including the D2
rationale for representing an approval-pending plan as a proposed intent.

## Verification

`TestHotListQueriesReadInIndexOrder` must show the intended index and no temporary
B-tree for all three queries. `go doc ./internal/store Store.ListIntentsByStatus`
must retain the method's explanatory comment.
