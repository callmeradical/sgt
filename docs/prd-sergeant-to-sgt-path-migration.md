# Product Requirements: Sergeant-to-sgt Path Migration

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md` (the rename itself, commits `a955ebf`/
`9a220ca`); AGENTS.md's Truthfulness section ("must not display
anything it cannot derive from stored state" — extended here to
"must not silently start from state that isn't there").

## Summary

The `sergeant` → `sgt` rename changed three hardcoded default
filesystem paths with no migration and, for one of them, no override
at all. An operator upgrading a running installation in place currently
loses visibility into all existing config and history **silently** —
the new binary just sees nothing, rather than erroring or migrating.
This PRD defines what `sgt` does about that.

## Problem

Confirmed directly in `~/Dev/sgt`'s current source:

| Path | Old default | New default | Env override |
|---|---|---|---|
| Config dir | `~/.config/sergeant` | `~/.config/sgt` | `SGT_CONFIG` |
| Database | `~/.local/share/sergeant/sergeant.db` | `~/.local/share/sgt/sgt.db` | **none** |
| Fleet root | `~/.local/share/sergeant-v2/fleet` | `~/.local/share/sgt-v2/fleet` | `SGT_FLEET_DIR` |

This is not hypothetical: a real, currently-running daemon on this
machine self-hosts five projects (`smith`, `tycho`, `sergeant`,
`sergeant-v2`, and our own `sgt`) against the old paths. Rebuilding and
relaunching the `sgt` binary as-is would start it with zero visibility
into any of them — not because they're gone, but because the new
binary looks in different default directories, and the database path
specifically has no env var escape hatch at all.

## Proposal

- **New subcommand `sgt migrate`.** For each of the three paths: if the
  new path is absent and the corresponding old path is present, rename
  (not copy) old → new and report exactly what moved. If both exist,
  report the conflict and change nothing — an operator resolves
  ambiguity by hand, this command never guesses which copy is
  authoritative. If neither or only the new path exists, report
  "nothing to migrate."
- **Every other subcommand that touches these paths (`ui`, `mcp`,
  `run`, `status`) SHALL refuse to start, with a clear and actionable
  error, when it detects the old-named path exists but the new-named
  path does not.** The error names the exact old path found and
  instructs the operator to run `sgt migrate`. This is the load-bearing
  requirement: silently starting from an empty store or empty config
  is exactly the failure this PRD exists to prevent, and it must be
  impossible to hit by accident, not just documented against.
- **Add `SGT_DB_PATH`** (naming to match `SGT_CONFIG`/`SGT_FLEET_DIR`'s
  existing convention) as a real env var override for the database
  path — closing the one gap where config dir and fleet root are
  already overridable but the database is not. Needed both as a manual
  escape hatch and to make `sgt migrate` itself testable without
  touching a real `$HOME`.

## Out of scope

- **Migrating data content.** This is purely a path rename; there is no
  schema change and nothing about the data itself needs transformation.
- **A generalized migration framework** for hypothetical future
  renames. This PRD is specifically about the one sergeant→sgt
  transition, not a reusable migration system.
- **v1's own paths** (`~/.local/share/sergeant/fleet`, the bash
  toolbelt's storage). v1 is not a dependency (D7) and stays untouched
  regardless of this change.
- **Multi-host or shared-filesystem scenarios.** This PRD assumes a
  single machine's `$HOME`, consistent with every other path-resolution
  decision already made in this codebase.

## Open questions

- Should `sgt migrate` require an explicit `--yes`/confirmation prompt
  before renaming directories, given it's a one-time, somewhat
  destructive-if-wrong operation (though always a rename, never a
  delete)?
- Does `sgt migrate` need a `--dry-run` mode mirroring
  `/api/clean-worktrees`'s existing dry-run pattern, so an operator can
  preview what would move before committing to it?
