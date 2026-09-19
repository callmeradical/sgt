# Upgrade migration of pre-rebrand v2 state

## ADDED Requirements

### Requirement: An upgrade never silently presents an empty installation when pre-rebrand v2 state exists

`sgt run`, `sgt status`, `sgt ui`, and `sgt mcp` SHALL detect
recognizable pre-rebrand v2 state (config YAML under
`~/.config/sergeant`, a database at
`~/.local/share/sergeant/sergeant.db`, or worktrees under
`~/.local/share/sergeant-v2/fleet`) and automatically attempt migration
before doing anything else, rather than starting from an apparently
empty config/store.

#### Scenario: Starting any path-resolving subcommand with pre-rebrand v2 state present triggers migration

- **WHEN** `sgt run`, `sgt status`, `sgt ui`, or `sgt mcp` is started
  and no prior "verified" migration sentinel exists, but pre-rebrand v2
  state is present on disk
- **THEN** migration runs automatically before the subcommand's own
  logic, and a status sentinel is written recording the outcome

#### Scenario: A previously verified migration is not re-attempted

- **WHEN** any of these subcommands starts and the migration sentinel
  already records `Status: "verified"`
- **THEN** no source path is re-scanned and no file is re-copied

### Requirement: v1's fleet state is never migrated or referenced

Migration SHALL distinguish the historical v1 fleet
(`~/.local/share/sergeant/fleet`) from pre-rebrand v2's fleet
(`~/.local/share/sergeant-v2/fleet`) and SHALL NOT read, copy, or
otherwise reference the former.

#### Scenario: v1 and pre-rebrand v2 fleets coexist on disk

- **WHEN** both `~/.local/share/sergeant/fleet/<task>/<repo>` (v1) and
  `~/.local/share/sergeant-v2/fleet/<task>/<repo>` (pre-rebrand v2)
  exist
- **THEN** only the pre-rebrand v2 worktree is copied to the new fleet
  root; nothing under the v1 path is read or copied

### Requirement: Source state is preserved, never mutated, until migration is verified

Migration SHALL only ever read from pre-rebrand v2 source paths and
write to post-rebrand destination paths — it SHALL NOT delete, rename,
or modify anything under `~/.config/sergeant`,
`~/.local/share/sergeant`, or `~/.local/share/sergeant-v2`.

#### Scenario: Source files are unchanged after migration

- **WHEN** migration runs to completion, whether it verifies or fails
- **THEN** every file under the pre-rebrand v2 source paths is
  byte-identical (or, for the database, produces an identical
  `VACUUM INTO` snapshot) to what it was before migration ran

### Requirement: Store migration is WAL-safe

Migrating the pre-rebrand v2 database SHALL produce a consistent
snapshot (via `VACUUM INTO` or equivalent) rather than copying the main
database file independently of its WAL file, so that writes present
only in the WAL are not silently lost.

#### Scenario: A write sitting only in the WAL file survives migration

- **WHEN** the source `sergeant.db` has a committed write that has not
  yet been checkpointed into the main database file (i.e. it exists
  only in `sergeant.db-wal`)
- **THEN** that write is present when the migrated destination database
  is opened and queried

### Requirement: A destination conflict is reported per-item and fails closed, never silently overwritten or merged

When a destination config file, the destination database, or a
destination fleet worktree already exists and differs from (or, for the
database, simply exists alongside) the corresponding source item,
migration SHALL report that specific item as a conflict, leave both the
source and the existing destination item unmodified, and continue
migrating every other, non-conflicting item.

#### Scenario: A conflicting config file is skipped, not overwritten

- **WHEN** `~/.config/sgt/<name>.yaml` already exists and its content
  differs from the source's `~/.config/sergeant/<name>.yaml`
- **THEN** the destination file is left unmodified, the conflict is
  recorded naming that file, and other non-conflicting config files
  still migrate

#### Scenario: An already-populated destination database is not merged

- **WHEN** `~/.local/share/sgt/sgt.db` already exists
- **THEN** the store migration step is skipped and recorded as a single
  conflict; no row from the source database is written into the
  existing destination database

#### Scenario: A conflicting fleet worktree is skipped, not overwritten

- **WHEN** a worktree already exists at
  `~/.local/share/sgt-v2/fleet/<task>/<repo>`
- **THEN** that worktree is left unmodified, the conflict is recorded
  naming `<task>/<repo>`, and other non-conflicting worktrees still
  migrate

### Requirement: Migration verifies its own result before declaring success

After copying, migration SHALL independently re-read the destination
(project count, run/phase counts, and each copied worktree's git
status/HEAD) and compare against the source, recording the outcome as
`verified` only if they match.

#### Scenario: A verification mismatch is reported and left in place, not rolled back

- **WHEN** post-migration verification finds a mismatch between source
  and destination counts or worktree state
- **THEN** the sentinel records `Status: "failed"` with the specific
  mismatch, and the partially-written destination is left exactly as it
  is (no rollback, no deletion)

#### Scenario: A failed migration is automatically retried

- **WHEN** a subcommand covered by this change starts and the migration
  sentinel records `Status: "failed"`
- **THEN** migration is attempted again automatically, and the sentinel
  is overwritten with the new attempt's outcome

### Requirement: Migration outcome is durably visible, not only in a log or the sentinel file

Migration's status, including any conflicts or mismatches, SHALL be
reachable through `sgt`'s dashboard/API without the operator needing to
inspect the sentinel file directly or already know to look for it.

#### Scenario: A conflict or failure is visible from the dashboard

- **WHEN** the migration sentinel records any conflict or a `"failed"`
  status
- **THEN** that information is exposed through `internal/ui`'s API and
  rendered visibly in the dashboard

#### Scenario: A clean or absent migration state adds no dashboard noise

- **WHEN** no pre-rebrand v2 state was ever detected, or migration
  completed with `Status: "verified"` and no conflicts
- **THEN** the dashboard shows no migration-related banner

### Requirement: An operator can trigger and inspect migration on demand

`sgt` SHALL provide a `migrate` subcommand that runs the same migration
logic as the automatic trigger and prints the resulting status,
including any conflicts, without waiting for the next automatic
trigger.

#### Scenario: Running `sgt migrate` with nothing to migrate

- **WHEN** `sgt migrate` runs and no pre-rebrand v2 state exists and no
  prior migration sentinel exists
- **THEN** it reports nothing to migrate and exits successfully

#### Scenario: Running `sgt migrate` surfaces conflicts and exits non-zero

- **WHEN** `sgt migrate` runs and migration completes with one or more
  conflicts, or with `Status: "failed"`
- **THEN** it prints each conflict/mismatch by name and exits with a
  non-zero status
