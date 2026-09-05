## 2026-09-03 - Added index to phases on run_id
**Learning:** Found a missing index on a frequently queried foreign key column (`phases.run_id`). Adding a simple index drops query time significantly (from ~140ms to ~12ms for a loop of 100 iterations on a table with 10k rows).
**Action:** Always check foreign key queries against tables to ensure they have indexes, especially on ones likely to grow linearly with the number of test runs or phases (like `phases` linked to `runs`).
## 2026-09-05 - Added missing database indexes on foreign keys
**Learning:** SQLite database tables (`envelopes`, `deliveries`, `artifacts`, `bullets`) that link to core concepts like `runs` and `intents` lacked indexes on their foreign key columns. This creates a significant bottleneck for cascading deletes or lookups scaling linearly with test runs.
**Action:** Always check foreign key queries against scaling tables to ensure they have indexes. Added missing indexes on `run_id`, `envelope_id`, and `intent_id` foreign keys.
