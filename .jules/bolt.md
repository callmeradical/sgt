## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).

## 2025-02-14 - SQLite Foreign Key Indexing
**Learning:** SQLite does not automatically index foreign keys. Missing indexes on frequently queried fields like `run_id`, `intent_id`, and `envelope_id` can lead to N+1 queries and full table scans during data reads and cascading deletes.
**Action:** Always index foreign keys that scale with the number of test runs across related tables to maintain query performance.
