## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-16 - Add missing indexes for critical foreign keys
**Learning:** Found multiple foreign key columns without indices (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Without these, any SELECT or DELETE operating via a foreign key (like cascading deletes during retention rotation, or listing bullets for an intent) required a full table scan.
**Action:** When adding tables with foreign keys that scale with the number of test runs or intent workloads, always ensure indices are created in `migrateAddIndexes()` to maintain performance and avoid N+1/table scan issues.
## 2024-06-25 - Missing composite indexes cause N+1 query scan in large core tables
**Learning:** Large core tables like `runs` and `intents` require careful indexing on columns frequently used for filtering or ordering (e.g., `project`, `status`, `created_at`) to avoid expensive full table scans. A query filtering by one column and sorting by another (e.g. `WHERE project = ? ORDER BY created_at DESC`) without a composite index takes ~2s for 100k rows due to memory sort/scan vs ~3ms with a composite index.
**Action:** When adding new queries to store tables that filter by one column and order by another, always add a composite index spanning both columns in the order they are used.
