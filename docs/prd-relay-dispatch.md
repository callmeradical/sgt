# Product Requirements: Relay Dispatch

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, decisions D4 ("Sgt stores intents and bullets
itself"), D6 ("Sequenced submission, human merge"), D8 ("The dashboard is
a view of intents"), D10 ("Sgt is an agent host"), and O3 ("Dispatched
work is held to the same standard").

## Summary

Today, every dispatch executes on the same machine that runs the
coordinator (`sgt ui`): the worktree, the agent CLI process, and the
gates all live on one box. **Relay dispatch** lets a project name a
remote `sgt` instance as the execution target for one or more of its
repos. The remote runs its own unmodified local dispatch — its own
worktree, its own agent CLI, its own gates — while the *coordinator's*
store remains the sole source of truth for the intent, bullet, and run
records (D4, D8). The remote is a pure execution backend: its activity
is mirrored back into the local store as evidence, not held anywhere
else as a second authority.

This is distinct from **managed dispatch** — handing work to a fully
external, non-`sgt` service such as GitHub's hosted Copilot coding
agent, where completion arrives via a foreign API/webhook rather than
another `sgt` instance's own well-known contract. Managed dispatch is
explicitly out of scope for this PRD (see below).

## Problem

Sgt's dispatch model assumes the coordinator and the executor are the
same machine. This session surfaced three concrete costs of that
assumption while self-hosting `sgt` to fix its own bugs:

- A laptop going to sleep (or its lid closing) kills an in-flight agent
  phase outright — `API Error: your computer went to sleep mid-response`
  — rather than pausing and resuming. One bug fix burned three build-gate
  attempts and several hours of wall clock to this alone.
- Concurrent dispatches compete for one machine's finite resources.
  Three simultaneous dispatches OOM-killed all three `go test` gates on
  this machine; there is no isolation between unrelated runs beyond the
  worktree boundary.
- An operator who wants to dispatch work and then be unreachable (close
  the laptop, board a flight) cannot, today, without the work stopping
  with them.

## Proposal

- A repo entry in a project's YAML gains an optional `remote:` block
  (target name/URL plus a credential reference) as an alternative to a
  local `path:`. A repo names at most one relay target; this PRD does
  not propose a pool or scheduling policy across multiple remotes.
- When a repo declares a `remote:` target, dispatch constructs a
  `RelayEngine` instead of a local `dag.Engine`. Both satisfy the same
  `stageRunner` interface (`RunStage(ctx, runID, *DAGStage) error`) that
  `executeRun` already drives every stage through today — this is an
  existing seam, not new plumbing in `dispatch.go`/`executeRun` itself.
- `RelayEngine.RunStage` forwards the stage to the remote target's own
  `/api/dispatch`. The remote does everything it already knows how to
  do — worktree, agent CLI, gates — with no new capability required on
  that side beyond authenticating the relay link.
- As the remote run progresses, `RelayEngine` mirrors its phases and
  envelopes into the *local* store, tagged with a provenance marker
  (e.g. `Producer: "sgt/relay"`, carrying the remote's own run id) so
  the existing dashboard rendering path — unchanged — can show
  "executed via relay: `<target>`" as a label on records it already
  knows how to render. Delivery/PR evidence flows back the same way
  local `run.delivered` envelopes already do.
- The scaffolded OpenSpec change directory (O3) is resolved locally, as
  it is today, and its file contents travel as part of the relay
  request payload — the remote's independently-cloned checkout cannot
  be assumed to already have it. (Direct lesson from issue #273: a
  worktree only contains committed history, and two independent clones
  of the same origin cannot be assumed to agree on anything neither has
  pushed yet.)
- Neither side needs to "stay in sync" as an ongoing process. Local and
  remote each fetch the same GitHub origin independently at
  dispatch/execution time — the same way any two developers' clones of
  one repo relate to each other.

### Credential model

- **Git/GitHub access on the remote** is a fine-grained Personal Access
  Token the operator explicitly supplies per relay target, scoped to
  the relevant repo(s) — not a GitHub App (organizationally impractical
  to get approved in this environment). As an explicit, opt-in
  alternative per target, the operator may instead forward their own
  ambient `gh auth token` — simpler to set up, but broader-scoped and
  tied to their personal identity, so this is a per-target choice, not
  a default.
- **Agent CLI auth on the remote** is, where the target agent CLI
  supports a custom endpoint/base-URL override, a per-run virtual key
  minted from the operator's existing LLMGateway deployment (already in
  production use for the `smith` project) — no durable storage needed,
  mint before dispatch, expire after. Where the CLI requires its own
  native device/session login (this may not be portable at all — e.g.
  session-bound OAuth), the remote is pre-provisioned with its own
  ambient login instead, out of band, the same way any CI runner or dev
  box is provisioned once.
- **The relay link itself** (coordinator authenticating to a remote
  `sgt`) is its own small, per-target credential.
- Whatever holds the PAT/relay-token values does so as a narrowly
  scoped, encrypted-at-rest store keyed by remote-target name — not a
  general secrets platform. LLMGateway virtual keys need no such
  storage since they are minted fresh per run.

## Interface: CLI and MCP

Today, driving dispatch from outside the dashboard means raw HTTP calls
— `curl` plus manual JSON parsing — for everything except the read-only
MCP tools (`sgt_status`, `sgt_run_status`, `sgt_run_wait`, etc.). This
session's self-hosting sweep ran entirely this way, including working
around a real bug (`/api/create-pr`'s missing `~` expansion, filed as
issue #8) that a proper client would have caught or avoided. Relay
dispatch makes this worse before it makes it better: a `remote:` block
adds a new axis (`--remote <target>`) to every dispatch call, which is
exactly the kind of detail a thin `curl` wrapper gets wrong silently.

Two surfaces, not competing with each other:

- **`sgt` CLI subcommands** (`cmd/sgt`, alongside the existing
  `run`/`status`/`ui`/`mcp`/`version`) wrapping the HTTP API directly:
  `sgt dispatch`, `sgt runs`, `sgt run-details`, `sgt create-pr`, each
  taking the same fields the corresponding endpoint does, plus
  `--remote <target>` where relay applies. This is the interface a
  human operator (or a shell script, or an agent driving a subprocess)
  uses without an MCP connection.
- **New MCP tools** `sgt_dispatch` and `sgt_create_pr`, already scoped
  in issue #5, mirroring the same HTTP handlers' request/response shape
  exactly so the CLI and MCP surfaces cannot drift into two independent
  contracts. This is the interface an MCP-connected agent uses natively
  without shelling out at all.

Both surfaces should be thin: neither reimplements dispatch logic, both
call the same HTTP handlers a browser-based dashboard client already
calls. Decision O2/O3 ordering (work type declared, change resolved,
before any run/worktree/branch exists) must hold identically across
all three surfaces (HTTP, CLI, MCP) — one handler underneath, three thin
callers.

## Out of scope

- **Managed dispatch** (GitHub's hosted Copilot coding agent, or any
  other fully-external managed execution backend). Different
  completion/status semantics; separate PRD.
- **A general-purpose secrets/credential platform.** This PRD's
  credential needs are narrow (PATs and relay tokens by target name);
  building something Vault-shaped is explicitly rejected.
- **Scheduling or load-balancing across multiple relay targets.** One
  named target per repo; no pool.
- **General HTTP API authentication hardening** beyond what the relay
  link itself needs. `sgt ui` binds to `127.0.0.1` with no auth on any
  endpoint today; relay dispatch is the first scenario that requires
  this daemon to be reachable from a different machine at all, but
  broader "make every endpoint safe on a real network" work is implied,
  not specified, here.
- **Continuous bidirectional sync** between local and remote checkouts.
  Each side fetches from the same GitHub origin independently; there is
  no mirroring process to design.

## Open questions

- Which of `sgt`'s supported agent harnesses (`claude`, `codex`,
  `goose`, `opencode`, `pi`, `copilot`) actually accept a custom
  LLM endpoint/base-URL with a supplied key, versus requiring their own
  native device/session login? This determines how much of the
  agent-CLI-auth surface LLMGateway's virtual keys can cover versus
  needing ambient pre-provisioned login instead.
- What is the actual relay-link auth mechanism — bearer token over TLS,
  mTLS, something else? Left to OpenSpec `design.md`.
- Does this PRD's scope include adding real authentication to
  `/api/dispatch`/`/api/create-pr`/etc., or is that a prerequisite
  change relay dispatch depends on rather than delivers?
- Is the PAT-vs-ambient-`gh auth token` choice made explicitly per
  remote target by the operator, or does this PRD need to pick one
  default?
