## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-16 - Add missing indexes for critical foreign keys
**Learning:** Found multiple foreign key columns without indices (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Without these, any SELECT or DELETE operating via a foreign key (like cascading deletes during retention rotation, or listing bullets for an intent) required a full table scan.
**Action:** When adding tables with foreign keys that scale with the number of test runs or intent workloads, always ensure indices are created in `migrateAddIndexes()` to maintain performance and avoid N+1/table scan issues.
## 2026-09-17 - Add composite indexes for common sorting/filtering on core tables
**Learning:** For frequently queried core tables (`runs`, `intents`), basic indexes on individual columns or foreign keys aren't enough when queries commonly filter by one column and sort by another (e.g., `WHERE project = ? ORDER BY created_at DESC`). This requires composite indexes to prevent expensive sort operations or full table scans in SQLite.
**Action:** Always consider the full query pattern (WHERE + ORDER BY) when adding indexes to core tables that grow linearly with app usage, and use composite indexes (like `project, created_at`) for these paths.
