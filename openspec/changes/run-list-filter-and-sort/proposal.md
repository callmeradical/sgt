## Why

The dashboard's run list has a fixed `created_at DESC` order and a
free-text search box with no status/repo/type facets. This surfaced as
a real problem during a self-hosted bug-fix sweep against `sgt` itself:
18 runs accumulated against one project in a few hours, and finding a
specific one meant scanning by eye rather than filtering by the thing
that actually distinguished them (status, repo, outcome). PRD:
`docs/prd-run-list-filter-and-sort.md`.

## What Changes

- Add filter controls to the existing runs panel, operating on the
  payload `GET /api/runs` already returns: status (`running`/`passed`/
  `failed`/`blocked`/`cancelled`), repo, and work type (`feat`/`fix`/
  `refactor`/`docs`/`chore`/`test`).
- Add a sort control: newest/oldest (already the default order) and by
  duration.
- Compose the existing free-text search box with the new filters (AND
  semantics) rather than replacing it.
- Reflect filter/sort state in the URL (query params), so a filtered
  view is shareable/bookmarkable rather than private, ephemeral client
  state that disappears on refresh.
- No changes to `GET /api/runs` itself or to any store method — this is
  entirely a client-side (dashboard) capability over data the API
  already serves.

## Capabilities

### New Capabilities
- `run-list-filter-and-sort`: the dashboard's runs panel supports
  filtering by status, repo, and work type, sorting by recency or
  duration, and reflects that state in the URL — all client-side over
  the existing `/api/runs` payload.

### Modified Capabilities
(none — no existing `openspec/specs/` capability governs the dashboard's
runs panel today; this introduces new scope rather than changing an
existing contract)

## Impact

- **Affected code**: `internal/ui/static/index.html` (the embedded
  dashboard) only. No Go handler, store method, or API shape changes.
- **Out of scope, explicitly** (per the PRD): server-side pagination —
  `ListRecentRuns`/`ListRunsForProject` both take a hard `limit` with no
  offset, so filtering only ever searches within that truncated window,
  not true full history. A known, separate gap; not bundled here.
- **Rebuild required**: the dashboard is served via `//go:embed
  static/*`, so this change requires a rebuild before the new controls
  are visible, per `AGENTS.md`'s existing note on that file.
