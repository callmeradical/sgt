# Project YAML Schema

A sgt project lives at `~/.config/sgt/<name>.yaml`. The filename (without extension) is the project identifier used by all `sgt-*` commands.

---

## Global config

`~/.config/sgt/config.yaml` holds machine-wide settings:

```yaml
dev_root: ~/Dev   # root of your development directory

# Optional. Default GitHub CLI identity for all dispatches.
# Overridden by project-level or repo-level identity fields.
# default_identity: callmeradical
```

All scripts read `dev_root` at startup. Repo `path` values that are not absolute (`/...`) or home-relative (`~/...`) are resolved relative to `dev_root`. This makes project YAMLs portable across machines — change `dev_root` in one place instead of every path in every YAML.

Durable callback implementations are executable profiles under
`~/.config/sgt/callbacks/`; they are not project YAML fields and fleet
requests cannot supply paths. See [Durable Callback Protocol](callbacks.md) for
the profile ownership/mode rules and versioned consumer schema.

**Path resolution examples** (with `dev_root: ~/Dev`):

| YAML path | Resolved to |
|---|---|
| `smith/ascend-arch-smith` | `~/Dev/smith/ascend-arch-smith` |
| `~/Dev/smith/ascend-arch-smith` | `~/Dev/smith/ascend-arch-smith` (unchanged) |
| `/opt/repos/myapp` | `/opt/repos/myapp` (unchanged) |

---

## Top-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Project identifier. Must match the filename. |
| `description` | string | no | Human-readable description of the project. |
| `repos` | list | yes | Ordered list of repositories in this project. |
| `groups` | map | no | Logical groupings of repos, with shared instructions and optional descriptions that Sgt can use for review routing. |
| `graphify` | map | no | Configuration for cross-repo knowledge graph generation. |
| `defaults` | map | no | Default values applied to every repo. |
| `identity` | string | no | GitHub CLI user for `gh auth switch` before dispatching. Overrides `config.default_identity`. Per-repo `identity` overrides this. |

---

## `repos[]` fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Short identifier for this repo. Used in output and context blocks. Not currently validated against any character-class pattern in `internal/config` or `internal/graphify` — it is used as a project-scoped repo identifier (a map key in `config.Project.Repos`) and, during a graph build, joined directly into a scratch directory path (`internal/graphify/graphify.go`, `filepath.Join(scratch, name)`) with no character check applied there today. |
| `path` | string | yes | Path on disk. Absolute (`/...`) and home-relative (`~/...`) paths pass through. Relative paths are resolved from `dev_root` in `config.yaml`. |
| `group` | string | no | Group name this repo belongs to. Must match a key in `groups`. |
| `role` | string | no | Human description of this repo's role in the project. Sgt includes it in worker context and review routing. |
| `agent_instructions` | string | no | Instructions injected into agent context when working in this repo. Overrides group-level instructions for the same repo and participates in merged review-routing context. |
| `identity` | string | no | GitHub CLI user for `gh auth switch` before dispatching this repo. Overrides project-level `identity` and `config.default_identity`. Resolution order: `repo.identity` → `project.identity` → `config.default_identity` → no-op. |

`config.Repo` (`internal/config/config.go`) has no `url`/`URL` field. There is
no clone-if-missing behavior for a repo's `path`: `internal/dag/engine.go`'s
`prepareWorktree` refuses to dispatch agents into a repo whose `path` is not
already a git repository (a missing path fails the same check) rather than
cloning one into place.

---

## `groups` fields

Each key under `groups` is a group name. Value is a map with:

| Field | Type | Required | Description |
|---|---|---|---|
| `description` | string | no | Human-readable description of this group. Sgt also includes it when classifying UI-facing work for accessibility review routing. |
| `agent_instructions` | string | no | Instructions inherited by all repos in this group. Repo-level `agent_instructions` override this, and the merged instructions participate in review routing. |

---

## `graphify` fields

| Field | Type | Required | Description |
|---|---|---|---|
| `output` | string | yes | Published output directory for the merged project graph. A trailing `/` is allowed. `internal/graphify/graphify.go`'s `BuildProjectGraph` builds the graph in a scratch directory first and only touches `output` once the merged graph is verified non-empty: any existing directory at `output` is moved aside to a timestamped backup, the scratch directory is renamed into `output`'s place, and the backup is removed once that rename succeeds (or restored on failure). This is a plain directory replace — it does not special-case a directory symlink at `output`, and it does not preserve any existing `wiki/` or `memory/` subdirectories underneath it. |
| `include_groups` | list | no | Only graph repos belonging to these groups. Default: all repos. |
| `exclude_patterns` | list | no | Glob patterns excluding files from the published graph (gitignore-style: `*` matches within one path segment, `**` matches across any number of segments), matched against each node's, link's, and hyperedge's `source_file`. Applied by `internal/graphify/graphify.go`'s `filterGraphFile` to the merged `graph.json`, after the per-repo graphs are merged and before `output` is published — not before extraction, and not via flags to the external `graphify` binary that `BuildProjectGraph` still shells out to for `extract`/`merge-graphs` (decision D9). |

---

## `defaults` fields

| Field | Type | Description |
|---|---|---|
| `agent_instructions` | string | Baseline instructions for every repo. Applied first; group and repo levels override, and the merged instructions participate in review routing. |

---

## Instruction layering

Agent instruction prose is concatenated in this order:

1. `defaults.agent_instructions` — applies to all repos
2. `groups.<group>.agent_instructions` — applies to all repos in that group
3. `repos[].agent_instructions` — applies to a specific repo

`sgt-context` emits every nonempty layer in one block. Later layers appear later
in the block; when directives conflict, the later repository-specific directive
is the intended authority. Sgt does not structurally merge or deduplicate
free-form instruction prose. The `review` stage does not classify or route
using any of these layers: `internal/dag/engine.go`'s `reviewPrompt` builds
the review agent's prompt from only the diff, the stage, and the repo name,
deliberately excluding role, group, and instruction context so an independent
review cannot see the implementing agent's own reasoning. When a review phase
reports an `error`-severity finding, `internal/ui/dispatch.go`'s
`blockedReasonForRun` reads it back from that phase's envelope to explain why
the run's bullet is blocked.

---

## Path resolution

1. Absolute paths (`/...`) — used as-is.
2. Home-relative paths (`~/...`) — `~` expanded to `$HOME`.
3. Relative paths — resolved from `dev_root` (`~/.config/sgt/config.yaml`). Default `dev_root` is `~/Dev` if no config exists.

Use relative paths in project YAMLs for portability. Use absolute paths when a repo lives outside your `dev_root`.
