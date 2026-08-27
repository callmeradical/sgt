# Product Requirements: Work Analytics Visualizations

Status: Draft, awaiting explicit human PRD approval

Extends: `openspec/specs/work-analytics/spec.md` (existing capability —
this PRD changes how its already-computed data is presented, not what
is computed). Related to issue #11 (bullet merge-status staleness) —
see Problem.

## Summary

Add visual charts to the dashboard's Work Analytics panel: a pie (or
donut) chart for outcome breakdown (passed/failed/blocked/cancelled/
running), and a bar or line chart for work-type breakdown (feat/fix/
refactor/docs/chore/test). Today this panel presents the same data as
plain text rows (`status: count (%)`).

## Problem

`ComputeWorkAnalytics`/`GET /api/analytics` already computes exactly the
aggregate data a chart needs (`ByStatus`, `ByType`, `BulletsMerged`/
`BulletsTotal`), but the dashboard renders it as a flat list of numbers.
This makes it hard to see the *shape* of the data at a glance, and it
was directly implicated in noticing issue #11: the panel currently
reads "Bullets Shipped: Merged 0/18 (0%)" for a project where five real
PRs merged the same session — a discrepancy traced to bullet
merge-status only refreshing when a run's pipeline view is opened
(#11), not to anything wrong with the aggregation math itself.

This PRD does not fix #11. It's worth naming the connection anyway: a
visualization an operator can eyeball against their own memory of what
actually happened surfaces a wrong number faster than a small buried
percentage does — that's part of the motivation, not something this
PRD is responsible for correcting.

## Proposal

- **Pie/donut chart** for outcome breakdown (`ByStatus`), replacing or
  augmenting the current text list.
- **Bar chart** for work-type breakdown (`ByType`) reflecting the
  current point-in-time snapshot `ComputeWorkAnalytics` already
  returns.
- Both charts render entirely from the existing `GET /api/analytics`
  payload — no new fields, no new store method, no schema change.
- Charts respect the same project scoping (a single project, or
  "Global / All projects") the existing text figures already use.
- Colors/labels for run status SHALL reuse the same status vocabulary
  (and ideally the same color coding) the run list and pipeline view
  already use, so an operator isn't learning a second color language
  for the same concept.

## Out of scope

- **Fixing issue #11.** Tracked separately; the "shipped" figure (and
  any chart built on it) inherits whatever #11's eventual fix produces.
- **A true time-series/trend view** (e.g. runs per day/week). The
  work-type chart proposed here is a bar chart over the current
  snapshot, not a line chart trended over time — a real trend view
  would need new time-bucketed query support beyond
  `ComputeWorkAnalytics`'s point-in-time aggregate. See Open Questions.
- **Charting other dashboard panels** (fleet/worktrees, the run list
  itself) — separate concern from work analytics.
- **Choice of charting library** — an implementation decision for
  `design.md`, not a product requirement.

## Open questions

- Do the new charts replace the existing text figures outright, or sit
  alongside them (e.g., chart plus a compact legend with the exact
  numbers)?

## Decisions

- **Work-type chart is a bar chart over the current snapshot, not a
  time-series line chart.** Confirmed 2026-08-27. A real trend view
  (runs per day/week) would need new time-bucketed aggregation beyond
  `ComputeWorkAnalytics` and can be revisited as a separate PRD if
  wanted later.
