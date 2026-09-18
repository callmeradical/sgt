# Proposal — `docs/schema.md` describes v2, not deleted v1 binaries

## Repository

One repository: `sgt`.

## Requirements served

PRD: `docs/prd-v2-native-skills-and-docs.md` (rewritten to scope narrowly
to this one file — see its Summary for why the originally-named eight
skill/doc files needed no change).

## Problem

`docs/schema.md`, cited by `docs/README.md` as "the canonical v2 schema
reference," describes project-YAML field behavior in terms of deleted v1
binaries:

- `name`: "For `sgt-graphify`, it must match `[A-Za-z0-9._-]+`..."
- `url`: "Used by `sgt-sync` to clone if path doesn't exist."
- `output`: "`sgt-graphify` preserves the symlink and publishes into its
  target."
- `exclude_patterns`: "Sgt applies them before `graphify extract`, so they
  keep working with current Graphify CLIs that do not accept exclude
  flags."
- Section prose: "`sgt-dispatch` still classifies review routing..."

`sgt-graphify`, `sgt-sync`, and `sgt-dispatch` no longer exist on this
branch. Worse, `config.Repo` (`internal/config/config.go`) has no
`url`/`URL` field at all — the `url` row documents a field and a clone-if-
missing behavior that are both fictional in v2. A canonical reference that
cites dead binaries and a nonexistent field defeats its own purpose.

## Proposal

For each affected fact in `docs/schema.md`, name the real, current v2
mechanism, verified by reading the actual source before writing the new
sentence — do not simply substitute a plausible-sounding v2 name for the
v1 one without confirming it's accurate:

- `name`'s format constraint: grep `internal/graphify` and
  `internal/config` for where (if anywhere) a repo name is actually
  validated against `[A-Za-z0-9._-]+` or an equivalent check. If found,
  cite that code. If genuinely not enforced anywhere today, say so
  honestly (e.g. "not currently validated; used as a filesystem path
  segment under a scratch directory during graph merge — see
  `internal/graphify/graphify.go`") rather than attributing an
  unenforced claim to any binary, dead or alive.
- `url`: remove the row, or replace it with an accurate statement that
  `config.Repo` has no such field and v2 refuses rather than clones a
  missing repo path (see `skills/dispatch/SKILL.md` Step 2: "a non-git or
  missing path is refused rather than cloned").
- `output` and `exclude_patterns`: describe `internal/graphify/
  graphify.go`'s actual native Go behavior (decision D9) — symlink
  preservation, exclude-pattern application before merge, staging outside
  a source repo when `output` lives inside one — without naming
  `sgt-graphify` or any external CLI. Read `graphify.go`'s current
  implementation (in particular around `filterGraphFile` and the
  merge/publish sequence) rather than assuming the old v1-era description
  is otherwise accurate.
- The review-routing sentence: read `internal/ui/dispatch.go`'s handling
  of the `"review"` stage and name what actually builds/classifies that
  context in v2 today, replacing `sgt-dispatch`.

## Out of scope

- Every file the PRD originally named besides `docs/schema.md` — already
  correct; re-verified during PRD revision, not touched by this change.
- Adding new validation/enforcement for `name`'s claimed format
  constraint if it turns out nothing enforces it today. This change
  documents the current, actual state; it does not add missing behavior.
- Any other document. Historical/comparative v1 references elsewhere
  (`docs/architecture.md`'s v1-vs-v2 section, dated audits, archived
  PRDs/openspec changes) are correctly out of scope, per the PRD.
