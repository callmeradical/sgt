# Proposal — Migrate pre-rebrand v2 state on upgrade

## Repository

One repository: `sgt`. Standalone.

## Requirements served

`docs/prd-upgrade-migration.md` (full text, including its Decisions and
Quality bar sections — both binding). Closes issue #15. Supersedes
`openspec/changes/archive/2026-08-27-sergeant-to-sgt-path-migration-superseded/`
(never implemented; see that directory's `SUPERSEDED.md` for why its
design — rename in place, no WAL-safety, no verification — was
rejected in favor of this one).

## Problem

A pre-rebrand v2 installation — project YAML under
`~/.config/sergeant/*.yaml`, its SQLite store at
`~/.local/share/sergeant/sergeant.db`, and isolated worktrees under
`~/.local/share/sergeant-v2/fleet` — goes completely dark on upgrade.
The rename commit (`a955ebf`) changed every path the binary reads
(`~/.config/sgt`, `~/.local/share/sgt/sgt.db`,
`~/.local/share/sgt-v2/fleet`) with no migration and no "old state
detected" signal; the dashboard just starts empty. See the PRD's
Problem section for the full account, including why the schema-neutral
nature of the rename means no schema-handling code is needed — only
safely getting existing files to the new paths.

## Proposal

- New package `internal/upgrademigrate` implementing detection,
  migration, verification, and a status sentinel, per the PRD's
  Proposal and Decisions sections.
- Every subcommand that resolves config/store paths (`run`, `status`,
  `ui`, `mcp`) calls this package's entry point before doing anything
  else, automatically (Decision 1, Decision 2). `version` does not.
- A new `sgt migrate` subcommand calls the same entry point on demand
  and prints the resulting sentinel status, including any conflicts
  (Decision 7).
- Config migration: copy each `~/.config/sergeant/*.yaml`/`*.yml` to
  `~/.config/sgt/`, skipping (reporting, not overwriting) any
  destination file that already exists and differs (Decision 3).
- Store migration: `VACUUM INTO` the source `sergeant.db` to produce a
  consistent snapshot (WAL-safe — captures anything only in the
  `-wal` file), then place that snapshot at
  `~/.local/share/sgt/sgt.db` if and only if no file already exists
  there; if one does, the entire store migration is reported as one
  conflicting item and skipped — no row-level merge of two independent
  run histories is attempted (that is a different, unscoped problem;
  see Non-Goals).
- Fleet migration: copy (not move — Decision 4) each
  `~/.local/share/sergeant-v2/fleet/<task>/<repo>/` worktree to
  `~/.local/share/sgt-v2/fleet/<task>/<repo>/`, verified by comparing
  git status/HEAD before and after; a destination worktree that already
  exists at that path is reported as a conflict for that one
  `<task>/<repo>` item and skipped, same per-item policy as config.
- v1's actual fleet (`~/.local/share/sergeant/fleet`) is never read,
  copied, or referenced by any of the above — detection explicitly
  distinguishes it from `~/.local/share/sergeant-v2/fleet`.
- Verification: after copying, re-count/re-read the destination
  (`config.ListProjects()`, the migrated store's run/phase counts, each
  copied worktree's git status) against the source, writing a
  `verified` or `failed` sentinel (Decision 5, Decision 6) at
  `~/.config/sgt/.migration-from-sergeant.json`. `failed` leaves the
  partially-written destination in place and is retried automatically
  on the next triggering subcommand invocation.
- Migration outcome (status, conflicts, mismatches) is exposed through
  `internal/ui`'s API (a new field or endpoint) and surfaced in the
  dashboard, not just left in the sentinel file or process stdout
  (Decision 3's "durable and visible" requirement).

## Non-Goals

- Row-level merging of two independent SQLite run histories when a
  destination `sgt.db` already exists. Reported as a single conflict
  item and left to the operator.
- Migrating v1 fleet state (`~/.local/share/sergeant/fleet`).
- Any change to today's (post-rebrand) default paths, env vars, or
  schema.
- A general-purpose `sgt backup`/restore command. `VACUUM INTO` is used
  internally to make store migration WAL-safe; this proposal does not
  expose it as a standing feature.
