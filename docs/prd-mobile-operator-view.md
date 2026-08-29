# Product Requirements: A Mobile-Friendly Dashboard for On-the-Go Operators

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, R7 (operator surfaces and delivery)

## Summary

Sgt's embedded dashboard is built for a wide desktop viewport — a
fixed-width sidebar, a workflow graph that needs horizontal room, side-
sliding drawers, hover-dependent tooltips. Now that the dashboard is
reachable from anywhere on the operator's tailnet, not just localhost, a
phone is a real, common way it will actually get opened — the exact use
case the current UI was never built for. This PRD is about making the
one existing dashboard usable on a phone: responsive adaptation at each
of the specific places that currently break on a small screen, not a
second, separately maintained page.

## Problem

The current dashboard's core interactions assume a desktop-class screen
and a pointer: a workflow graph that needs horizontal space to lay out
its lanes, drawers that slide in from the side, tooltips that only
appear on hover, dense multi-column tables. None of this translates to
a phone's narrow viewport and touch input — an operator who opens the
dashboard on a phone today gets a cramped, largely unusable rendering of
a page built for something else.

This now matters in practice, not just in principle: the dashboard is
reachable over Tailscale from any device on the operator's tailnet, and
a phone is one of the most likely devices an operator actually reaches
for when they want to check on something without being at their desk.
What they need in that moment — is a run still going, did it pass or
fail, does a bullet need a decision, can I resume or fix a failure right
now — is a small, well-defined set of actions the current UI happens to
also support, but renders far too densely to use comfortably on a small
screen.

## Proposal

- One dashboard, made to respond to a narrow viewport at each of the
  specific places that currently don't: the fixed-width sidebar, the
  workflow graph's horizontal layout, side-sliding drawers, and
  hover-only tooltips. Each of these is a distinct point that needs its
  own small-screen treatment (for example the sidebar collapsing behind
  a toggle, a drawer becoming a full-screen sheet, a tooltip getting a
  tap-to-reveal alternative to hover) — not one wholesale redesign, and
  not a second page maintained in parallel with the first.
- Every action and view that already exists remains reachable on a
  small screen once these specific points adapt — this PRD does not
  narrow what a phone visitor can do relative to what the dashboard
  already offers; it makes what already exists actually usable at a
  phone's width. (Whether some already-dense views, like the workflow
  graph, get a genuinely different small-screen treatment rather than
  just a narrower rendering of the same layout is a `design.md`
  decision informed by which specific components turn out not to adapt
  well by simply resizing.)
- No new frontend framework, no build step, no new third-party JS
  dependency, and no new backend endpoint — this changes how the
  existing dashboard renders at a narrow width, not what it can do or
  how it's served. Same server, same port, same Tailscale exposure as
  today.

## Out of scope

- **Any new backend capability.** Every action the dashboard exposes
  already exists as an API endpoint (dispatch, `/api/run-resume`, the
  forthcoming `/api/run-fix`, plan approval). This PRD changes how the
  existing dashboard renders at a narrow width; it adds no new
  capability to Sgt itself.
- **Native iOS/Android apps, push notifications, or app-store
  distribution of anything.** This remains a web page, reached the same
  way it already is today.
- **User-agent sniffing or explicit device detection.** Responsive
  layout (the browser's own viewport width, via CSS) is what adapts the
  page — Sgt does not need to identify what device is asking.
- **Guaranteeing every feature is equally comfortable on a phone.**
  Composing a multi-repo dispatch or deep-reviewing a diff may remain
  genuinely more awkward on a small screen even once responsive — this
  PRD requires those paths to be usable, not that a phone becomes the
  preferred way to do them.

## Open questions

None blocking. Whether any already-dense view (the workflow graph in
particular) needs a genuinely different small-screen treatment, versus
adapting well enough by simply resizing, is left to `design.md` to
determine per component.
