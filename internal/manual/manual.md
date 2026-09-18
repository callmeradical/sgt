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
- **Coordinator-driven.** Dispatch work from the embedded dashboard
  (`POST /api/dispatch`) and Sgt runs bounded headless agent phases
  itself, each in an isolated git worktree on its own branch.

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

Once a project is registered, the loop is the same regardless of which
of the two entry points (agent-driven or coordinator-driven) you use:

1. State or update the intent — the durable statement of what should
   change, potentially spanning repos.
2. Work proceeds per repo (a bullet), in an isolated git worktree on
   its own branch, never touching your own checkout.
3. Code gates run deterministically, in sorted name order, before any
   phase can be recorded as passed.
4. A bullet that passes its gates is sealed into a pull request; the
   intent tracks merge order across its bullets when more than one
   repo is involved.

Check on things without reading logs:

- `sgt status` — recent factory runs and phase states from the CLI.
- The embedded dashboard (`bin/sgt ui`, `http://127.0.0.1:8484`) —
  runs, worktrees, work analytics, and this manual, all read from the
  same durable store.
- The `sgt_status` / `sgt_run_status` / `sgt_run_wait` MCP tools, from
  inside an agent session — see MCP tools for agents below.

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
