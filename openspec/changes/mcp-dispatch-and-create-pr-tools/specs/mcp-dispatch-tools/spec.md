# MCP dispatch and create-PR tools

## ADDED Requirements

### Requirement: An MCP-connected agent can start a dispatch without shelling out

`sgt mcp` SHALL expose a tool, `sgt_dispatch`, that starts a dispatch
against a running `sgt ui` instance using the same fields and the same
underlying behavior as `POST /api/dispatch`.

#### Scenario: Dispatching explicit repos through the MCP tool creates the same records the HTTP endpoint would

- **WHEN** `sgt_dispatch` is called with `project`, `brief`, `type`, and
  a non-empty `repos` list
- **THEN** a run, an intent, and one bullet per named repo are recorded,
  identical in shape to what `POST /api/dispatch` with the same fields
  would record

#### Scenario: Dispatching with no repos records a proposed plan, not a run

- **WHEN** `sgt_dispatch` is called with an empty or omitted `repos`
  list
- **THEN** an intent and its bullets are recorded with status
  `"proposed"`, and no run is created

#### Scenario: A repeated request_id returns the original run

- **WHEN** `sgt_dispatch` is called twice with the same non-empty
  `request_id`
- **THEN** exactly one run is recorded, and the second call's response
  names that same run

#### Scenario: An unrecognized work type is refused before anything is created

- **WHEN** `sgt_dispatch` is called with a `type` not in the
  recognized set
- **THEN** the call is refused with the same message
  `POST /api/dispatch` produces for the same input, and no run, intent,
  or bullet is created

### Requirement: An MCP-connected agent can open a pull request for a coordinator-dispatched run without shelling out

`sgt mcp` SHALL expose a tool, `sgt_create_pr`, that opens a pull
request for a run's isolated worktree using the same fields and the
same underlying behavior as `POST /api/create-pr` — the human-approval
seal gate included.

#### Scenario: Sealing and opening a PR for a green bullet succeeds

- **WHEN** `sgt_create_pr` is called naming a run/repo whose bullet is
  `"green"`
- **THEN** the bullet is sealed, a pull request is opened through the
  provider seam against the run's isolated worktree, and the bullet's
  recorded PR URL matches the opened pull request

#### Scenario: A non-green bullet is refused, not sealed

- **WHEN** `sgt_create_pr` is called naming a run/repo whose bullet is
  not `"green"`
- **THEN** the call is refused with the same message
  `POST /api/create-pr` produces for the same input, the bullet's
  status is unchanged, and no provider call is made

### Requirement: `sgt_create_pr` and the pre-existing `sgt_seal_pr` tool remain distinct, documented mechanisms

Adding `sgt_create_pr` SHALL NOT change `sgt_seal_pr`'s existing
behavior. Both tools' descriptions SHALL state which situation each
applies to.

#### Scenario: sgt_seal_pr's existing behavior is unchanged

- **WHEN** `sgt_seal_pr` is called exactly as it was before this change
- **THEN** it behaves identically — no seal gate is added, no provider
  seam is substituted

### Requirement: Both new tools are thin clients, not a second implementation

Both `sgt_dispatch` and `sgt_create_pr` SHALL construct their HTTP
request and decode their response using the shared `internal/sgtclient`
package exclusively, never a locally constructed `http.Request` or a
locally defined response struct for these two endpoints.

#### Scenario: An unreachable sgt ui produces one consistent, actionable error

- **WHEN** `sgt_dispatch` or `sgt_create_pr` is called while no `sgt
  ui` process is listening at the resolved address
- **THEN** the tool returns an error naming the address and instructing
  the caller to start it with `sgt ui`
