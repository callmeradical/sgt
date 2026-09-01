# Proposal — Sgt manual, `sgt help` search, and a dashboard manual drawer

## Repository

One repository: `sgt`.

## Requirements served

PRD: `docs/prd-sgt-help-documentation-search.md`.

## Problem

`sgt help`/`sgt --help`/`sgt -h` (`cmd/sgt/main.go`'s `printUsage`) prints
a fixed five-line subcommand list regardless of what was asked — it
cannot answer "how do I install this" or any other real question.
`skills/sgt-help/SKILL.md` already answers these questions well, but only
inside an agent session that has it loaded. Sgt's real, current
documentation is scattered across a dozen-plus files with no single
start-to-finish manual. The dashboard has no documentation surface at
all.

## Proposal

- Write `internal/manual/manual.md`: a single, hierarchical manual with
  `##`-delimited sections — What is Sgt, Installation, Your first
  project, Day-to-day workflow, Configuration reference, Troubleshooting,
  Command reference, MCP tools for agents. Installation and first-project
  content is consolidated and verified from `README.md`'s existing "Quick
  start" and "Upgrading from a pre-rename (Sergeant) install" sections;
  "What is Sgt" is new content (none exists today); Configuration
  reference and Troubleshooting are short overviews that link to
  `docs/schema.md` and `docs/troubleshooting.md` rather than duplicating
  them, per the PRD's "front door, not a second copy" requirement.
- The `Command reference` and `MCP tools for agents` sections' bodies are
  exactly one live-substitution marker each (`%%LIVE_COMMANDS%%` and
  `%%LIVE_MCP_TOOLS%%`) — never hand-written prose listing command/tool
  names, so they cannot drift from what the binary actually does.
- Embed `manual.md` directly into the `sgt` binary via `//go:embed` in a
  new `internal/manual` package — physically alongside the Go code that
  embeds it (matching `internal/ui/static/index.html`'s existing
  embedding precedent), not copied from a separate `docs/` location at
  build time, so there is exactly one copy, not two that can drift.
  `docs/README.md` links to `internal/manual/manual.md` as the manual's
  location.
- `internal/manual` exposes: parsing into an ordered list of
  `{Title, Body}` sections, live-marker substitution (using
  `cmd/sgt`'s `printUsage` output and `internal/mcp.Tools()`), and a
  search function matching a query against section titles first, then
  section bodies, case-insensitively.
- `sgt help` (no argument): prints the manual's section titles as a table
  of contents, followed by the existing subcommand list.
- `sgt help <topic...>`: joins remaining arguments as the query, searches
  the manual, and either prints the one matching section in full, lists
  every matching section's title when more than one matches (suggesting
  a more specific query), or — no match — says plainly that the manual
  does not cover it and lists the available section titles instead of
  fabricating an answer.
- `GET /api/manual`: returns the parsed, live-substituted sections as
  JSON (`{"sections":[{"title":...,"body":...}, ...]}`) — a plain read,
  no side effects, matching every other read-only endpoint's shape in
  this codebase (e.g. `handleAnalytics`'s precedent from the sibling
  `sergeant-v2` lineage this repo descends from).
- A new dashboard header button opens a "Manual" drawer, following
  `openWorkersDrawer`/`openAnalyticsDrawer`'s existing fetch-then-
  openDrawer pattern exactly: fetch `/api/manual`, render a table of
  contents plus each section's body as preformatted, whitespace-preserved
  text (no markdown-to-HTML rendering, no new JS dependency — consistent
  with this dashboard's existing zero-third-party-JS-dependency
  convention).
- Update `skills/sgt-help/SKILL.md`'s "Documentation map" to name
  `internal/manual/manual.md` (via `sgt help` or `GET /api/manual`) as
  the first thing to check, ahead of the existing per-topic file list —
  the CLI, the dashboard, and the skill now share one reference.

## Out of scope

Per the PRD: exhaustive documentation of every topic; a fuzzy/semantic
search engine or vector index; a hosted documentation site; a new
full-page dashboard view or routing mechanism; surfacing
`docs/archive/v1/` as current; rewriting `skills/sgt-help/SKILL.md`'s
command references beyond the one documentation-map addition above
(already otherwise accurate, confirmed during
`docs/prd-v2-native-skills-and-docs.md`'s implementation).
