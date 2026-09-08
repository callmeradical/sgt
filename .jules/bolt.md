## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2024-05-18 - Added index to envelopes, bullets, and deliveries on foreign keys
**Learning:** Found missing indexes on frequently queried foreign key columns (`envelopes.run_id`, `bullets.intent_id`, and `deliveries.envelope_id`). Just like `phases.run_id`, adding simple indexes dropped query time significantly (from ~180s to ~60s for list envelopes, and 12s to 4s for list bullets).
**Action:** Always verify foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases.
