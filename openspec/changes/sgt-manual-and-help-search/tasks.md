# Tasks — Sgt manual, `sgt help` search, and a dashboard manual drawer

One repository, `sgt`, so one task.

## Task 1 — manual content, package, CLI search, HTTP endpoint, drawer, skill update

Repository: `sgt`. Depends on: nothing. Read
`openspec/changes/sgt-manual-and-help-search/{proposal,design}.md` and
`specs/documentation/spec.md` first — they are binding. Read `AGENTS.md`.
Work test-first per decision D3. Before writing anything, read:
`README.md`'s "Quick start" and "Upgrading from a pre-rename (Sergeant)
install" sections (the real source for the manual's installation/
first-project content); `cmd/sgt/main.go`'s `printUsage`; `internal/mcp/
server.go`'s `Tools()`; `internal/ui/static/index.html`'s
`openWorkersDrawer`/`openAnalyticsDrawer`/`openDrawer` and `analyticsHTML`
(the drawer and pure-render-function patterns to match exactly);
`internal/ui/server.go`'s `handleAnalytics` (the plain-read handler
shape); `internal/ui/stage_matrix_test.go`'s `extractJSFunction` harness;
`skills/sgt-help/SKILL.md`'s current "Documentation map" table.

- Write `internal/manual/manual.md`: sections in this order — What is
  Sgt (new content — verify nothing accurate exists elsewhere first);
  Installation; Your first project (both consolidated and verified from
  `README.md`); Day-to-day workflow; Configuration reference (short,
  links to `docs/schema.md`); Troubleshooting (short, links to
  `docs/troubleshooting.md`); Command reference (body is exactly
  `%%LIVE_COMMANDS%%`); MCP tools for agents (body is exactly
  `%%LIVE_MCP_TOOLS%%`).
- Add `internal/manual/manual.go`: `//go:embed manual.md`, `Section`
  type, `Sections()`, `Search(query string) []Section`, and the
  live-marker substitution per design.md. Confirm no import cycle before
  importing `internal/mcp` from `internal/manual`.
- Refactor `cmd/sgt/main.go`'s command-list content into a function
  `internal/manual`'s live-substitution can call, so `printUsage` and the
  manual's `%%LIVE_COMMANDS%%` render from one source, not two that
  happen to agree today.
- Update `cmd/sgt/main.go`'s `help`/`--help`/`-h` case: no argument keeps
  `printUsage` (now prefixed with the manual's table of contents);
  one-or-more arguments call a new `printHelpTopic` per design.md
  (0/1/2+ match behavior).
- Add `GET /api/manual` to `internal/ui/server.go` per design.md.
- Add the manual drawer to `internal/ui/static/index.html`: header
  button, `openManualDrawer`, `manualHTML` (pure function, node-testable).
- Add one row to `skills/sgt-help/SKILL.md`'s "Documentation map" naming
  the manual first, per design.md.
- Do not add a third-party JS dependency. Do not add a new full-page
  dashboard view. Do not duplicate `docs/schema.md`/`docs/
  troubleshooting.md`'s content into the manual — link to them.

Verification: `go build ./... && go vet ./... && go test ./internal/... -count=1`.
Tests must cover every scenario in `specs/documentation/spec.md`:
`sgt help` with no argument shows the table of contents plus usage; a
single-match query prints that section; a multi-match query lists titles
without dumping full bodies; a no-match query states the gap honestly and
lists available titles; the `Command reference` and `MCP tools for
agents` sections' rendered content actually reflects `manual.CommandList`
/`internal/mcp.Tools()` (assert by changing what those return in a test
and confirming the manual's rendered output changes accordingly, not by
asserting fixed strings that would still pass if live substitution were
silently removed); `GET /api/manual` returns the same sections `sgt help`
answers from and performs no write (assert via a fixture confirming no
`store`/`db.Exec` call in its path); the dashboard's `manualHTML`
function, executed under real `node` per the `extractJSFunction` harness
pattern, renders the table of contents and section bodies from a fixture
response. Exit status decides the outcome.
