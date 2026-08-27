---
name: sgt-setup
description: Interactively and idempotently bootstrap a new Sgt (v2) installation or diagnose and repair an existing one. Triggers on "set up Sgt", "install Sgt", "configure Sgt", "repair Sgt", "register a project with Sgt", or similar setup and onboarding requests.
---

# Sgt Setup

Bootstrap or repair a Sgt v2 installation interactively and idempotently.
Orchestrate only the commands documented in `README.md`, `docs/schema.md`, and
`mise.toml`. Surface missing capabilities as separate td issues; do not
substitute undocumented workarounds, and never fall back to v1's `bin/sgt-*`
shell toolbelt or its `~/.config/sergeant/` layout — v2 does not shell out to
v1 (decision D7 in `docs/prd-sgt.md`).

## When to use

Load this skill when the user wants to:
- Clone and build the `sgt` engine for the first time on a machine.
- Register a new project, or add repositories to an existing project's YAML.
- Diagnose and repair a broken or incomplete Sgt installation.
- Verify that an existing installation is working correctly.

Do not load it when the user wants documentation only; read `docs/README.md`
and its linked pages instead. Do not load it when the user is reporting a
runtime problem with an in-flight dispatch or run; use `docs/troubleshooting.md`
directly instead.

## Safety constraints

This skill writes only to Sgt-owned paths:

- `~/.config/sgt/config.yaml` — global config (`dev_root`, `default_identity`)
- `~/.config/sgt/<project>.yaml` — project registry files
- The Sgt checkout's own working tree (via `mise run build`/`mise run install`)

Never write to:

- `~/.config/opencode/`, `~/.opencode/`, or any `opencode.json` in any repository
- `~/.claude/`, `CLAUDE.md` in any repository, or any `.claude/` directory
- `~/.codex/`, `~/.config/codex/`, or any Codex configuration path
- `~/.goose/` or any Goose configuration path
- Any registered repository's files — this skill registers repos, it does not
  modify them
- Any path outside `~/.config/sgt/` unless the user explicitly names it

Do not automatically initialize `openspec`, Graphify, or `no-mistakes` in a
registered repository. Each requires an explicit confirmation prompt before
any command runs. If consent is declined, leave state unchanged and report
what was skipped.

## Checklist maintenance

Maintain a visible, numbered checklist in the terminal output. Before each
step, verify whether it is already complete and skip it without prompting if
it is. After each step completes or is skipped, write a `[ok]` or `[skipped]`
status line. When a phase fails, stop the current run with actionable output
identifying the last completed phase. On the next invocation the checklist
starts over from Phase 1 but skips every phase that already passes
verification; this is how resumability works — not by persisting state
between runs, but by re-checking each phase before acting on it.

## Phase 1: Detect prerequisites

Run `mise run check` if a Sgt checkout is already present — it is the
authoritative, up-to-date prerequisite check (derived directly from this
engine's `exec.Command` call sites). If no checkout exists yet, check
manually for:

Required:
- `git`
- `bash`
- [`openspec`](https://github.com/Fission-AI/OpenSpec) — a dispatch resolves
  to a change before any run, intent, or worktree exists
- `gh` (GitHub CLI) — opening pull requests
- At least one supported agent harness on `PATH`: `opencode`, `oc`, `claude`,
  `goose`, `codex`, or `pi`
- Go 1.21+ — to build the `sgt` binary

Optional (skip, do not fail, if absent):
- `mise` (for `mise run check`, `mise run build`, `mise run install`)

`tmux` is explicitly **not** a dependency — v2 does not use it, drive it, or
require it for any command. Do not install it or check for it.

For each missing required prerequisite, show the install guidance and ask for
explicit consent before running anything:

```
<tool> is not found. See <install guidance>.
Continue checking other prerequisites? [y/N]
```

Do not continue past Phase 1 until all required prerequisites are either
present or the user explicitly accepts the risk of proceeding without them.

## Phase 2: Clone and build

If the Sgt repository is not already cloned:

1. Ask where to place the clone:
   ```
   Where should Sgt be cloned? [e.g. ~/Dev/sgt]
   ```
   Wait for the user's answer. Do not proceed until a destination is provided.

2. Show the exact command and ask for consent:
   ```
   git clone https://github.com/callmeradical/sgt <destination>
   Clone to <destination>? [y/N]
   ```
   Run the command only after the user types `y` or `yes`. Leave the
   filesystem unchanged on any other response.

If `mise` is available, ask before building or installing:

```
Run `mise run build` to build the sgt binary? [y/N]
```

```
Run `mise run install` to symlink sgt onto PATH? [y/N]
```

Run each command only after the user confirms. If `mise` is unavailable or
consent is declined, tell the user to run `go build -o bin/sgt ./cmd/sgt`
manually and verify the result before continuing.

Verify that `sgt` resolves on `PATH` (or that `bin/sgt` exists in the
checkout) before proceeding. Stop the current run if verification fails; the
next run re-checks this phase.

## Phase 3: Global config

Check whether `~/.config/sgt/config.yaml` exists.

- **Missing**: ask the user for a `dev_root` path (the root of their
  development directory, e.g. `~/Dev`), then show a preview and ask for
  confirmation before writing anything:
  ```yaml
  dev_root: <path>
  # default_identity: <github-cli-user>   # optional
  ```
  ```
  Write ~/.config/sgt/config.yaml? [y/N]
  ```
  Write the file only after the user confirms. Leave the filesystem unchanged
  on any other response.
- **Present and valid**: report `[ok]` and note the current `dev_root`.
- **Present and invalid YAML**: report the parse error and stop; do not
  overwrite without a timestamped backup, a diff preview, and explicit
  confirmation.

## Phase 4: Project registration

If a project YAML for the target project already exists at
`~/.config/sgt/<name>.yaml` and the user wants to modify it, skip the
interview below and go directly to Phase 5 (repair/edit existing YAML).

For a new project, interview the user for the fields documented in
`docs/schema.md`. Ask these in order; stop and wait for each answer:

1. Project name (must be a valid filename component; becomes
   `~/.config/sgt/<name>.yaml`)
2. Optional human-readable description
3. Repositories: for each, its `name`, absolute or `dev_root`-relative `path`
   (must already exist on disk — v2 does not clone repos into place), optional
   `url`, optional `group`, optional `role`, optional `agent_instructions`
4. Optional groups, with shared `agent_instructions`
5. Optional Graphify block (`output`, `include_groups`, `exclude_patterns`) —
   only ask if the user wants cross-repo knowledge graph generation

Do not invent repository paths, groups, or instructions the user did not
provide. Refuse to register a repo whose `path` does not exist; report it and
move on to the next repo instead of guessing a path.

Show the assembled YAML in full and ask for confirmation before writing:

```
Write ~/.config/sgt/<name>.yaml? [y/N]
```

Write the file only after the user confirms.

## Phase 5: Repair an existing project

If the target project YAML exists but is invalid or incomplete:

1. Validate it against `docs/schema.md` (required top-level fields: `name`
   matching the filename, `repos`; each repo needs `name` and `path`).
2. Report every violation found — do not silently coerce or drop fields.
3. Propose the minimal diff that fixes the violations, show it, and ask for
   confirmation before writing:
   ```
   Apply this fix to ~/.config/sgt/<name>.yaml? [y/N]
   ```
4. Take a timestamped backup (`<name>.yaml.bak.<timestamp>`) before
   overwriting an existing file that already validated correctly, so an
   unwanted edit can be reverted.

## Phase 6: Verify

1. Start the engine if it is not already running:
   ```
   bin/sgt ui
   ```
   or confirm the user will start it themselves.
2. Once running, verify the registered project is visible:
   ```bash
   curl http://127.0.0.1:8484/api/projects
   ```
3. Report `[ok]` once the project name from Phase 4/5 appears in the
   response. If it does not, report the mismatch and point back to the
   relevant phase — do not guess at a fix.

## Failure table

| Symptom | Likely cause | Action |
|---|---|---|
| `sgt: command not found` | Phase 2 build/install skipped or failed | Re-run Phase 2 |
| `~/.config/sgt/config.yaml` missing `dev_root` | Phase 3 skipped | Re-run Phase 3 |
| Project absent from `/api/projects` | YAML missing, misnamed, or invalid | Re-run Phase 4/5 |
| Repo path in YAML does not exist on disk | v2 does not clone repos automatically | Ask the user to clone it manually, then re-validate |
| `openspec`, `gh`, or an agent harness missing | Prerequisite not installed | Re-run Phase 1; do not substitute a different tool silently |

See `docs/troubleshooting.md` for problems with an already-registered project
or an in-flight dispatch — those are out of scope for this skill.
