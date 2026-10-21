# Sgt manual

## What is Sgt

Sgt is a single-user, local-first software factory: a Go-native engine
(`sgt`) plus an `AGENTS.md` and a set of skills that turn a
general-purpose coding agent into a project-aware first mate. A
*project* is a named collection of repositories — an API, a frontend,
an infra chart, a shared library — declared once in a YAML file so that
every tool built on top of Sgt (the CLI, the dashboard, an agent
session) shares the same understanding of how those repos relate.

Work is tracked as a durable hierarchy: a **Project** is a named set of
repositories; an **Intent** is a durable statement of desired change
that may span repos; a **Bullet** is one repo's vertical slice of an
intent, implemented test-first, yielding one commit and one pull
request. Runs, phases, and worktrees all exist to serve an intent — the
intent is the thing that survives.

There are two ways to work through Sgt, and both produce the same
records:

- **Agent-driven.** Run your own agent CLI (opencode, codex, goose, pi,
  claude) inside a registered repo and let it talk to Sgt over MCP —
  `sgt mcp`, a subcommand of the `sgt` binary. Sgt does not spawn or
  host this session; it only answers the agent's tool calls.
- **Coordinator-driven.** Dispatch work to the running Sgt server through
  `sgt dispatch` or `POST /api/dispatch`, and Sgt runs bounded headless
  agent phases itself, each in an isolated git worktree on its own branch.
  The embedded dashboard currently observes and recovers work; it does not
  contain a dispatch form.

## Installation

Requirements (run `mise run check` after cloning to verify these
resolve on `PATH` — that task is the authoritative source, derived
directly from the engine's own `exec.Command` call sites):

- Go 1.21+ — to build the `sgt` binary (`mise run build`)
- `git` — worktrees, branches, commits
- `bash` — runs each project's configured quality gates
- [`openspec`](https://github.com/Fission-AI/OpenSpec) — a dispatch
  resolves to a change before any run, intent, or worktree exists
- `gh` — opening a pull request
- A supported agent harness on `PATH`: `opencode`, `oc`, `claude`,
  `goose`, `codex`, or `pi`

tmux is explicitly **not** a dependency — Sgt does not use it, drive
it, or require it for any command.

Clone and build:

```bash
git clone https://github.com/callmeradical/sgt
cd sgt
mise run build
```

### Upgrading from a pre-rename ("Sergeant") install

This project was renamed from `Sergeant`/`sergeant` to `Sgt`/`sgt`. If
you already had the old build running, three default paths changed and
there is no automatic migration yet (tracked in
`openspec/changes/sergeant-to-sgt-path-migration/` — a future `sgt
migrate` subcommand). Until that exists, migrate by hand:

| Path | Old | New |
|---|---|---|
| Config dir | `~/.config/sergeant` | `~/.config/sgt` |
| Database | `~/.local/share/sergeant/sergeant.db` (+ `-shm`/`-wal`) | `~/.local/share/sgt/sgt.db` |
| Fleet root | `~/.local/share/sergeant-v2/fleet` | `~/.local/share/sgt-v2/fleet` |

1. Stop the running sergeant/sgt daemon first — confirm nothing is
   mid-run (`GET /api/runs`) before killing it.
2. Move the config dir: `mv ~/.config/sergeant ~/.config/sgt`.
3. Move the database — if `~/.local/share/sgt/sgt.db` already exists
   (e.g. from an earlier trial run of the new binary), back it up
   rather than overwrite it; this is the rename that holds your real
   history: `mkdir -p ~/.local/share/sgt && mv
   ~/.local/share/sergeant/sergeant.db* ~/.local/share/sgt/`.
4. Move the fleet root: `mkdir -p ~/.local/share/sgt-v2 && mv
   ~/.local/share/sergeant-v2/fleet ~/.local/share/sgt-v2/fleet`.
5. Rebuild and relaunch: `mise run build && bin/sgt ui`.

Do not touch `~/.local/share/sergeant/fleet` — that is the old v1
bash toolbelt's own fleet layout, unrelated to the database file that
happens to live in the same parent directory. v1 is not a dependency
of `sgt`.

If you are coming from v1 with no prior v2/"Sergeant" install, none of
this applies — you have none of the old paths above, so just do a
fresh install per Installation above.

## Your first project

A project is one YAML file at `~/.config/sgt/<name>.yaml`. `path` is
an absolute path on disk (`~` expands to `$HOME`) — Sgt does not clone
a repo for you, the path must already exist.

```bash
mkdir -p ~/.config/sgt
cp schema/project.yaml.example ~/.config/sgt/myproject.yaml
# Edit it — set your repo names and absolute paths on disk
```

Then either build and start the dashboard, or launch your own agent
harness inside one of the project's repos and let it talk to Sgt over
MCP (see "What is Sgt" above for both paths):

```bash
bin/sgt ui
```

Once registered, talk to your agent about the project:

```
> load context for myproject
> what repos are in this project?
> go work on myapp-api
> add feature X across all repos
```

The full YAML shape — repos, groups, per-repo and per-group agent
instructions, graphify output — is the Configuration reference below,
which links to the complete schema document rather than repeating it
here.

## Day-to-day workflow

The primary workflow is **intent to reviewed pull request**. "Primary"
describes this shared lifecycle, not a preferred interface: an agent using
MCP and the CLI/API create the same Intent, Bullet, run, phase, and evidence
records, which the dashboard reads.

### 1. Start Sgt and choose the work

Start the long-running engine before using the dashboard, the dispatch CLI,
or the MCP dispatch tools:

```bash
bin/sgt ui
```

Open `http://127.0.0.1:8484`, or launch an MCP-configured agent inside a
registered project repository. State one bounded intent: what should change,
the acceptance criteria, the repositories that own it, and their merge order.
Use one bullet per repository; a bullet is a vertical slice that yields one
commit and one pull request.

Every dispatch must name a work `type` (`feat`, `fix`, `refactor`, `docs`,
`chore`, or `test`) and resolves to an OpenSpec change before a run or
worktree exists. Supply `change_id` when one already exists; otherwise Sgt
derives and scaffolds one from the brief.

### 2. Dispatch the intent

When the target repositories are known, name them explicitly. This is the
normal path and starts execution immediately. Repeated `--repo` flags, or the
MCP `repos` array, become bullet positions in that order, so list repositories
in the intended merge order:

```bash
bin/sgt dispatch \
  --project myproject \
  --type feat \
  --change-id add-google-oauth \
  --repo myapp-api \
  --repo myapp-app \
  --brief "Add Google OAuth"
```

The HTTP API performs the same operation at `POST /api/dispatch`. From an
agent session, call `sgt_dispatch` with the equivalent arguments:

```json
{
  "project": "myproject",
  "brief": "Add Google OAuth",
  "repos": ["myapp-api", "myapp-app"],
  "type": "feat",
  "change_id": "add-google-oauth",
  "request_id": "add-google-oauth-1"
}
```

Use a stable `request_id` when retrying an uncertain request; repeating it
returns the original run instead of starting duplicate work.

If you omit `repos`, Sgt does not guess and execute. It records a proposed
intent spanning every project repository, ordered alphabetically, and starts no
run. Review, approve, or reject it through the API:

```text
GET  /api/plans
POST /api/plans/{intent_id}/approve
POST /api/plans/{intent_id}/reject
```

Approval reuses the exact OpenSpec change and work type recorded on the
proposal; it does not infer them again. The dashboard is currently an
observation and recovery surface: it does not yet expose dispatch or plan
approval controls.

### 3. Let each bullet go red to green

Sgt creates an isolated worktree and branch for each repository bullet, never
mutating your checkout. Each bullet follows the same evidence-producing loop:

1. Render the brief from the stored intent.
2. Record a failing deterministic gate result, or an explicit red-state
   exemption when no natural failing state exists.
3. Implement one test-first vertical slice at a time.
4. Run the configured gates in sorted name order and record the passing result.
5. Record phase output and typed handoff envelopes in Sgt's store.

A failed or timed-out phase is recorded as failed, never passed. A bullet that
cannot proceed becomes blocked with a reason rather than guessing.

### 4. Monitor and recover

Use stored state rather than log scraping:

```bash
bin/sgt runs --project myproject
bin/sgt run-details <run-id>
```

An MCP-connected agent can use `sgt_status`, `sgt_run_status`, or
`sgt_run_wait`. The dashboard shows the same records and live updates.

For a failed, cancelled, timed-out, or interrupted run, read its run details,
fix the underlying cause in the isolated worktree or OpenSpec change, then
resume it from the dashboard or call:

```text
POST /api/run-resume  {"id":"<run-id>"}
```

Resume keeps the existing branch and worktree and skips phases already recorded
as passed. `blocked` is a bullet status, not a resumable run status; when a
bullet is blocked, its underlying run is normally one of the resumable terminal
statuses above. Read the blocked reason, fix it, and resume that run. If the
underlying run is already `passed` — for example, a PR merged into the wrong
base branch — resume is refused. Resolve that delivery mismatch explicitly or
dispatch corrected work; do not retry resume as if it were a transient failure.

### 5. Open, review, and merge pull requests

After a bullet is green, explicitly seal it and open its pull request:

```bash
bin/sgt create-pr \
  --run-id <run-id> \
  --project myproject \
  --repo myapp-api \
  --title "feat: add Google OAuth" \
  --body "Implements add-google-oauth"
```

From an MCP agent, use `sgt_create_pr` for a coordinator-dispatched isolated
worktree. `sgt_seal_pr` is the separate tool for verified work already sitting
in the agent's own current checkout. Sgt refuses to create a PR for a bullet
that is not green.

When sealing makes every bullet in an intent sealed-or-merged, Sgt evaluates the
project's configured shipping gates once and records their outcome on the
intent. This is durable delivery evidence; in the released engine it does not
delay or undo the PR action that triggered it.

Review and merge each PR in the intent's recorded order. Sgt records and displays
that order, but the released engine does not currently enforce that order or
automatically release downstream bullets. The operator must follow it. Sgt can
observe each PR's real merge state, but **Sgt never merges**; the human remains
the final irreversible boundary.

The intent, not an individual run, is the durable completion record. A run
ending or a PR opening does not by itself mean a multi-repository intent is
complete: every bullet must reach its required delivered state.

## Filesystem and database

Sgt's own account of what it works on, and how it did the work, lives in
a small number of well-known places on disk. Knowing them is how an
operator inspects, backs up, or migrates that history directly, without
going through the API — and it is the same account the dashboard, the
CLI, and the MCP surface all read from; none of them is a second source
of truth.

### Directories

| Path | Contents | Override |
|---|---|---|
| `~/.config/sgt/<project>.yaml` | Project definitions — repos, groups, agent instructions, factory gates. One file per project. | none |
| `~/.local/share/sgt/sgt.db` | The SQLite database: every run, phase, envelope, intent, and bullet Sgt has ever recorded. | none |
| `~/.local/share/sgt/artifacts/<run_id>/<phase_id>/` | Durable copies of files a gate or agent phase captured (screenshots, traces), outliving that run's worktree. | `SGT_ARTIFACTS_ROOT` |
| `~/.local/share/sgt-v2/fleet/<run_id>/<repo>/` | The isolated git worktree a dispatched agent phase actually worked in. Reclaimed automatically 7 days after its run goes terminal. | `SGT_FLEET_DIR` |
| `~/.local/share/sgt-v2/sgt-ui.lock` | The single-instance lock `sgt ui` holds for its lifetime — a second invocation refuses to start rather than reconciling a live run out from under the first. | `SGT_UI_LOCK` |

The fleet root and UI lock path still carry the `sgt-v2` segment rather
than `sgt` — that name predates and is unrelated to the later
`Sergeant`→`Sgt` rename (see Installation's upgrade section above); it
is how this engine has distinguished its own data from any legacy
layout since before the rename existed. This is a real, current path,
not a typo.

### Database tables

| Table | What it records |
|---|---|
| `runs` | One row per dispatch — status, brief, work type, OpenSpec change id, and the intent it serves. |
| `phases` | One row per phase attempt within a run — status, duration, and its (redacted) output payload. |
| `envelopes` | One row per typed handoff or notification a phase produced. |
| `intents` | An intent's durable statement of desired change — the primary durable object every run/phase/worktree exists to serve. |
| `bullets` | One row per repo-scoped vertical slice of an intent — status, branch, worktree path, and pull request URL. |
| `changes` | An append-only record of which OpenSpec change each intent was resolved against, and when. |
| `deliveries` | Durable retry state for outbound envelope delivery. |
| `artifacts` | Durable metadata (path, size, content type) for each captured artifact file; a dropped-count row when a phase's artifact cap was exceeded, so a drop is never silent. |
| `retention_rollups` | One row per project — the surviving aggregate counts of runs and bullets that have since been rotated out of the tables above, so historical totals stay accurate after the detailed rows are gone. |
| `export_cursor` | Per-target progress cursor for the read-only task-tracking export. |

## Configuration reference

Every project field — `repos`, `groups`, `agent_instructions`,
`graphify`, gates, and export — is documented in full in
[`docs/schema.md`](../../docs/schema.md), with an annotated example at
`schema/project.yaml.example`. This manual does not repeat that
reference; treat `docs/schema.md` as the source of truth for field
names, types, and defaults.

## Troubleshooting

For stale runs, authentication failures, gate errors, and worktree
cleanup, see [`docs/troubleshooting.md`](../../docs/troubleshooting.md).
Prefer the Sgt API and stored state (`sgt status`, `GET /api/runs`)
over manual process or git surgery, and preserve the exact error before
attempting recovery.

## Command reference

%%LIVE_COMMANDS%%

## MCP tools for agents

%%LIVE_MCP_TOOLS%%
