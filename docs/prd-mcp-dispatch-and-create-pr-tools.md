# Product Requirements: MCP Dispatch and Create-PR Tools

Status: Approved 2026-09-17 (grilled and resolved with the operator; see Decisions below)

Extends: `docs/prd-sgt.md`, decisions D1 ("agent-driven and
coordinator-driven paths are equal in standing"), D10 ("Sgt is an agent
host"), O2 ("work type declared before change resolution"), and O3
("dispatched work is held to the same standard"). Companion to
`docs/prd-cli-dispatch-subcommands.md` (issue #10) — both wrap the same
two HTTP handlers and must not drift into independent contracts.
Referenced by `docs/prd-relay-dispatch.md`'s "Interface: CLI and MCP"
section, which anticipates these tools as a prerequisite.

## Summary

`sgt mcp` exposes a tool set for an agent already working inside a
dispatched bullet (`sgt_get_brief`, `sgt_run_gates`, `sgt_emit_envelope`,
`sgt_seal_pr`, `sgt_status`, `sgt_run_status`, `sgt_run_wait`,
`sgt_graph_query`, `sgt_graph_explain`, `sgt_graph_affected`). There is
no MCP equivalent for the coordinator-driven half of the same workflow:
starting a new dispatch and opening its pull request both require raw
`POST /api/dispatch` / `POST /api/create-pr` HTTP calls today, even
though `sgt_run_status`/`sgt_run_wait` already exist to poll the result
of exactly that call. This PRD adds `sgt_dispatch` and `sgt_create_pr`,
mirroring `handleDispatch` and `handleCreatePR` exactly, so an
MCP-connected operator agent can drive dispatch end-to-end natively.

## Problem

This gap was hit directly this session, self-hosting `sgt` to fix its
own bugs (issues #21, #22, #14, #20, #19, #18, #11, #9, #16): every
dispatch and every PR open went through `curl` plus hand-rolled JSON,
including working around a real bug (`/api/create-pr`'s missing `~`
expansion, issue #8, since fixed) that a client sharing the HTTP
handler's own path-resolution logic would have avoided entirely,
because it would have been resolving the path the same way the handler
itself does rather than guessing.

`sgt_run_status` and `sgt_run_wait` (`internal/mcp/run_follow.go`) only
answer "what is the status of a run I already know the ID of." Nothing
in `internal/mcp/server.go`'s tool set can create that run or open its
PR — an MCP-connected agent (this session, concretely) hits a hard wall
at exactly the two actions that matter most: starting work and shipping
it.

**A distinct, pre-existing tool sits close enough to be a real source
of confusion for whoever designs and implements this**: `sgt_seal_pr`
(`internal/mcp/server.go` `case "sgt_seal_pr"`) takes the identical
field set `handleCreatePR` does (`run_id, project, repo, title, body`)
but calls a completely separate code path —
`runner.PhaseRunner.DeliverPullRequest`, not
`changerequest.Providers[...].Create` — operating directly on
`repoCfg.Path` (the repo as the *agent-driven* caller already has it
checked out) rather than a coordinator-dispatched isolated worktree, and
with no R3.5 human-approval seal gate (`SealBulletForRun`) in front of
it. This is not a bug in isolation — it is the correct shape for the
agent-driven path, where the caller's own agent CLI session, not a
coordinator-spawned worktree, is what already holds the change — but it
means the codebase is about to have two tools that both "open a pull
request for a run," doing so through two different mechanisms, and an
MCP client has no principled way to know which one a given situation
calls for unless the tool descriptions state it explicitly. See Open
Questions.

## Proposal

- Add `sgt_dispatch`, mirroring `POST /api/dispatch`'s request fields
  exactly (`project, brief, repos, agent, type, change_id,
  request_id`) and calling the same `handleDispatch`/
  `createRunAndDispatch` code path the HTTP handler does — not a
  reimplementation. Response shape mirrors `dispatchResponse` (task_id,
  project, change_id, change_dir, etc.).
- Add `sgt_create_pr`, mirroring `POST /api/create-pr`'s request fields
  exactly (`run_id, project, repo, title, body`) and calling
  `handleCreatePR`'s own logic (`SealBulletForRun` gate, then
  `changerequest.Providers[...].Create` against the run's isolated
  worktree, per issue #21's fix) — not `sgt_seal_pr`'s
  `DeliverPullRequest` path.
- Both tools are thin: the MCP tool handler decodes `args` into the same
  request shape the HTTP handler's `json.Decoder` would, then calls into
  the handler's own logic (refactored into a callable function if it is
  not already factored out from the `http.ResponseWriter`/`*http.Request`
  signature) rather than reimplementing validation, sealing, or change
  resolution a second time. Decision O2/O3 ordering (work type declared,
  change resolved, before any run/worktree/branch exists) must hold
  identically to the HTTP path.
- `sgt_dispatch`'s idempotency key (`request_id`) and its no-repos
  proposed-plan path map onto the MCP call exactly as they do over
  HTTP: a repeat `request_id` returns the original run and starts
  nothing; empty `repos` records a proposed plan and starts nothing.
  Neither is special-cased away for the MCP surface.

## Non-Goals

- Changing `sgt_seal_pr`'s existing behavior or callers. Whatever this
  PRD's Open Questions resolve to, existing agent-driven-path callers
  of `sgt_seal_pr` must keep working exactly as they do today unless a
  separate, explicit migration is proposed.
- Relay dispatch's `--remote <target>` parameter. `docs/prd-relay-dispatch.md`
  is where that surfaces once relay targets exist; these two tools
  target today's local-only `handleDispatch`/`handleCreatePR`.
- Any new MCP tool beyond the two named here (e.g. `sgt_run_cancel`,
  `sgt_run_resume`). Out of scope for this PRD; a real gap, but a
  separate one.
- Authentication/authorization on the MCP transport itself. Unchanged
  from every other existing `sgt mcp` tool.

## Acceptance Criteria

- `sgt_dispatch` and `sgt_create_pr` appear in `sgt mcp`'s tool list
  with an `inputSchema` matching the corresponding HTTP handler's
  request fields.
- Both tools call the same underlying logic the HTTP handlers already
  call — proven by a test that dispatches via the MCP tool and asserts
  the exact same store rows (run, intent, bullets) a `POST /api/dispatch`
  call would have produced, and likewise for create-pr against the
  same seal-then-provider-call sequence `handleCreatePR` runs. Nothing
  about this test requires `sgt ui` to be running.
- A repeated `request_id` through `sgt_dispatch` returns the original
  run and creates no second one, matching `POST /api/dispatch`'s
  existing idempotency test coverage.
- An empty `repos` list through `sgt_dispatch` records a proposed plan
  and starts no run, matching the HTTP path.
- `sgt_create_pr` refuses (with the same error) a bullet that is not
  green, exactly as `handleCreatePR`'s `SealBulletForRun` gate already
  does over HTTP.
- The Open Question below about `sgt_seal_pr` overlap is resolved and
  recorded in `design.md` before implementation starts, not discovered
  mid-implementation.

## Decisions (grilled and resolved 2026-09-17)

1. **Transport: HTTP.** `sgt_dispatch` and `sgt_create_pr` are real HTTP
   clients against a running `sgt ui` (default `http://127.0.0.1:8484`,
   overridable via `SGT_UI_ADDR`) — never in-process/direct-store. `sgt
   ui` is the always-running daemon in this project's operating model;
   direct-store tools (`sgt_get_brief` et al.) exist because they serve
   the agent-driven path, not because direct-store is the default
   posture for a new tool. This also resolves the "two independently
   bookkept processes" risk raised in the prior draft: there is exactly
   one process (`sgt ui`) ever driving a dispatched run's goroutine,
   full stop.
2. **`sgt_create_pr` coexists with `sgt_seal_pr`; neither replaces the
   other.** They answer different questions — `sgt_create_pr` opens a
   PR for a coordinator-dispatched run's isolated worktree (through
   `sgt ui`, the seal gate, `changerequest.Provider`); `sgt_seal_pr`
   opens one for work already sitting in the agent-driven caller's own
   current checkout. `sgt_seal_pr`'s implementation and behavior are
   unchanged by this PRD — only its tool `Description` is tightened to
   say explicitly it is for the caller's own checkout, not a
   coordinator-dispatched run, so a caller doesn't reach for the wrong
   one.
3. **Shared client package.** Both this PRD and
   `docs/prd-cli-dispatch-subcommands.md` depend on a new
   `internal/sgtclient` package: one typed Go function per endpoint
   (`Dispatch`, `CreatePR`, and — for the CLI's sake — `Runs`,
   `RunDetails`), each doing exactly one HTTP round-trip and JSON
   decode. `internal/mcp`'s new tool cases call these functions and
   nothing else for these two endpoints — no local `http.NewRequest`/
   `json.Marshal` of the request or response shape. This is what
   actually enforces "must not drift into independent contracts,"
   rather than leaving it as prose.
4. **Sequencing.** This PRD's OpenSpec change
   (`mcp-dispatch-and-create-pr-tools`) lands first and owns
   `internal/sgtclient`'s initial two functions (`Dispatch`,
   `CreatePR`). `docs/prd-cli-dispatch-subcommands.md`'s change
   (`cli-dispatch-subcommands`) lands second and extends the same
   package with `Runs`/`RunDetails` rather than inventing its own
   client code.
5. **Out of scope, follow-up:** `sgt_run_cancel`/`sgt_run_resume`.
   Small marginal cost once `sgtclient` exists, but neither issue #5
   nor this PRD asked for them — a separate, later PRD.

## Quality bar (ungameable, applies to both this PRD and
docs/prd-cli-dispatch-subcommands.md)

1. **Cross-surface parity test.** One test drives the same dispatch (or
   create-pr) three ways against one real `httptest.Server` wrapping
   the real `NewServer(...).Handler()` — a raw HTTP POST, the compiled
   `sgt` binary invoked as a real subprocess, and the MCP tool handler
   — and asserts all three produce identical store state (same run,
   intent, bullets) for the same valid input, and identical error text
   for the same invalid input. No mocking the HTTP layer for this test;
   no hardcoded expected JSON strings.
2. **Negative-path parity.** Every existing HTTP-level refusal
   (`SealBulletForRun`'s non-green rejection, O2's unrecognized-type
   rejection, O3's unknown-change-id rejection) is exercised through
   `sgt_dispatch`/`sgt_create_pr` too, reusing the existing fixtures
   rather than writing new ones that could quietly test something
   slightly different.
3. **Idempotency proven against real store rows.** Two `sgt_dispatch`
   calls with the same `request_id` produce exactly one run row in the
   actual database — checked directly, not inferred from both calls
   returning 200.
4. **Structural constraint.** `internal/mcp/server.go`'s new
   `sgt_dispatch`/`sgt_create_pr` cases contain no direct
   `http.NewRequest`/`json.Marshal` calls of their own for these two
   endpoints — only calls into `internal/sgtclient`, checkable by grep
   in review.
5. **No regressions.** Full `go build`/`go vet`/`go test ./internal/...`
   stays green, including every existing `sgt_seal_pr` and
   `handleCreatePR`/`handleDispatch` test, unmodified.
