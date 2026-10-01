## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).

## 2026-09-16 - Add missing indexes for critical foreign keys
**Learning:** Found multiple foreign key columns without indices (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Without these, any SELECT or DELETE operating via a foreign key (like cascading deletes during retention rotation, or listing bullets for an intent) required a full table scan.
**Action:** When adding tables with foreign keys that scale with the number of test runs or intent workloads, always ensure indices are created in `migrateAddIndexes()` to maintain performance and avoid N+1/table scan issues.

## 2024-05-20 - SQLite Composite Indexes for Filtering and Sorting
**Learning:** SQLite requires specific composite indexes to avoid temp B-trees and full table scans when a query filters by one column (e.g., `WHERE project = ?`) and sorts by another (e.g., `ORDER BY created_at DESC`). Simple single-column indexes on the WHERE clause are not enough to prevent the temporary B-tree sorting penalty on large core tables like `runs` and `intents`.
**Action:** Always create composite indexes mapping exactly to the `(filter_column, sort_column [direction])` for frequently executed, paginated, or ordered reads in SQLite stores.

## 2026-10-01 - Update SQLite foreign key indexes to cover ORDER BY clauses
**Learning:** Found that our single-column foreign key indexes (`idx_phases_run_id`, etc.) were causing SQLite to use temporary B-trees in memory for sorting during List queries that feature an `ORDER BY` clause. Changing these to composite indexes that match both the filter condition and the exact sorting direction (e.g., `idx_phases_run_id_created_at ON phases(run_id, created_at ASC)`) eliminates the `USE TEMP B-TREE FOR ORDER BY` step in the query execution plan, allowing results to stream directly from the index.
**Action:** When creating indexes for collections retrieved via a foreign key, always check if they are being queried with an `ORDER BY` clause, and if so, create a composite index that covers both the foreign key and the sort field(s) in the proper direction.
