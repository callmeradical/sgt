## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-04 - Added index to envelopes on run_id
**Learning:** Similar to the `phases.run_id` index, queries fetching or deleting `envelopes` by `run_id` (e.g. during retention cleanup or envelope retrieval) take significantly longer without an index on the `run_id` foreign key. Benchmarking shows a drop from ~85ms to ~20ms for 1000 queries on a 50k row table.
**Action:** Consistently index foreign keys like `run_id` across all tables that scale with the number of test runs (e.g., phases, envelopes, artifacts) to prevent N+1 and full table scan bottlenecks during reads and cascading deletes.
