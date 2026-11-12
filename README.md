# Sgt

A project-aware first mate for working across multi-repo projects.

## Genesis

Sgt was directly inspired by [firstmate](https://github.com/kunchenguid/firstmate) — an agent distro for running a crew of autonomous agents. Firstmate showed that the right unit of distribution is not a CLI tool or an MCP server, but a cloned directory of instructions, skills, and conventions that turns a general-purpose agent into a specialist.

Sgt takes that idea and narrows the focus: instead of orchestrating a crew of agents across arbitrary tasks, it starts with the project topology. A project is a named collection of repositories. Everything — context, instructions, dispatch, graphify output — flows from that definition. Where firstmate asks "how do I run a crew?", Sgt asks "what does this project look like, and how do I work across all of it?"

If you want a general-purpose multi-agent crew orchestrator, use firstmate. If you want your agent to deeply understand your specific projects, their repos, and how they relate — use Sgt.

---

## What it is

You have a project. It has four repos: an API, a frontend, an infra chart, and a shared library. You open your agent and start working — but the agent has no idea these repos are related, what tooling each uses, or which one needs to change first when you add a new feature.

Sgt fixes that. It is a Go-native engine (`sgt`) plus an
`AGENTS.md` and skills that turn a general-purpose agent into a
project-aware first mate. Either dispatch work from its embedded
dashboard, or launch your own agent harness inside a registered
project and let it talk to Sgt directly — either way, Sgt
knows your projects, their repos, how they group, and what instructions
apply to each one.

## Mental model

```
~/.config/sgt/           ← project registry (one YAML per project)
  smith.yaml
  myapp.yaml

~/Dev/smith/                  ← your repos
  smith-api/
  smith-app/
  smith-infra/

sgt/                     ← this checkout (you are here)
  AGENTS.md
  skills/                     ← agent-loaded skills
  cmd/sgt/               ← the engine binary
```

Each project is a YAML file. That file defines which repos belong to it, how they group, where Sgt publishes the merged graphify output, and what agent instructions apply — per group and per repo.

## Quick start

```bash
git clone https://github.com/callmeradical/sgt
cd sgt

# Register a project
mkdir -p ~/.config/sgt
cp schema/project.yaml.example ~/.config/sgt/myproject.yaml
# Edit it — set your repo names and absolute paths on disk

# Build and start the engine, opening its dashboard, or launch your own
# agent harness inside a repo and let it talk to Sgt over MCP —
# see AGENTS.md's "Two ways in" for both paths.
mise run build
bin/sgt ui
```

Then talk to it:

```
> load context for myproject
> what repos are in this project?
> go work on smith-api
> add feature X across all repos
```

## Upgrading from a pre-rename ("Sergeant") install

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

```bash
# 1. Stop the running sergeant/sgt daemon first — confirm nothing is
#    mid-run (GET /api/runs) before killing it.

# 2. Config dir
mv ~/.config/sergeant ~/.config/sgt

# 3. Database — if ~/.local/share/sgt/sgt.db already exists (e.g. from
#    an earlier trial run of the new binary), back it up rather than
#    overwrite it; this rename is the one that matters, it holds your
#    real history.
mkdir -p ~/.local/share/sgt
mv ~/.local/share/sergeant/sergeant.db*   ~/.local/share/sgt/    # sgt.db, -shm, -wal

# 4. Fleet root
mkdir -p ~/.local/share/sgt-v2
mv ~/.local/share/sergeant-v2/fleet ~/.local/share/sgt-v2/fleet

# 5. Rebuild and relaunch
mise run build
bin/sgt ui
```

**Do not touch `~/.local/share/sergeant/fleet`** — that's v1's own
fleet layout (the bash toolbelt), unrelated to the database file that
happens to live in the same parent directory. v1 is not a dependency
of `sgt` and none of its paths are part of this migration.

If you're coming from v1 with no prior v2/"Sergeant" install, none of
this applies to you — you have none of the old paths above, so you're
just doing a fresh install via Quick start.

## Documentation

Start with the [documentation index](docs/README.md), which is the
current, maintained map of what's v2-native versus historical. In brief:

- [Architecture overview](docs/architecture.md)
- [PRD: Sgt v2](docs/prd-sgt.md) — binding requirements and settled decisions
- [Repo-scoped worker skills](docs/repo-scoped-skills.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Project YAML schema](docs/schema.md)

v1's usage docs (what Sgt is, getting started, skill sources, using
Sgt) described the removed `sgt-*` shell toolbelt throughout and have
been archived to [`docs/archive/v1/`](docs/archive/v1/) rather than left
live and stale — see `docs/README.md`'s "Not yet written for v2" section.

## Project YAML

Projects live at `~/.config/sgt/<name>.yaml`. `path` is an absolute
path on disk (`~` expands to `$HOME`); v2 does not clone a repo for you —
the path must already exist.

```yaml
name: myapp
description: My SaaS — Go API, SvelteKit frontend, Helm infra.

repos:
  - name: myapp-api
    path: ~/Dev/myapp/myapp-api
    url: git@github.com:myorg/myapp-api.git
    group: backend
    role: Go REST API
    agent_instructions: |
      Go 1.22. Run `go test ./...` before committing.

  - name: myapp-app
    path: ~/Dev/myapp/myapp-app
    url: git@github.com:myorg/myapp-app.git
    group: frontend
    role: SvelteKit frontend

groups:
  backend:
    agent_instructions: |
      All Go. Use golangci-lint.
  frontend:
    agent_instructions: |
      All SvelteKit. Package manager: pnpm.

graphify:
  output: myapp/graphify-out
  include_groups: [backend, frontend]
```

Full schema reference: `docs/schema.md`. Annotated example: `schema/project.yaml.example`.

## Skills

Agent-loaded skills for structured workflows:

| Skill | What it does |
|---|---|
| `skills/load-project` | Load and internalize full project context |
| `skills/cross-repo-work` | Plan and execute changes across multiple repos |
| `skills/dispatch` | Dispatch subagents per repo with worktrees + briefs |

## Requirements

Run `mise run check` after cloning to verify all of the following resolve
on `PATH` — that task is the authoritative, up-to-date source (derived
directly from this engine's own `exec.Command` call sites), so treat it,
not this list, as ground truth if the two ever disagree:

- Go 1.21+ — to build the `sgt` binary (`mise run build`)
- `git` — worktrees, branches, commits
- `bash` — runs each project's configured quality gates
- [`openspec`](https://github.com/Fission-AI/OpenSpec) — a dispatch resolves to a change before any run, intent, or worktree exists
- `gh` — opening a pull request
- A supported agent harness on `PATH`: `claude` (the default), `opencode`, `oc`, `goose`, `codex`, `pi`, or `copilot` (see Supported agents below)
- With `claude`, the gauntlet plugin, unless the project sets `defaults.gauntlet_roles: false` (see below)

tmux is explicitly **not** a dependency — v2 does not use it, drive it, or
require it for any command.

`mise run check` itself uses `python3` to read claude's plugin list; without
it, the check warns that it could not check the gauntlet plugin.

### Agent harness and gauntlet roles

The default agent harness is claude. A dispatch or project that names no
harness runs `claude`; to use another, pin it in the project YAML with
`defaults.agent` (for example `agent: opencode` under `defaults:`). A missing
harness fails with a reason that names it.

When the harness is `claude`, phases run as the roles of the
[gauntlet](https://github.com/callmeradical/gauntlet) plugin:

- `build` and `fix` run as `gauntlet:alpha`, the test-first builder;
- `review` runs as `gauntlet:beta`, the fresh-context critic, which judges the
  run's diff against the intent and ends with `VERDICT: PASS` or
  `VERDICT: HOLD: <gap>`. A HOLD fails the review and blocks the bullet with the
  gap as its reason;
- other phases, such as `plan`, run with no role;
- a repo that lists no `pipeline` gets `review` added after `test`
  (`plan, build, test, review`); a listed pipeline runs exactly as listed.

Install the plugin inside claude, at user scope:

```
/plugin install gauntlet@gauntlet
```

Dispatch, plan approval, resume, run-fix and `sgt run` refuse a project with
roles on, before creating anything, when claude reports no enabled gauntlet
plugin. `mise run check` warns about the same. To opt a project out:

```yaml
defaults:
  gauntlet_roles: false
```

Roles never apply to other harnesses. `sgt help gauntlet_roles` prints the
manual's full description.

### Supported agents

| Harness | Default | Gauntlet roles |
|---|---|---|
| `claude` | default | on by default; the only harness that runs gauntlet roles |
| `opencode` | — | never |
| `oc` | — | never (the `opencode` CLI under its short name) |
| `goose` | — | never |
| `codex` | — | never |
| `pi` | — | never |
| `copilot` | — | never |

Every harness, `claude` included, gets this behaviour, by design:

- **Every gate in a repo re-runs after a fix.** A gate or review counts only if
  it passed after the repo's last code change, and a run is recorded `passed`
  only when every required phase passes by that rule.
- **Reviews see the run's whole diff** from its base branch: committed,
  uncommitted and untracked work. A very large diff is cut to a
  harness-specific limit, with a marker naming it.
- **A review with a blocking finding is recorded `failed`**, so resume runs it
  again rather than skipping it.
- **A blocked bullet shows its failed phase's own error** when no agent or
  review gave a reason, including up to 300 characters of redacted gate output
  for a failed gate.
- **`sgt run` follows the dispatch path:** it commits output, records delivery,
  refuses a project with no DAG stages, and exits 0 only on `passed`.

`sgt help "Supported agents"` prints the manual's version of this list.

## License

MIT
