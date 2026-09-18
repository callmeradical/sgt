# Proposal — The dashboard adapts to a phone width

## Repository

One repository: `sgt`.

## Requirements served

PRD: `docs/prd-mobile-operator-view.md`.

## Problem

`internal/ui/static/index.html` has exactly one responsive rule today
(`@media (prefers-reduced-motion: reduce)`) plus a handful of Tailwind
breakpoint utility classes applied unevenly:

- `#master-rail` (the "Pipeline runs" list — the primary way an operator
  finds a specific run) is `hidden lg:flex`: below 1024px it disappears
  entirely, with no toggle, button, or alternate way to reach it. Below
  `lg`, an operator has no way to select a run at all.
- `#detail-drawer` is already `w-full sm:w-[30rem]` — it already becomes
  a full-width sheet below 640px. `#terminal-drawer` is already full-width
  (`left-0 right-0`), docked to the bottom, at every width. Neither of
  these two drawers has the problem the PRD assumed all drawers have.
- 34 interactive elements carry a `title=` attribute as their only
  visible-label mechanism (most also carry `aria-label`, so screen
  readers are already fine) — `title=` is a hover tooltip a touch screen
  cannot trigger, so a sighted phone user gets an unlabeled icon.
- `#workflow-graph` has no responsive treatment at all; its lanes render
  at a fixed layout regardless of viewport width.

## Proposal

- Add a mobile nav control (visible only below `lg`, alongside the
  existing header controls) that reveals `#master-rail` as a full-screen
  or near-full-screen overlay panel, with a way to dismiss it back to the
  workflow view — the one real gap, since nothing else stands in for the
  master rail below `lg` today.
- Leave `#detail-drawer` and `#terminal-drawer`'s existing responsive
  behavior as the baseline; only touch them if testing at real phone
  widths (see design.md) surfaces a genuine problem, not because the PRD
  assumed they needed work.
- Give icon-only interactive elements a visible text label (or short
  label revealed by a persistent affordance) below the narrow-viewport
  breakpoint, rather than only their `title=` hover tooltip — resolving
  the tooltip gap without introducing a tap-to-reveal JS mechanism.
- Evaluate `#workflow-graph`'s lanes at real phone widths and give them
  whatever narrow-viewport treatment (horizontal scroll with snap,
  stacked layout, etc.) design.md settles on per-component, per the
  PRD's own open question.
- No new backend endpoint, no new frontend framework, no build step, no
  new third-party JS dependency. Same server, same port, same Tailscale
  exposure as today.

## Out of scope

Per the PRD: native apps, push notifications, app-store distribution;
user-agent sniffing or explicit device detection (CSS/viewport-width
media queries only); guaranteeing every dashboard feature is equally
comfortable on a phone (composing a multi-repo dispatch or deep-reviewing
a diff may remain more awkward on a small screen); any new backend
capability (every action the dashboard exposes already has an endpoint).
