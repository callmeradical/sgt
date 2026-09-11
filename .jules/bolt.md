## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).

## 2026-09-11 - Adding comprehensive indexes for foreign keys
**Learning:** Found several missing indexes on frequently queried foreign key columns (`envelopes.run_id`, `deliveries.envelope_id`, `bullets.intent_id`, `artifacts.run_id`). Missing indexes on foreign keys cause significant O(N) penalties during SELECTs and cascading DELETE operations in SQLite.
**Action:** Always index foreign keys in related tables that scale linearly with root entities (like runs, intents, envelopes) to prevent full table scans.
