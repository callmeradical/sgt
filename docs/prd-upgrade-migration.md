# Product Requirements: Migrate Pre-Rebrand v2 State on Upgrade

Status: Approved 2026-09-19 (grilled and resolved with the operator; see Decisions below and Quality bar).

Extends: none directly. Related to the rebrand commit `a955ebf`
("refactor: rename module and identifiers sergeant -> sgt"), which
renamed on-disk paths going forward but added no migration for
installations that predate it.

## Summary

Issue #15: a working pre-rebrand "v2" installation — project YAML
under `~/.config/sergeant/*.yaml`, its SQLite store at
`~/.local/share/sergeant/sergeant.db`, and isolated worktrees under
`~/.local/share/sergeant-v2/fleet` — silently goes dark when the
operator updates to today's rebranded `sgt`. Every path the binary
reads changed (`~/.config/sgt`, `~/.local/share/sgt/sgt.db`,
`~/.local/share/sgt-v2/fleet`), nothing migrates the old data, and the
dashboard starts up showing zero projects with no indication that
prior state exists anywhere. This PRD adds an explicit, one-time,
idempotent migration path (or an explicit refusal to start) so an
upgrade never silently presents an empty installation.

## Problem

Confirmed by reading the actual rename commit and today's code
(`internal/config/config.go:146-161`, `internal/config/list.go:10-18`,
`internal/store/store.go:218-238`, `internal/dag/engine.go:100-111`):

- `a955ebf` renamed `~/.config/sergeant` → `~/.config/sgt`,
  `~/.local/share/sergeant/sergeant.db` → `~/.local/share/sgt/sgt.db`,
  and `~/.local/share/sergeant-v2` → `~/.local/share/sgt-v2`, as a pure
  path/identifier rename — no schema change accompanied it. It
  deliberately left v1's actual path,
  `~/.local/share/sergeant/fleet`, untouched, because that directory
  belongs to the historical v1 fleet, not to pre-rebrand v2 — a
  meaning issue #15 also calls out ("never import v1 fleet records").
- There is currently zero migration/detection code anywhere in the
  repo for this (confirmed via repo-wide grep for "migrat", "rebrand",
  "sergeant" — the only hits are unrelated SQLite schema-evolution
  functions in `store.go`, and a doc comment in `engine.go`
  describing the v1/v2 fleet-layout incompatibility).
- Today's dashboard has no "empty installation" signal
  (`internal/ui/server.go`'s `handleProjects` just returns `[]`, and
  `index.html`'s `loadProjects()` silently renders nothing extra) — an
  operator with stranded pre-rebrand v2 state sees the same UI as a
  genuinely fresh install.
- Because the rename was schema-neutral, an old `sergeant.db` copied
  byte-for-byte to the new path and opened through today's
  `store.Open` will self-upgrade through the existing
  `migrate()`/`migrateAddTables()`/`migrateAddColumns()`/
  `migrateAddIndexes()` functions the same way any long-lived `sgt.db`
  already does — migration does not need to reimplement schema
  evolution, only get the file to the right place safely.

## Proposal

- A new migration step, run automatically by `run`/`status`/`ui`/`mcp`
  the moment pre-rebrand v2 state is detected (Decision 1), plus an
  explicit `sgt migrate` subcommand that runs the same logic on demand
  (Decision 7).
- Detection: pre-rebrand v2 state is recognized by the literal
  presence of `~/.config/sergeant/*.yaml` and/or
  `~/.local/share/sergeant/sergeant.db` and/or
  `~/.local/share/sergeant-v2/fleet` — never by anything under
  `~/.local/share/sergeant/fleet` (that's v1; explicitly excluded per
  issue #15's safety constraints).
- Config migration: copy each `~/.config/sergeant/*.yaml` /
  `*.yml` to `~/.config/sgt/` under the same filename, skipping (not
  overwriting) any file that already exists and differs at the
  destination (Decision 3, conflict policy).
- Store migration: never copy the live `sergeant.db` file directly
  (WAL mode means the on-disk `.db` file alone is not a consistent
  snapshot — the `-wal`/`-shm` files matter too). Use SQLite's
  `VACUUM INTO` (executed against the source db) to produce a single
  consistent snapshot file, then move that snapshot into place as
  `~/.local/share/sgt/sgt.db`. Opening it through the existing
  `store.Open` afterward exercises the existing schema-migration path
  with no new schema-handling code.
- Fleet migration: pre-rebrand v2 worktrees under
  `~/.local/share/sergeant-v2/fleet/<task>/<repo>/` are copied
  (Decision 4, `cp -a` semantics) to
  `~/.local/share/sgt-v2/fleet/<task>/<repo>/`.
- Verification before declaring success: after migration, re-read the
  destination — project count from `config.ListProjects()`, run/phase
  counts from the migrated store, and worktree git status from the
  fleet root — and compare against the source's own counts. Only mark
  the sentinel `verified` if they match; otherwise `failed` (Decision
  5), left in place for debugging, retried automatically next startup.
- Idempotency: re-running migration when the sentinel already says
  `verified` is a no-op (no re-scan, no re-copy, no re-run of
  `VACUUM INTO`); a `failed` sentinel triggers an automatic retry
  (Decision 6).
- Source state (`~/.config/sergeant`, `~/.local/share/sergeant`,
  `~/.local/share/sergeant-v2`) is never deleted or mutated by
  migration — copy-only, per issue #15's "preserve source state until
  migration is fully verified."
- Conflicts and migration outcomes are surfaced durably and visibly
  (dashboard banner/API field, not just a log line — Decision 3),
  since migration now runs unattended by default.

## Non-Goals

- Migrating v1 fleet state (`~/.local/share/sergeant/fleet`). Issue
  #15 explicitly excludes this; v1 and pre-rebrand v2 are different
  systems that happen to share a directory prefix.
- Any change to the current (post-rebrand) path layout, env var names,
  or schema. This PRD only adds a one-time bridge into that existing
  layout.
- A general-purpose backup/restore feature for `sgt.db`. The
  `VACUUM INTO` step exists to make migration safe, not to become a
  standing `sgt backup` command — that would be a separate PRD if
  wanted.
- Multi-hop migration (e.g. a hypothetical future rebrand). This PRD
  handles exactly the one known pre-rebrand-v2-to-current gap.

## Acceptance Criteria

- A fixture pre-rebrand v2 installation (config YAML + a `sergeant.db`
  built with the pre-rebrand schema/data + a fleet worktree under
  `sergeant-v2/fleet`) upgrades with its registry and durable run
  evidence intact, provable by reading the destination store/config
  after migration and comparing row-for-row against the fixture.
- Existing isolated worktrees remain discoverable after migration
  without changing their Git contents (same HEAD, same working tree
  diff, before and after).
- A partially populated destination (e.g. `~/.config/sgt/foo.yaml`
  already exists and differs from the source's `foo.yaml`) produces
  an actionable conflict report instead of a silent overwrite or a
  silent skip.
- Starting `sgt ui` (or `run`/`status`/`mcp`) can never silently show
  an empty installation while recognizable pre-rebrand v2 state exists
  on disk unmigrated — migration runs automatically, and its outcome
  (including any conflicts) is visible via the dashboard/API, not just
  a log line.
- Re-running migration against an already-migrated destination is a
  no-op with respect to record/worktree counts (idempotent), proven by
  running it twice against the same fixture and diffing the result.

## Decisions (grilled and resolved 2026-09-19)

1. **Trigger: automatic.** Migration runs the moment pre-rebrand v2
   state is detected — no separate operator invocation is required for
   the common case. (A manual re-run surface still exists — Decision
   7.)
2. **Scope of automatic triggering: every subcommand that resolves
   config/store paths** — `run`, `status`, `ui`, `mcp` — i.e.
   everything except `version` and `migrate` itself. `status` and `mcp`
   would otherwise show the same false "empty" picture `ui` does.
3. **Conflict policy: fail closed per-item.** If
   `~/.config/sgt/foo.yaml` already exists and differs from the
   source's `foo.yaml`, skip that one file/record, report it by name as
   an unresolved conflict, and continue migrating everything else that
   doesn't conflict. Because migration is automatic (Decision 1) rather
   than an explicit command whose stdout an operator is already
   watching, conflicts are surfaced somewhere durable and visible — a
   dashboard banner/API field, not only a log line — so an automatic,
   unattended migration run still gets noticed.
4. **Fleet worktree migration: copy** (via `cp -a` semantics,
   preserving git internals exactly), verified — only report success
   once the destination's git status matches the source's. Manual
   cleanup of the old fleet root is left to the operator, mentioned
   wherever the migration result surfaces (Decision 3's dashboard/log
   surface) — consistent with copy-only everywhere else in this PRD.
5. **Verification failure: leave the partially-written destination in
   place**, marked explicitly unverified (Decision 6's marker), with
   exactly what didn't match reported. No rollback — it would destroy
   the evidence needed to debug the mismatch, and the source is
   untouched regardless so nothing is lost either way.
6. **Idempotency/status marker: a sentinel file**,
   `~/.config/sgt/.migration-from-sergeant.json`, holding status
   (`verified` / `failed`), source paths, a timestamp, and — when
   failed — the specific mismatch details. On startup: no sentinel +
   pre-rebrand v2 state detected → migrate; sentinel `verified` → skip
   entirely, no re-scan; sentinel `failed` → automatically retry (since
   migration is automatic per Decision 1) and overwrite the sentinel
   with the new attempt's outcome. A failed migration is never silently
   stuck — every startup gets another attempt until it verifies.
7. **Command surface: `sgt migrate`, kept small.** A single `sgt
   migrate` subcommand runs the same automatic logic on demand (forcing
   an immediate retry rather than waiting for the next
   `run`/`status`/`ui`/`mcp` invocation) and prints the sentinel's
   resulting status, including any conflicts — no separate
   `migrate-status` command. This does not overlap with
   `docs/prd-cli-dispatch-subcommands.md`'s four endpoints.

## Quality bar

- A real fixture installation (real YAML files, a real `sergeant.db`
  built through the pre-rebrand schema, a real git worktree under a
  `sergeant-v2/fleet` path) is migrated and independently re-read from
  the destination — not just a mock of the migration function's own
  return value.
- Idempotency is proven by literally running migration twice against
  the same fixture and asserting identical destination state after
  each run (not asserted from code inspection).
- The v1-exclusion constraint is proven by a fixture that has *both*
  `~/.local/share/sergeant/fleet` (v1) and
  `~/.local/share/sergeant-v2/fleet` (pre-rebrand v2) present
  simultaneously, asserting only the latter is touched.
- The conflict-detection path is proven by a fixture with a genuinely
  conflicting destination file, asserting the specific file is
  reported and left unmodified, and that unrelated non-conflicting
  files still migrate, and that the conflict is visible through the
  dashboard/API surface (Decision 3), not just in a log.
- WAL-safety is proven by migrating a source db that has an active
  `-wal` file with uncommitted-to-main-file writes, asserting those
  writes are present at the destination (i.e. the migration didn't
  just copy the stale main `.db` file and silently drop recent writes).
- The failed→retry loop (Decisions 5 and 6) is proven end-to-end: force
  a verification failure, confirm the sentinel is written as `failed`
  with the destination left in place, then confirm the next automatic
  trigger retries and can reach `verified` once the underlying cause is
  fixed — not just unit-tested in isolation from the sentinel file.
- Automatic-trigger scope (Decision 2) is proven by exercising the
  detection/migration path through each of `run`, `status`, `ui`, and
  `mcp` — not asserted only through one entrypoint and assumed to hold
  for the others.
- Full existing suite (`go build ./...`, `go vet ./...`, `go test
  ./...`) stays green, on both macOS and the Linux container, exactly
  as held for every other change this session.
