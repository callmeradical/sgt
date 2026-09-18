# Design — `docs/schema.md` describes v2, not deleted v1 binaries

## Ownership

One repository, `sgt`. Touches only `docs/schema.md` (prose) and adds one
new test file verifying the fix and guarding against the same class of
staleness recurring silently.

## Investigation before writing any prose

Before editing `docs/schema.md`, confirm each fact against the real
source — do not swap one plausible-sounding name for another without
checking:

1. `name`'s claimed `[A-Za-z0-9._-]+` constraint: `internal/graphify/
   graphify.go` joins a repo's `name` directly into a scratch directory
   path (`filepath.Join(scratch, name)`) with no visible validation in
   that file as of this writing. Grep `internal/config/config.go` and
   `internal/graphify/*.go` for any validation of `Repo.Name` against a
   character-class pattern. If none exists, the doc must say the
   constraint is not currently enforced — do not attribute an unenforced
   claim to any binary.
2. `url`: confirm `config.Repo` (`internal/config/config.go`, currently
   `Name`/`Path`/`Role`/`Group`/`Factory`/`Retries`) has no `url`/`URL`
   field, and confirm no code path clones a missing repo path (a
   `git status`/`git worktree list`-style refusal is `skills/dispatch/
   SKILL.md` Step 2's documented behavior instead).
3. `output` / `exclude_patterns`: read `internal/graphify/graphify.go`'s
   current merge/publish sequence, in particular `filterGraphFile` and
   wherever the merged graph is written to `Graphify.Output`, to describe
   the real symlink-preservation and staging-outside-a-source-repo
   behavior accurately.
4. The review-routing sentence: read `internal/ui/dispatch.go`'s handling
   of the `"review"` stage to name what actually builds/classifies review
   context today.

## The doc edit

Rewrite each affected cell/sentence in `docs/schema.md` per the
Proposal's per-field instructions, citing the actual file (and function,
where one specific function is the real owner) rather than a bare "v2
does this now" without a pointer — the whole point of a canonical
reference is that a reader can go verify the claim.

## Regression test

Add a test (e.g. `internal/config/schema_doc_test.go`) that:

- Reads `docs/schema.md` from disk (relative to the repository root,
  resolved the way other doc-reading tests in this repository already
  do, if a precedent exists — check `internal/ui`'s embedded-doc test
  patterns first) and fails if it contains the literal strings
  `sgt-graphify`, `sgt-sync`, or `sgt-dispatch` — the exact class of
  staleness this change fixes.
- Fails if the doc's `url` row still describes a `config.Repo` field,
  unless `config.Repo` actually gains a `URL`/`Url` field in the future
  (a simple substring check on "Used by `sgt-sync`" plus a reflection
  check that `config.Repo` has no field named `URL` is enough — this
  guards against someone reintroducing the same fictional-field mistake
  either by re-adding the stale prose or genuinely adding a `url` field
  without updating this doc's now-correct description of its absence).

This is deliberately a plain Go test, not a shell script (matching this
repository's current convention — the old `tests/*.sh` suite no longer
exists), and deliberately narrow (three literal strings plus one
structural check) rather than a general-purpose doc linter, which is out
of scope for a single-file prose fix.

## Rejected alternatives

**A general-purpose "no v1 command names anywhere in docs/" linter.**
Rejected: over-broad for this change's scope, and would incorrectly flag
the legitimate historical/comparative references in `docs/architecture.md`
and dated PRDs that this PRD explicitly keeps out of scope. A test scoped
to `docs/schema.md` alone avoids that false-positive class entirely.
