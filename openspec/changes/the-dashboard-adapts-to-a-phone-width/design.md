# Design — The dashboard adapts to a phone width

## Ownership

One repository, `sgt`. Touches only `internal/ui/static/index.html`
(markup, Tailwind utility classes, and the small amount of new JS needed
to toggle the new mobile rail panel). No Go code changes — every action
already has a backend endpoint.

## Current state (verified by reading the file, not assumed)

- `#master-rail` (the "Pipeline runs" list, `internal/ui/static/
  index.html` ~line 78): `class="hidden lg:flex w-72 ..."`. Below 1024px
  it renders nothing and there is no button, toggle, or alternate path to
  it anywhere in the page. This is the one real gap.
- `#detail-drawer`: `class="fixed top-11 right-0 bottom-6 w-full
  sm:w-[30rem] ..."` — already a full-width sheet below 640px. No change
  needed.
- `#terminal-drawer`: `class="fixed left-0 right-0 bottom-0 h-[55vh] ..."`
  — already full-width at every viewport. No change needed.
- `#workflow-graph`: `class="flex-1 border ... overflow-auto min-h-0
  p-3"` — no responsive treatment; content is rendered by
  `renderWorkflowGraph()`/lane-building JS in the same file.
- 34 elements carry `title="..."` as their only visible-label mechanism;
  most (all header/footer icon buttons) already also carry `aria-label`,
  so this is a sighted-touch-user gap, not a screen-reader gap.

## Mobile rail toggle

Add one new button to `<header>`, visible only below `lg` (matching
`#master-rail`'s own existing breakpoint, so the two states are always
consistent — never "hidden with no way to reveal it" and never "a toggle
button with nothing to toggle"):

```html
<button type="button" id="btn-toggle-rail" onclick="toggleMobileRail()"
  aria-label="Pipeline runs" aria-expanded="false" aria-controls="master-rail"
  class="lg:hidden min-w-[44px] min-h-[44px] ...">
  <i class="fa-solid fa-bars" aria-hidden="true"></i>
</button>
```

`#master-rail`'s class changes from `hidden lg:flex` to a state driven by
a new `mobile-rail-open` class (or equivalent — exact mechanism is an
implementation choice) that, below `lg`, makes it a fixed overlay
(`fixed inset-x-0 top-11 bottom-0 z-30` or similar) covering the main
canvas — not pushing it — so the workflow graph underneath is not
reflowed by opening/closing the rail. `toggleMobileRail()` flips this
state and `aria-expanded`; selecting a run from the rail while it is open
closes it back to the workflow view (a run selection is the rail's whole
purpose — staying open after one is made would just be another modal the
operator has to dismiss). At `lg` and above, this new state must never
activate — the rail keeps its current always-visible desktop behavior
untouched.

## Visible labels below `lg`

The header's icon-only buttons (Manual, Work analytics, Worktrees,
Refresh) and the run-header's icon buttons (Run detail, Activity, Stop
run — Run detail and Activity already carry visible text; only Stop run
is icon-only) gain a visible text label alongside the icon below `lg`,
using the same breakpoint as the rail toggle so the whole header adapts
at one consistent point rather than several different ones. Above `lg`,
they keep today's icon-only-plus-`title=`-tooltip behavior unchanged — a
mouse-driven desktop session is not the problem being solved here. Do not
solve this with a tap-to-reveal JS tooltip mechanism (unnecessary
complexity when a visible label is simpler and works everywhere,
including for a keyboard/screen-reader user who currently only reaches
the icon via its `aria-label`).

## Workflow graph at narrow widths

`#workflow-graph`'s lane-rendering JS is read first, then evaluated at
real phone widths (a phone-sized browser window or device emulation, not
just a narrower desktop window) to decide, per lane/component, whether
`overflow-auto`'s existing horizontal scroll is sufficient or a
lane/component needs its own narrow-width treatment (stacking, a
horizontal-scroll-with-snap container, etc.). This is deliberately left
to implementation judgment per the PRD's own open question — the
requirement is that every lane's content remains reachable and readable
at a phone width, not a specific layout mechanism.

## Rejected alternatives

**A separate mobile page/route.** Rejected per the PRD directly: one
dashboard, adapted at specific breakpoints, not a second page maintained
in parallel.

**Detecting touch capability via `@media (hover: none)` instead of the
existing `lg`/`sm` viewport-width breakpoints.** Rejected: the PRD's
philosophy and the file's one existing convention are both viewport-width
based; introducing a second detection axis (interaction capability) for
only the label-visibility piece would make the header adapt at a
different, inconsistent point than the rail toggle it sits next to.

**A JS tap-to-reveal tooltip component.** Rejected: adds a new
interaction pattern and its own accessibility surface (focus handling,
dismiss-on-outside-tap, ARIA) to solve a problem a plain visible label
already solves with no JS at all.
