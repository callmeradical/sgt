## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-16 - Add missing indexes for critical foreign keys
**Learning:** Found multiple foreign key columns without indices (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Without these, any SELECT or DELETE operating via a foreign key (like cascading deletes during retention rotation, or listing bullets for an intent) required a full table scan.
**Action:** When adding tables with foreign keys that scale with the number of test runs or intent workloads, always ensure indices are created in `migrateAddIndexes()` to maintain performance and avoid N+1/table scan issues.
## 2026-10-23 - Added index to runs table to speed up ListRunsForProject
**Learning:** Found a missing index for `project` and `created_at` on the `runs` table, which is heavily queried by `ListRunsForProject`. Since the query uses `WHERE project = ? ORDER BY created_at DESC`, a composite index avoids full table scans and memory sorting. Benchmarks showed query times dropped from ~422ms to ~15ms (for 100 queries).
**Action:** When performing `WHERE column_a = ? ORDER BY column_b DESC` lookups, always consider a composite index `(column_a, column_b)` to optimize read paths.
