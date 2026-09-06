## 2026-09-06 - Missing Indexes on Foreign Keys in SQLite
**Learning:** The initial SQLite schema included foreign keys for tables like `envelopes`, `artifacts`, `deliveries`, and `bullets`, but omitted the corresponding indexes on those columns (`run_id`, `envelope_id`, `intent_id`). This would cause full table scans during both lookups and cascading deletes as data scales.
**Action:** Always verify that every foreign key and frequently queried column in a new or updated table has a corresponding index. Add missing ones to `migrateAddIndexes` idempotently (`CREATE INDEX IF NOT EXISTS`).
