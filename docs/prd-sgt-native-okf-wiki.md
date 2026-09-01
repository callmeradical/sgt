# Product Requirements: Sgt Maintains Its Own OKF Wiki of Its Work

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, R7 (operator surfaces and delivery)

## Summary

Sgt's durable record of its own work — runs, phases, intents, bullets,
envelopes — lives only in its SQLite store, reachable through the API,
CLI, and dashboard. There is no plain-markdown, browsable trail of what
Sgt actually did over time. Separately, the operator already maintains a
personal wiki in [Open Knowledge Format](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
(OKF) — a real, currently-active vault at `~/wiki` (a symlink to an
Obsidian vault), currently on OKF v0.1, with a recognizable shape: a root
`index.md` listing dated folders, each dated folder holding its own
`index.md`, a chronological `log.md`, and one page per topic/session.
The specification has since moved to v0.2. This PRD gives Sgt its own
wiki in that same OKF shape, targeting the current v0.2 specification —
but in its own folder, for its own work, not commingled with the
operator's personal vault.

## Problem

An operator (or anyone reviewing Sgt's history) who wants to browse "what
has Sgt actually done" has to query the store via the API, the CLI, or
the dashboard's run list — there is no plain-file record they can open in
a text editor or Obsidian, link between, or search the way they already
do for their personal notes. `skills/wiki/SKILL.md` and
`bin/wiki-daily-digest` already exist, but they target the operator's own
personal wiki (`~/wiki`) and are about general agent-session activity
capture — genuinely different content and a genuinely different audience
from "a durable, browsable trail of Sgt's own dispatched work." Writing
Sgt's own operational history into the same vault as the operator's
personal notes would commingle two things that are not the same thing.

Separately: `wiki-daily-digest` calls an LLM to synthesize a narrative
digest, because its source material (raw, unstructured agent session
transcripts) needs synthesis to become readable. Sgt's own store data is
already structured — a run's brief, repos, outcome, PR links, and any
review findings are already discrete, durable facts, not a transcript
that needs summarizing. Sgt's own wiki should not need an LLM call to
exist.

## Proposal

Sgt maintains its own wiki, in its own folder — never the operator's
personal `~/wiki` — conformant with OKF v0.2: a root `index.md`
(carrying the `okf_version` frontmatter field, per spec the one place
frontmatter on an index file is allowed) listing dated entries, each
date holding its own `index.md`, a chronological `log.md` (date-grouped,
newest first, entries as prose), and one concept page per unit of work,
each carrying at minimum the spec's one always-required frontmatter
field, `type`. Cross-links use the spec's recommended bundle-relative
absolute form (leading `/`, resolved from the bundle root) rather than
relative paths, and nothing in Sgt's own reading of the wiki (if any) may
reject a page for a missing optional field, an unrecognized `type`
value, or a broken link — the spec requires every consumer to tolerate
all three.

- **Content is a deterministic rendering of already-durable facts** — the
  intent/brief, the repo(s) and bullet(s) involved, the outcome
  (passed/failed/blocked/merged), links to any opened pull request, and
  the substance of any review-phase findings — not an LLM-synthesized
  narrative. Sgt already knows these facts; the wiki's job is to make
  them browsable as plain files, not to interpret or summarize them.
- **A wiki entry reflects real, completed (or actively in-flight) work.**
  What specifically triggers a page being written or updated — per
  completed run, per intent, per bullet reaching a terminal state — is an
  implementation decision for OpenSpec's `design.md`, not fixed here.
- **This is additive, not a replacement.** The store remains Sgt's
  authoritative record; the wiki is a derived, human-browsable view of
  it, the same relationship the dashboard already has to the store.

## Out of scope

- **Writing into the operator's personal `~/wiki` vault.** Sgt's own
  wiki lives in its own, separate location. This PRD does not change
  `skills/wiki/SKILL.md` or `bin/wiki-daily-digest`'s existing behavior.
- **LLM-synthesized narrative content.** Per the Problem above, Sgt's
  wiki renders already-structured facts; it does not call a model to
  write prose about them.
- **A two-way Obsidian plugin, live sync, or any Obsidian-specific
  dependency.** The output is plain markdown files in the OKF shape;
  that they happen to be readable in Obsidian is a property of the
  format, not a dependency Sgt takes on.
- **Backfilling historical runs that predate this feature.** Scope is
  runs/work going forward from when this ships, unless a later PRD
  decides backfill is worth doing separately.
- **Any change to what data Sgt records.** This PRD only adds a rendering
  of facts that already exist in the store.

## Open questions

- **Resolved: one wiki per project**, matching how every other Sgt view
  (dashboard, analytics, run list) already scopes by project.
- **Exact root location** (e.g. under Sgt's own `~/.local/share/sgt/`
  data root, keyed by project name, versus something rooted in the
  project's own configured repositories) — an implementation decision
  for `design.md`.
