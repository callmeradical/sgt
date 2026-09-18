# Tasks — The dashboard adapts to a phone width

One repository, `sgt`, one file (`internal/ui/static/index.html`), so one
task.

## Task 1 — Mobile rail toggle, visible labels below `lg`, workflow graph at phone widths

Repository: `sgt`. Depends on: nothing. Read
`openspec/changes/the-dashboard-adapts-to-a-phone-width/{proposal,design}.md`
and `specs/responsive-layout/spec.md` first — they are binding. Read
`AGENTS.md`. This is a frontend-only change to
`internal/ui/static/index.html`; there is no Go code to change and no new
backend endpoint to add.

- Add the mobile rail toggle button and its `toggleMobileRail()` handler
  per design.md, wired to `#master-rail` via a new open/closed state that
  only takes effect below `lg`. Selecting a run while the rail is open
  (below `lg`) closes it back to the workflow view.
- Give the header's icon-only buttons (Manual, Work analytics, Worktrees,
  Refresh) and the run-header's Stop-run button a visible text label
  below `lg`, unchanged (icon-only, `title=` tooltip) at `lg` and above.
- Read `#workflow-graph`'s lane-rendering JS, then test it at real
  phone-class viewport widths (a phone-sized browser window or device
  emulation — not just a narrower desktop window). Give any lane/component
  that isn't already reachable via the existing horizontal scroll its own
  narrow-width treatment; this is per-component implementation judgment,
  not a fixed layout mechanism.
- Do not touch `#detail-drawer` or `#terminal-drawer`'s existing
  responsive classes — verified already correct in design.md. Do not add
  a tap-to-reveal JS tooltip mechanism, a new frontend framework, a build
  step, a new third-party JS dependency, or any new backend endpoint. Do
  not use `@media (hover: none)` or user-agent sniffing — viewport-width
  breakpoints only, consistent with the file's one existing convention.

Verification: `go build ./... && go vet ./... && go test $(go list
./internal/... | grep -v repopolicy) ./cmd/sgt/... -count=1` (confirms no
Go-side regression; this change touches no Go code, so this should be a
no-op diff on all Go test results). Since this is a static HTML/CSS/JS
file with no existing browser-based test harness in this repository,
verification is manual: load the dashboard in a real or emulated
phone-width viewport and confirm each scenario in
`specs/responsive-layout/spec.md` holds — the runs list is reachable and
usable, icon-only controls show a visible label, the workflow graph's
content is all reachable — and separately confirm the desktop
(`lg`-and-above) presentation is visually unchanged from before this
change. Run `node --check internal/ui/static/index.html`'s embedded
`<script>` content (extract it first, matching this repository's existing
`extractJSFunction`-style pattern) to confirm the new JS has no syntax
error, since nothing in `go test` executes embedded dashboard JS.
