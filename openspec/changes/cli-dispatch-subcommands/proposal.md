# Proposal — CLI dispatch subcommands

## Repository

One repository: `sgt`. Standalone, but stacked on (depends on)
`mcp-dispatch-and-create-pr-tools`, which introduces the
`internal/sgtclient` package this change extends rather than
duplicates. That change's branch (`feat/mcp-dispatch-and-create-pr-tools`,
PR #72) must merge first, or this change's branch must be rebased onto
it before merge.

## Requirements served

`docs/prd-cli-dispatch-subcommands.md` (full text, including its
Decisions and Quality bar sections — the Quality bar is the identical
one `mcp-dispatch-and-create-pr-tools` was held to, not restated
separately). Extends D1, O2, O3.

## Problem

`cmd/sgt` has five subcommands (`run`, `status`, `ui`, `mcp`,
`version`) and no CLI way to drive dispatch — see the PRD's Problem
section for the full account. See also
`docs/prd-mcp-dispatch-and-create-pr-tools.md`'s Problem section: the
identical gap, on the MCP surface, has already been closed.

## Proposal

- Extend `internal/sgtclient` (added by `mcp-dispatch-and-create-pr-tools`)
  with `Runs(addr string, project string) ([]RunsResponseItem, error)`
  and `RunDetails(addr string, runID string) (*RunDetailsResponse,
  error)`, matching `handleRuns`/`handleRunDetails`'s JSON shapes
  exactly. `Dispatch`/`CreatePR` are reused as-is, unmodified.
- Four new `cmd/sgt` subcommands — `dispatch`, `runs`, `run-details`,
  `create-pr` — each a thin CLI-flags-to-`sgtclient`-call-to-stdout-JSON
  wrapper. `SGT_UI_ADDR` resolves the target `sgt ui` (default
  `http://127.0.0.1:8484`); an unreachable target produces the error
  `sgtclient`'s shared `doJSON` already produces (added by the other
  change) and exits 1.
- `sgt --help`/`printUsage()` lists all four alongside the existing
  five.
- Output is the endpoint's own JSON response, written to stdout
  verbatim (via the same struct `sgtclient` already decoded it into,
  re-marshaled — not a second independent rendering).

## Out of scope

- `run-cancel`/`run-resume` subcommands — explicit follow-up per the
  PRD.
- Any change to `handleRuns`/`handleRunDetails`/`handleDispatch`/
  `handleCreatePR` themselves, or to `sgt status`'s existing
  direct-store behavior.
- Relay dispatch's `--remote` flag.
- The MCP tools — those are `mcp-dispatch-and-create-pr-tools`'s scope,
  already shipped.
