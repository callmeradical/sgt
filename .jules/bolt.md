## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-17 - Added index to envelopes on run_id and bullets on intent_id
**Learning:** Found missing indexes on frequently queried foreign key columns (`envelopes.run_id` and `bullets.intent_id`). Adding these simple indexes prevents full table scans on queries resolving test phases or tracking intent completion across bullets. This fixes N+1 scaling issues.
**Action:** Verify all foreign keys used in WHERE clauses against runs or intents are fully indexed, since these tables scale linearly with agent activity.
