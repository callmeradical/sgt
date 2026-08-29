# Product Requirements: Sgt Has a Real Manual, Reachable from `sgt help` and the Dashboard

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, R7 (operator surfaces and delivery)

Sequencing: resolved. `docs/prd-v2-native-skills-and-docs.md` shipped
first (`docs/schema.md` was its only remaining stale file, fixed in
commit `6594e31`); `skills/sgt-help/SKILL.md` was already accurate.

## Summary

`sgt help` (and `sgt --help`/`sgt -h`) exists today, but it only prints a
static five-line list of subcommand names (`cmd/sgt/main.go`'s
`printUsage`). It cannot answer "how do I install this," "how do I run a
project," "how does dispatch work," or any other real question — an end
user typing the one command they already know to run for help gets a
menu, not an answer. Separately, `skills/sgt-help/SKILL.md` already does
real work answering exactly these kinds of questions for an agent that
has it loaded, but a person running the bare `sgt` binary gets no benefit
from it, and Sgt's documentation itself is scattered across a dozen-plus
separate files (`docs/architecture.md`, `docs/schema.md`,
`docs/troubleshooting.md`, `AGENTS.md`, `skills/*/SKILL.md`) with no
single, coherent, start-to-finish manual the way (for example) Omarchy's
manual (omarchy.org/manual) is one navigable document covering
installation through advanced configuration. This PRD gives Sgt a real
manual, reachable three ways from the same source: `sgt help`, the
existing embedded dashboard (already has a drawer pattern for secondary
views — Workers, Work analytics — that this fits directly), and the
agent-facing `skills/sgt-help/SKILL.md`.

## Problem

Two related gaps:

1. **No single, coherent manual exists.** Sgt's real, current
   documentation is genuinely useful but lives as independent files, each
   written for a different purpose and audience, with no one place that
   reads start-to-finish the way a manual does — install, first project,
   day-to-day workflow, configuration, troubleshooting, in order. A person
   new to Sgt has to already know which of a dozen files answers their
   question before they can find the answer.
2. **`sgt help` cannot answer anything.** It does not accept a topic or
   question and does not read `docs/`, `AGENTS.md`, or `skills/` at all —
   its output is fixed regardless of what was asked. `skills/sgt-help/
   SKILL.md` already defines a documentation map and query procedure that
   solves a version of this problem, but only inside an agent session
   that has it loaded; there is also no guarantee today that the skill
   and any future CLI search would agree, since nothing ties them to one
   shared reference.

Compounding both: `docs/README.md`'s own "Not yet written for v2" list
claims no v2-native install/quick-start guide exists — but accurate
install and first-project content already exists in `README.md`'s own
"Quick start" and "Upgrading from a pre-rename (Sergeant) install"
sections; it is simply not yet part of a coherent manual alongside the
rest, and `docs/README.md`'s index has not been updated to point to it.
The real gap for those core topics is consolidation and verification, not
authoring from nothing — though other topics a manual should cover (for
example "what is Sgt" framing) may have no accurate content anywhere yet.
A manual with an empty or wrong first chapter, or a help command that has
to lie about that gap, fails at its basic job.

## Proposal

- **A single, hierarchical Sgt manual** (its exact location and format —
  one file or a directory of numbered sections — is an implementation
  decision, not this PRD's) that reads start-to-finish the way a real
  manual does: installation, first project, day-to-day workflow/process
  (dispatch, review, delivery), configuration reference, troubleshooting.
  Built primarily by organizing and cross-linking content that already
  exists (`docs/architecture.md`, `docs/schema.md`,
  `docs/troubleshooting.md`, `AGENTS.md`, `skills/*/SKILL.md`) into one
  coherent read rather than duplicating it — the manual is the front
  door, not a second copy of everything behind it.
- **Installation and first-project content is consolidated from
  `README.md`'s existing "Quick start" and "Upgrading from a pre-rename
  (Sergeant) install" sections**, verified accurate, and folded into the
  manual rather than left as a separate copy `docs/README.md`'s own index
  doesn't point to. Where a manual chapter genuinely has no accurate
  content anywhere in the repository (for example, a "what is Sgt"
  overview), this PRD requires writing that content as part of making the
  manual real — bounded to what's needed for the manual's core path, not
  a commitment to document every possible topic exhaustively.
- **`sgt help` takes a topic or question** and answers from the manual —
  either showing the relevant section directly or naming the specific
  section that answers it — instead of always printing the same static
  subcommand list regardless of what was asked.
- **The manual is also reachable from the dashboard**, following the
  embedded UI's existing drawer pattern (the same shape as its Workers
  and Work analytics drawers) rather than introducing a new page-routing
  concept. A person working in the dashboard should not have to drop to
  a terminal to read the same manual `sgt help` answers from.
- **Prefer live, authoritative sources over static prose wherever both
  exist for the same fact.** Where a fact is also directly observable
  from the running system — the actual subcommand list, the actual MCP
  tool list, the actual HTTP route table — the manual and `sgt help`
  must not let a hand-maintained description of it silently drift out of
  sync with what the software actually does. `skills/sgt-help/SKILL.md`
  already applies this principle for CLI behavior questions ("trust
  tested released behavior over documentation"); this PRD makes it
  explicit and central, not incidental, and extends it to the manual
  itself wherever a section describes something the running system can
  state more authoritatively than prose can.
- **`sgt help`, the dashboard's manual drawer, and `skills/sgt-help/
  SKILL.md` all share the same manual as their reference**, so the CLI,
  the UI, and the agent-facing skill answer consistently rather than
  maintaining three independently drifting understandings of where each
  answer lives.
- **Honest gaps, never fabrication.** Where no good answer exists after
  the manual is built, `sgt help` says so and points to the closest
  existing document — mirroring `skills/sgt-help/SKILL.md`'s existing
  failure behavior. It must never invent an install step, command, or
  behavior the documentation does not actually describe.

## Out of scope

- **Exhaustive documentation of every feature or topic.** The manual's
  bar is a genuinely useful, coherent core path (install through
  day-to-day workflow through troubleshooting), not maximal coverage —
  closer to Omarchy's own "zero bloat here: just everything I use" than
  to documenting every edge case.
- **Rewriting `skills/sgt-help/SKILL.md`'s command references.** Already
  resolved: `docs/prd-v2-native-skills-and-docs.md` shipped first per the
  agreed sequencing, and its investigation found `skills/sgt-help/
  SKILL.md` already accurate — the one remaining stale file was
  `docs/schema.md`, fixed separately. Nothing left for this PRD to avoid
  touching here.
- **A new full-page dashboard view, or any new page-routing mechanism.**
  The manual drawer reuses the existing drawer pattern exactly — this PRD
  does not add a second way to navigate the dashboard.
- **A fuzzy/semantic search engine, embeddings, or a vector index.** Sgt's
  documentation corpus is small; exact and keyword matching over real,
  current content (including the manual once it exists) is sufficient at
  this scale. No new search-infrastructure dependency is warranted.
- **A hosted or exported documentation site** (unlike Omarchy's manual,
  which is hosted at omarchy.org). This is a local CLI capability,
  matching Sgt's single-user, local-first design throughout.
- **Ever surfacing archived v1 documentation** (`docs/archive/v1/`) as if
  it described current behavior.

## Open questions

None blocking. Sequencing with `docs/prd-v2-native-skills-and-docs.md` is
resolved above.
