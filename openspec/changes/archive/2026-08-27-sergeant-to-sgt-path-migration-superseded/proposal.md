## Why

The `sergeant` → `sgt` rename changed three hardcoded default
filesystem paths (config dir, database, fleet root) with no migration
path, and the database path has no env var override at all. An
operator upgrading an existing installation in place currently loses
visibility into all existing config and history silently — the new
binary just finds nothing, rather than erroring or migrating. This is
not hypothetical: a real daemon on this machine self-hosts five
projects (`smith`, `tycho`, `sergeant`, `sergeant-v2`, `sgt`) against
the old paths right now. PRD: `docs/prd-sergeant-to-sgt-path-migration.md`.

## What Changes

- Add a new `sgt migrate` subcommand that renames each of the three
  paths (config dir, database, fleet root) from its old `sergeant`-named
  location to its new `sgt`-named location, when the old path exists and
  the new one does not. Reports a conflict (does nothing) when both
  exist; reports "nothing to migrate" otherwise.
- **BREAKING (safety-motivated):** every other subcommand that opens
  the store or loads config (`ui`, `mcp`, `run`, `status`) SHALL refuse
  to start when it detects the old-named path present and the
  new-named path absent, instead of silently starting from an empty
  store/config. This is a deliberate behavior change: today those
  subcommands start "successfully" into a false-empty state.
- Add `SGT_DB_PATH` as a real env var override for the database path,
  matching the existing `SGT_CONFIG`/`SGT_FLEET_DIR` convention.

## Capabilities

### New Capabilities
- `path-migration`: `sgt migrate` detects and performs the one-time
  sergeant-to-sgt path rename for config dir, database, and fleet root,
  and every path-dependent subcommand refuses to silently start from a
  false-empty state when migration is needed but hasn't happened.

### Modified Capabilities
(none — no existing `openspec/specs/` capability governs startup path
resolution today; this introduces new scope)

## Impact

- **Affected code**: `cmd/sgt/main.go` (new subcommand, new refusal
  check on `ui`/`mcp`/`run`/`status`), `internal/config/config.go`
  (config dir resolution), `internal/dag/engine.go` (`FleetRoot`), and
  wherever the database path is currently hardcoded (`cmd/sgt/main.go`,
  four call sites per the current source) to add the `SGT_DB_PATH`
  override.
- **Out of scope**: data content/schema migration (none needed — this
  is purely a path rename); a generalized migration framework for
  hypothetical future renames; v1's own paths (untouched, D7); any
  multi-host scenario.
