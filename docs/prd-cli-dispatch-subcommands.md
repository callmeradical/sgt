# Product Requirements: CLI Dispatch Subcommands

Status: Approved 2026-09-17 (grilled and resolved with the operator; see Decisions below)

Extends: `docs/prd-sgt.md`, decisions D1 ("agent-driven and
coordinator-driven paths are equal in standing"), O2, and O3.
Companion to `docs/prd-mcp-dispatch-and-create-pr-tools.md` (issue
#5) — both wrap the same two HTTP handlers plus two read-only ones, and
must not drift into independent contracts. Referenced by
`docs/prd-relay-dispatch.md`'s "Interface: CLI and MCP" section, which
anticipates these subcommands as a prerequisite and as the future home
of a `--remote <target>` flag.

## Summary

`cmd/sgt` has exactly five subcommands: `run`, `status`, `ui`, `mcp`,
`version`. There is no CLI way to drive the dispatch API — every
interaction outside the dashboard and the (currently read-only, until
`docs/prd-mcp-dispatch-and-create-pr-tools.md` lands) MCP tools requires
raw `curl` against `/api/dispatch`, `/api/runs`, `/api/run-details`,
`/api/create-pr`. This PRD adds four subcommands — `sgt dispatch`, `sgt
runs`, `sgt run-details`, `sgt create-pr` — as thin wrappers over those
same four HTTP endpoints, for the human-operator/shell-script/subprocess
surface that has no MCP connection.

## Problem

This session's entire self-hosted bug-fix sweep (issues #21, #22, #14,
#20, #19, #18, #11, #9, #16) was driven by hand-rolled `curl` + JSON
parsing against a locally running `sgt ui`, because nothing else
exists. That included working around a real, now-fixed bug (issue #8:
`/api/create-pr` never expanded a `~`-prefixed repo path) that a
client reusing the handler's own path-resolution logic — rather than a
`curl` command with no logic of its own at all — would most likely have
avoided, or at least surfaced with a clearer error than a raw `gh`
process failing to `chdir`.

A human operator scripting a batch of dispatches, or an agent
CLI driving `sgt` as a subprocess rather than over MCP, has no better
option today than reimplementing request construction and response
parsing from scratch for each of these four endpoints, in whatever
language that caller happens to be in.

## Proposal

- `sgt dispatch` — wraps `POST /api/dispatch`. Flags for `project`,
  `brief`, `repos` (repeatable or comma-separated), `agent`, `type`,
  `change_id`, `request_id`. Prints the same JSON
  `handleDispatch`/`dispatchResponse` returns (task_id, project,
  change_id, change_dir, etc.) to stdout.
- `sgt runs` — wraps `GET /api/runs`. A `--project` flag matching
  `handleRuns`' own `project`/`all` scoping convention exactly (empty
  or `all` combines every project).
- `sgt run-details` — wraps `GET /api/run-details`. Takes a run id,
  prints phases/envelopes/resume-skip fields exactly as the endpoint
  returns them.
- `sgt create-pr` — wraps `POST /api/create-pr`. Flags for `run_id`,
  `project`, `repo`, `title`, `body`. Same seal-then-provider-call
  sequence, same refusal (and same message) for a non-green bullet,
  because it is the same handler underneath, not a second
  implementation of the seal check.
- Each subcommand surfaces the HTTP handler's own validation errors
  verbatim (work-type validation, agent validation, O3 change
  resolution) — never a separate, possibly-inconsistent CLI-side
  re-validation that could disagree with what the server actually
  enforces.
- **Transport is the open design question this PRD does not answer by
  default** — see Open Question 1. The existing subcommand closest to
  this shape, `sgt status` (`cmd/sgt/main.go`'s `showStatus`), opens
  `~/.local/share/sgt/sgt.db` directly and reads with no HTTP call and
  no dependency on `sgt ui` running at all. `run`/`dispatch`/
  `run-details`/`create-pr` are not pure reads the way `status` is,
  which is exactly what makes the transport question real instead of
  already answered by precedent — see Open Question 1.

## Non-Goals

- Relay dispatch's `--remote <target>` flag. `docs/prd-relay-dispatch.md`
  is where that lands once relay targets exist; this PRD's four
  subcommands target today's local-only endpoints on whatever `sgt ui`
  instance they're pointed at.
- `sgt run-cancel` / `sgt run-resume` subcommands. Real gaps, same
  shape, explicitly deferred to keep this PRD to the four endpoints
  issue #10 actually named.
- Any new server-side capability. Every subcommand here calls an
  endpoint that already exists; nothing in `internal/ui` changes
  behavior as a result of this PRD.
- Reimplementing dispatch/create-pr logic CLI-side "for speed" or to
  avoid an HTTP round-trip. The whole point is one behavior, driven
  through the existing HTTP handlers, from three surfaces (dashboard,
  CLI, MCP) — not three implementations of the same behavior.

## Acceptance Criteria

- `sgt dispatch`, `sgt runs`, `sgt run-details`, and `sgt create-pr`
  exist as subcommands alongside `run`/`status`/`ui`/`mcp`/`version`
  in `sgt --help`'s usage output.
- Each subcommand's request maps 1:1 onto the corresponding endpoint's
  fields, proven against real server-side state (the store rows
  actually created/read), not just the CLI's own stdout — the specific
  test shape (an `httptest.Server` the CLI talks to over HTTP, or a
  direct in-process call) follows whatever Open Question 1 decides.
- A validation failure the HTTP handler already produces (e.g. an
  unrecognized `type`) is surfaced by the CLI with the same message,
  not a CLI-specific rewrite of it.
- `sgt create-pr` against a non-green bullet fails with
  `handleCreatePR`'s own refusal message, proven against the same
  fixture pattern `TestCreatePRForNonGreenBulletIsRefusedAndNeverInvokesGH`
  already uses.
- Running any of the four subcommands with no `sgt ui` process
  reachable produces a clear, actionable error (see Open Questions for
  exactly what it says) rather than a bare connection-refused stack
  trace or a silent hang.

## Decisions (grilled and resolved 2026-09-17)

1. **Transport: HTTP, for all four subcommands uniformly**, including
   `runs`/`run-details` even though they're pure reads. `sgt ui` is
   the always-running daemon in this project's operating model; `sgt
   status`'s direct-store style is a pre-existing exception (predates
   this PRD), not a precedent to extend, and mixing transports across
   four subcommands in one PRD would itself be a source of confusion.
2. **Discovery and failure mode.** `SGT_UI_ADDR` env var (default
   `http://127.0.0.1:8484`, matching `NewServer`'s own fallback), no
   `--addr` flag for now — one override mechanism until something
   actually needs a second. Unreachable → a clear, actionable error
   ("sgt ui is not reachable at `<addr>`; start it with `sgt ui`") and
   exit 1. No auto-start.
3. **Output format: JSON by default, no table rendering, no `--json`
   flag.** A direct passthrough of each endpoint's own response shape
   — nothing to opt into since there's only one format. `sgt status`
   stays as its own older, human-oriented thing; not retrofitted
   either direction.
4. **Shared client package.** New `internal/sgtclient` package, one
   typed Go function per endpoint. `cmd/sgt`'s four new subcommands
   call these functions and nothing else — no local `http.NewRequest`/
   response-struct definitions of their own. `Dispatch`/`CreatePR` are
   introduced by `docs/prd-mcp-dispatch-and-create-pr-tools.md`'s
   change, which lands first; this PRD's change extends the same
   package with `Runs`/`RunDetails`.
5. **Out of scope, follow-up:** `run-cancel`/`run-resume` subcommands.
   Real gap, same shape, small marginal cost once `sgtclient` exists —
   deliberately deferred rather than folded in here.

## Quality bar

Identical to `docs/prd-mcp-dispatch-and-create-pr-tools.md`'s Quality
bar section — both PRDs are held to the same five criteria (cross-surface
parity test across HTTP/CLI/MCP, negative-path parity, idempotency
against real store rows, the structural "only `sgtclient`" constraint,
and a fully green existing suite). Not restated here to avoid the two
copies drifting; that section is the canonical one.
