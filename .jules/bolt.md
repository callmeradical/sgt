## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-16 - Add missing indexes for critical foreign keys
**Learning:** Found multiple foreign key columns without indices (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Without these, any SELECT or DELETE operating via a foreign key (like cascading deletes during retention rotation, or listing bullets for an intent) required a full table scan.
**Action:** When adding tables with foreign keys that scale with the number of test runs or intent workloads, always ensure indices are created in `migrateAddIndexes()` to maintain performance and avoid N+1/table scan issues.
## 2024-05-20 - SQLite Composite Indexes for Filtering and Sorting
**Learning:** SQLite requires specific composite indexes to avoid temp B-trees and full table scans when a query filters by one column (e.g., `WHERE project = ?`) and sorts by another (e.g., `ORDER BY created_at DESC`). Simple single-column indexes on the WHERE clause are not enough to prevent the temporary B-tree sorting penalty on large core tables like `runs` and `intents`.
**Action:** Always create composite indexes mapping exactly to the `(filter_column, sort_column [direction])` for frequently executed, paginated, or ordered reads in SQLite stores.
## 2025-10-04 - Eliminate SQLite TEMP B-TREE Sort Penalties
**Learning:** In SQLite, queries that filter on a foreign key and then immediately sort (e.g. `ORDER BY created_at ASC`) will incur a "TEMP B-TREE" in-memory sort penalty if the index is only on the foreign key.
**Action:** Always create composite covering indexes that include both the filtered column and the sorted column(s) in the exact order and direction of the sort (e.g. `CREATE INDEX idx_table_fk_sorted ON table(fk, sort_col ASC)`). When updating existing schemas, use `DROP INDEX IF EXISTS` for old indexes before creating the new ones to avoid database bloat.
