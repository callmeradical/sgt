# Sgt manual and help search

## ADDED Requirements

### Requirement: `sgt help` answers a topic or question from the manual

`sgt help` SHALL accept an optional topic/question and answer from Sgt's
own manual instead of always printing a fixed subcommand list.

#### Scenario: No argument shows the manual's table of contents

- **WHEN** `sgt help` is run with no further arguments
- **THEN** it prints every manual section's title, followed by the
  existing subcommand list

#### Scenario: A query matching one section prints that section

- **WHEN** `sgt help <query>` matches exactly one manual section (by
  title or body)
- **THEN** that section's title and full body are printed

#### Scenario: A query matching several sections lists them

- **WHEN** `sgt help <query>` matches more than one manual section
- **THEN** every matching section's title is printed, with a pointer to
  ask again with a more specific query, rather than printing every
  matched section's full body at once

#### Scenario: An uncovered query is reported honestly

- **WHEN** `sgt help <query>` matches no manual section
- **THEN** it states that the manual does not cover the query and lists
  the manual's available section titles, rather than fabricating an
  answer

### Requirement: The manual's command and MCP-tool facts are generated live, never hand-copied

The manual's `Command reference` and `MCP tools for agents` sections
SHALL be generated from the running system's own data, so they cannot
silently drift out of sync with what the software actually does.

#### Scenario: The command reference reflects the real subcommand list

- **WHEN** the manual's `Command reference` section is rendered (by
  `sgt help` or `GET /api/manual`)
- **THEN** its content is generated from the same source `cmd/sgt`'s own
  `--help` output uses, not separately hand-written prose

#### Scenario: The MCP tools section reflects the real tool list

- **WHEN** the manual's `MCP tools for agents` section is rendered
- **THEN** its content is generated from `internal/mcp.Tools()`, not
  separately hand-written prose

### Requirement: The manual is reachable from the dashboard

The embedded dashboard SHALL provide a way to read the manual without
requiring a terminal.

#### Scenario: A dashboard drawer shows the manual

- **WHEN** an operator opens the dashboard's manual drawer
- **THEN** it shows the same section titles and content `sgt help`
  answers from, fetched from `GET /api/manual`

#### Scenario: The manual endpoint is a pure read

- **WHEN** `GET /api/manual` is called
- **THEN** no database row or file is written or modified as a result

### Requirement: `sgt help`, the dashboard, and the agent skill share one manual

`skills/sgt-help/SKILL.md` SHALL name the manual as the first reference
to check, so all three surfaces answer from the same content.

#### Scenario: The skill's documentation map names the manual first

- **WHEN** `skills/sgt-help/SKILL.md`'s documentation map is read for any
  general question
- **THEN** the manual (`internal/manual/manual.md`) is listed before the
  existing per-topic file list
