## ADDED Requirements

### Requirement: sgt migrate renames old-named paths to new-named paths
`sgt migrate` SHALL check, for each of config dir, database, and fleet
root, whether the old `sergeant`-named path exists and the new
`sgt`-named path does not, and if so rename (not copy) the old path to
the new path, reporting exactly what moved.

#### Scenario: Old path present, new path absent
- **WHEN** `sgt migrate` runs and `~/.config/sergeant` exists while
  `~/.config/sgt` does not
- **THEN** `~/.config/sergeant` is renamed to `~/.config/sgt` and the
  command reports the old and new paths

#### Scenario: Same behavior applies to database and fleet root
- **WHEN** `sgt migrate` runs and the old database path or old fleet
  root exists while its corresponding new path does not
- **THEN** that path is likewise renamed and reported, independently of
  the other two paths' state

### Requirement: A conflict is reported, never silently resolved
When both the old-named and new-named path exist for a given path
kind, `sgt migrate` SHALL change nothing for that path and SHALL report
the conflict, naming both paths, rather than guessing which is
authoritative.

#### Scenario: Both old and new database paths exist
- **WHEN** `sgt migrate` runs and both `~/.local/share/sergeant/sergeant.db`
  and `~/.local/share/sgt/sgt.db` exist
- **THEN** neither path is modified, and the command reports both paths
  as an unresolved conflict requiring manual action

### Requirement: Nothing-to-migrate is reported, not silent
When neither the old path exists, or the new path already exists (with
no old path present), `sgt migrate` SHALL report that there is nothing
to migrate for that path kind rather than producing no output.

#### Scenario: Fresh install with no old paths
- **WHEN** `sgt migrate` runs and none of the three old-named paths
  exist
- **THEN** the command reports nothing-to-migrate for all three path
  kinds and exits successfully

### Requirement: Path-dependent subcommands refuse to start from a false-empty state
`sgt ui`, `sgt mcp`, `sgt run`, and `sgt status` SHALL refuse to start,
with a clear and actionable error naming the exact old path found, when
the old-named config dir, database, or fleet root exists but the
corresponding new-named path does not.

#### Scenario: Starting sgt ui with an unmigrated database
- **WHEN** `sgt ui` starts and `~/.local/share/sergeant/sergeant.db`
  exists while `~/.local/share/sgt/sgt.db` does not
- **THEN** `sgt ui` exits with an error naming
  `~/.local/share/sergeant/sergeant.db` and instructing the operator to
  run `sgt migrate`, rather than starting against an empty database

#### Scenario: Starting sgt ui after migration succeeds normally
- **WHEN** `sgt ui` starts and the new-named database, config dir, and
  fleet root all exist (whether freshly created or already migrated)
- **THEN** `sgt ui` starts normally with no migration-related error

### Requirement: Database path has an environment override
The database path SHALL be overridable via an `SGT_DB_PATH` environment
variable, matching the existing override pattern `SGT_CONFIG` and
`SGT_FLEET_DIR` already establish for the other two paths.

#### Scenario: SGT_DB_PATH overrides the default database location
- **WHEN** `SGT_DB_PATH` is set to an explicit path
- **THEN** every subcommand that opens the store uses that path
  instead of the default `~/.local/share/sgt/sgt.db`, and the
  old/new-path refusal check in the prior requirement is skipped for
  the database (an explicit override is the operator's own decision,
  not a state migration needs to arbitrate)
