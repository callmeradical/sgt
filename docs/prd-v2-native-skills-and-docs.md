# Product Requirements: v2-Native Skills and Docs

Status: Draft, awaiting explicit human PRD approval

Extends: the v1 shell-toolbelt removal already landed on `v2` this session,
which deliberately left this exact gap for a follow-up (flagged then as
"out of scope for a code-removal pass").

## Summary

This PRD originally targeted nine files believed to still instruct an
operator or agent to run deleted v1 commands. Re-checking the current
repository (`callmeradical/sgt`) found that eight of those nine —
`skills/dispatch/SKILL.md`, `skills/cross-repo-work/SKILL.md`,
`skills/load-project/SKILL.md`, `skills/wiki/SKILL.md`,
`skills/sgt-help/SKILL.md`, `.agents/skills/to-tickets/SKILL.md`,
`docs/troubleshooting.md`, and `schema/project.yaml.example` — already
describe v2's actual surface; whatever produced this repository already
fixed them. `tests/instruction-policy-test.sh`, the ninth, no longer
exists at all — the file this PRD would have updated is gone.

One file the original version of this PRD did not name still has the
problem: `docs/schema.md` — cited by `docs/README.md` as "the canonical
v2 schema reference" — describes several project-YAML fields in terms of
deleted v1 commands, including one field (`url`) that does not exist in
`config.Repo` at all. This PRD now targets that file only.

## Problem

`docs/schema.md` presents three fields' behavior as still driven by v1
binaries that no longer exist:

- The `name` field's format constraint is attributed to `sgt-graphify`.
  The constraint's real owner today, if it is still enforced at all, is
  `internal/graphify`'s native Go implementation (decision D9) — not a
  deleted bash script.
- The `url` field is documented as "Used by `sgt-sync` to clone if path
  doesn't exist" — but `config.Repo` (`internal/config/config.go`) has no
  `url`/`URL` field at all, and no code anywhere clones a missing repo
  path. This describes a field and a behavior that do not exist in v2. A
  missing or non-git path is refused, not cloned (`skills/dispatch/
  SKILL.md`'s Step 2).
- The `output` and `exclude_patterns` fields' descriptions name
  `sgt-graphify` and "current Graphify CLIs that do not accept exclude
  flags" — obsolete framing from before decision D9 made graph building
  native Go (`internal/graphify/graphify.go`); the *behavior* described
  (preserves symlinks, applies excludes before merging, stages outside a
  source repo) is now implemented correctly, just not by anything named
  `sgt-graphify`.
- Separately, `docs/schema.md` states "`sgt-dispatch` still classifies
  review routing" — routing is real and current, but the classifier is
  v2's own dispatch/review code, not the deleted v1 binary.

This is not cosmetic: `docs/schema.md` is documented elsewhere as the
canonical reference for what these fields do, so a reader has no way to
know its command citations are dead without independently checking the
source, which defeats the purpose of a canonical reference.

## Proposal

Rewrite `docs/schema.md`'s field descriptions to name the actual, current
mechanism behind each one:

- `name`: cite `internal/graphify` (D9) if the format constraint is still
  enforced there; if it is not currently enforced anywhere, say so rather
  than attributing it to code that no longer exists.
- `url`: remove this row, or replace it with an accurate statement that
  `config.Repo` has no such field and v2 does not clone a missing repo
  path — whichever the implementer judges clearer; either way, stop
  describing a fictional field and behavior as real.
- `output` / `exclude_patterns`: describe the native `internal/graphify`
  behavior (decision D9) without naming `sgt-graphify` or any external
  CLI.
- The review-routing sentence: name v2's actual dispatch/review mechanism
  instead of `sgt-dispatch`.

## Out of scope

- **The eight files the original version of this PRD named that are
  already correct** (`skills/dispatch/SKILL.md`,
  `skills/cross-repo-work/SKILL.md`, `skills/load-project/SKILL.md`,
  `skills/wiki/SKILL.md`, `skills/sgt-help/SKILL.md`,
  `.agents/skills/to-tickets/SKILL.md`, `docs/troubleshooting.md`,
  `schema/project.yaml.example`). No further action needed on them.
- **`tests/instruction-policy-test.sh`.** No longer exists; there is
  nothing to update.
- **Rewriting historical/dated documents** (`docs/audit-2026-07.md`,
  `docs/adr-oc-inject-deletion.md`, `docs/dead-code-2026-07.md`, PRDs,
  `docs/architecture.md`'s v1-vs-v2 comparison, openspec archived
  changes, research docs). These correctly use v1 command names in
  historical or comparative context, not as current instructions; editing
  them would be revisionist.
- **`AGENTS.md` itself.** Its own v1 command references are explicit
  prohibitions ("Do **not** call `sgt-dispatch`..."), which is already
  correct; this PRD does not revisit it.
- **Adding a new v2 capability to close a gap this rewrite reveals** (for
  example, if `name`'s constraint turns out to be unenforced anywhere and
  someone decides it should be). Documenting the current, actual state
  honestly is this PRD's job; deciding to add missing enforcement is
  separate, future scope.

## Open questions

None blocking.
