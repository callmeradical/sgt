# Product Requirements: Run List Filter and Sort

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, decision D8 ("The dashboard is a view of
intents, and it renders the workflow from a...").

## Summary

Add basic filtering (status, repo, work type) and sorting (newest/oldest,
duration) to the dashboard's run list. Today the list has a fixed
`created_at DESC` order and a free-text search box with no status/type
facets.

## Problem

`GET /api/runs` supports only a `project` query param server-side
(`ListRecentRuns`/`ListRunsForProject`, both a fixed `ORDER BY
created_at DESC LIMIT ?`); the dashboard's existing search box filters
client-side over whatever that call already returned. There is no way
to ask "show me only failed runs," "only runs against repo X," or sort
by duration to find the slowest gate.

This was felt directly this session: self-hosting `sgt` to fix its own
bugs produced 18 runs against one project in a few hours, and finding a
specific one meant scanning the list by eye or guessing at search text,
not filtering by the thing that actually distinguished them (status,
repo, outcome).

## Proposal

- Add filter controls to the existing runs panel operating on the
  payload `/api/runs` already returns — no new endpoint needed at
  today's scale: status (`running`/`passed`/`failed`/`blocked`/
  `cancelled`), repo, and work type (`feat`/`fix`/`refactor`/`docs`/
  `chore`/`test`).
- Add a sort control: newest/oldest (already the default order) and by
  duration.
- The existing free-text search box stays, composing with the new
  filters (AND semantics) rather than being replaced by them.
- Filter/sort state is reflected in the URL (query params), so a
  filtered view is shareable/bookmarkable — consistent with D8's "the
  dashboard is a view," not private client-only state that disappears
  on refresh.

## Out of scope

- **Server-side pagination.** `ListRecentRuns`/`ListRunsForProject`
  both take a hard `limit` with no offset, so client-side filtering
  only ever searches within that truncated window, not true full
  history. Real at higher run volumes, but not blocking basic
  filter/sort at today's volumes — worth its own future fix, not
  bundled into this one.
- **Saved filter presets or other per-operator dashboard
  customization.**
- **Filtering the fleet/worktree panel** — a separate view, separate
  concern from the run list.

## Open questions

- Does "sort by duration" need a first-class stored duration field on
  `RunRecord`, or is computing it client-side from phase timestamps on
  each render sufficient?
- At what run count does client-side filtering stop being viable and
  force the pagination gap above to be fixed first? Not urgent now, but
  worth naming a number to watch for rather than discovering it by
  surprise.
