# CLI factory run

## ADDED Requirements

### Requirement: sgt run uses the same execution path as a dispatch

`sgt run <project>` SHALL execute through the same run execution path as
`POST /api/dispatch`. That covers the gauntlet preflight, phase roles,
committing agent output, the delivery record, the final pass check, and the
terminal status write. It SHALL NOT have its own stage loop or its own
terminal status write. Its exit code SHALL be 0 only when the stored
terminal status of its run is `passed`.

#### Scenario: sgt run records and exits by the stored outcome

- **WHEN** `sgt run <project>` runs a project with one DAG stage whose gate
  fails
- **THEN** the run's stored status is `failed`, the command exits non-zero,
  and it prints the same reason that was recorded with the terminal status

#### Scenario: sgt run commits agent output and records delivery

- **WHEN** `sgt run <project>` completes a stage whose agent changed files
- **THEN** the worktree's changes are committed on the run branch, and a
  `run.delivered` envelope exists for the run

#### Scenario: sgt run applies gauntlet roles

- **WHEN** `sgt run <project>` runs a claude project with roles on and the
  probe reports gauntlet `0.1.0`
- **THEN** its `build` phase payload has `role` = `gauntlet:alpha` and
  `plugin_version` = `0.1.0`

### Requirement: sgt run refuses a project with nothing to run

`sgt run <project>` for a project with no DAG stages SHALL exit non-zero with
a message saying the project has no DAG stages and naming `sgt dispatch`. It
SHALL create no run row. Recording `passed` with no work performed SHALL NOT
happen.

#### Scenario: No DAG stages is refused, not passed

- **WHEN** `sgt run <project>` is invoked for a project with no `dag.stages`
- **THEN** the command exits non-zero, its message names `sgt dispatch`, and
  the store contains no new run
