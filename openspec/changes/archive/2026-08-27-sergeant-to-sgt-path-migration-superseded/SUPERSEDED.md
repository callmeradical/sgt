# Superseded

This change proposal was never implemented (no `tasks.md` was ever
written for it) and is archived here as a rejected draft, not a
completed change — `openspec/specs/` was never updated with its
`path-migration` capability, and must not be, since none of this
behavior exists in the codebase.

It is superseded by `docs/prd-upgrade-migration.md` (issue #15),
grilled and approved 2026-09-19, which covers the same underlying
problem with a materially different and safer design:

- **Copy, not rename/move.** This proposal moved the old paths
  in place, destroying the source immediately. Issue #15's safety
  constraints require preserving source state until migration is
  verified — the new PRD copies instead.
- **WAL-safe store migration.** This proposal implied a raw directory
  rename of `sergeant.db`, which would silently drop any writes still
  sitting in an uncommitted `-wal` file. The new PRD uses SQLite's
  `VACUUM INTO` to produce a consistent snapshot first.
- **Verification and idempotency.** This proposal had no
  post-migration verification step and no mechanism to make a retry
  safe. The new PRD adds both (a status sentinel file, and explicit
  acceptance criteria proving idempotent re-runs).

See `docs/prd-sergeant-to-sgt-path-migration.md` (also marked
superseded) for this proposal's original PRD.
