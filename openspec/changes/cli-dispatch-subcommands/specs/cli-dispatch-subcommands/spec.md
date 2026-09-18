# CLI dispatch subcommands

## ADDED Requirements

### Requirement: An operator can dispatch, list runs, inspect a run, and open a PR from the CLI without an MCP connection

`cmd/sgt` SHALL expose `dispatch`, `runs`, `run-details`, and
`create-pr` subcommands, each a thin client of the corresponding
`sgt ui` HTTP endpoint via `internal/sgtclient`, targeting the address
resolved from `SGT_UI_ADDR` (default `http://127.0.0.1:8484`).

#### Scenario: sgt dispatch with explicit repos creates the same records the HTTP endpoint would

- **WHEN** `sgt dispatch` is run with `--project`, `--brief`, `--type`,
  and one or more `--repo` flags, against a reachable `sgt ui`
- **THEN** a run, an intent, and one bullet per named repo are
  recorded, identical in shape to what `POST /api/dispatch` with the
  same fields would record, and the CLI prints that response as JSON
  to stdout

#### Scenario: sgt dispatch with no repos records a proposed plan

- **WHEN** `sgt dispatch` is run with no `--repo` flags
- **THEN** an intent and its bullets are recorded with status
  `"proposed"`, and no run is created

#### Scenario: sgt runs and sgt run-details reflect real stored state

- **WHEN** `sgt runs` (optionally with `--project`) or `sgt run-details
  <id>` is run against a reachable `sgt ui`
- **THEN** the printed JSON matches what `GET /api/runs`/
  `GET /api/run-details` would return for the same parameters

#### Scenario: sgt create-pr enforces the same seal gate the HTTP endpoint does

- **WHEN** `sgt create-pr` is run naming a run/repo whose bullet is not
  `"green"`
- **THEN** the command exits non-zero with the same refusal text
  `POST /api/create-pr` produces for the same input, and no pull
  request is opened

### Requirement: An unreachable `sgt ui` produces a clear, actionable failure, never a hang or a stack trace

Each of the four subcommands SHALL fail fast and clearly when no
`sgt ui` process is reachable at the resolved address.

#### Scenario: No sgt ui running

- **WHEN** any of the four subcommands is run while nothing is
  listening at the resolved `SGT_UI_ADDR`
- **THEN** the command exits non-zero and prints an error naming the
  address and instructing the operator to start it with `sgt ui`

### Requirement: These subcommands are thin clients, not a second implementation

All four subcommands SHALL construct their requests and decode their
responses exclusively through `internal/sgtclient`.

#### Scenario: CLI and MCP surfaces agree on the same dispatch

- **WHEN** the same dispatch is driven once through `sgt dispatch` and
  once through the `sgt_dispatch` MCP tool (added by
  `mcp-dispatch-and-create-pr-tools`), against the same `sgt ui`
- **THEN** both produce identical resulting store state
