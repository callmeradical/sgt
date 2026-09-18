# Proposal — MCP dispatch and create-PR tools

## Repository

One repository: `sgt`. Standalone.

## Requirements served

`docs/prd-mcp-dispatch-and-create-pr-tools.md` (full text, including its
Decisions and Quality bar sections — both binding). Extends D1
(agent-driven and coordinator-driven paths are equal in standing), D10
("Sgt is an agent host"), O2, and O3. Companion to
`cli-dispatch-subcommands` (not yet landed) — this change lands first
and owns the shared client package the other depends on.

## Problem

`sgt mcp` has no way to start a dispatch or open a pull request — every
such action goes through raw `curl` against `/api/dispatch`/
`/api/create-pr` today, even though `sgt_run_status`/`sgt_run_wait`
already exist to poll the result of exactly that call. See the PRD's
Problem section for the full account, including the pre-existing
`sgt_seal_pr` tool's overlap with (and distinctness from) what this
change adds.

## Proposal

- New package `internal/sgtclient`: `Dispatch(addr string, req
  DispatchRequest) (*DispatchResponse, error)` and `CreatePR(addr
  string, req CreatePRRequest) (*CreatePRResponse, error)`, each one
  HTTP round-trip against a running `sgt ui`, request/response structs
  matching `handleDispatch`/`handleCreatePR`'s own JSON shapes exactly.
  `addr` resolves from `SGT_UI_ADDR` (default `http://127.0.0.1:8484`)
  at the call site, not hidden inside the package.
- New MCP tools `sgt_dispatch` and `sgt_create_pr` in
  `internal/mcp/server.go`, registered in the tool list alongside the
  existing ten. Each tool's `executeTool` case decodes `args` into the
  corresponding `sgtclient` request struct and calls the matching
  function — no other logic.
- `sgt_seal_pr`'s `Description` field is tightened to say explicitly it
  is for work already sitting in the caller's own current checkout, not
  a coordinator-dispatched run. No change to its implementation.
- Both new tools surface `sgtclient`'s returned error text verbatim as
  the MCP tool error — including an unreachable-`sgt ui` connection
  error, which should read the same actionable way `cli-dispatch-subcommands`
  makes it read for the CLI ("`sgt ui` is not reachable at `<addr>`;
  start it with `sgt ui`").

## Out of scope

- `sgt_run_cancel`/`sgt_run_resume` — explicit follow-up per the PRD.
- Any change to `sgt_seal_pr`'s behavior, `handleDispatch`'s or
  `handleCreatePR`'s behavior, or any other existing MCP tool.
- Relay dispatch's `--remote` parameter.
- `cli-dispatch-subcommands`'s own subcommands — that change extends
  `internal/sgtclient` with `Runs`/`RunDetails` separately; this change
  does not add CLI subcommands.
