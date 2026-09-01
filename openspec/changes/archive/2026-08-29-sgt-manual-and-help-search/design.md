# Design — Sgt manual, `sgt help` search, and a dashboard manual drawer

## Ownership

One repository, `sgt`. Adds `internal/manual/` (the manual content and
its parsing/search/live-substitution logic), touches `cmd/sgt/main.go`
(the `help` case), `internal/ui/server.go` (a new `GET /api/manual`
handler), `internal/ui/static/index.html` (a new header button and
drawer), `docs/README.md` (one link added), and
`skills/sgt-help/SKILL.md` (one row added to its documentation map).

## `internal/manual` package

```go
package manual

import _ "embed"

//go:embed manual.md
var raw string

// Section is one ##-delimited section of the manual, in document order.
type Section struct {
	Title string
	Body  string
}

// Sections parses the embedded manual into an ordered list of sections,
// with each %%LIVE_...%% marker in a section's body already substituted
// via substituteLiveMarkers. It is the single entry point both sgt help
// and GET /api/manual call, so they can never see different content.
func Sections() []Section { ... }

// Search returns every section whose title or body contains query
// (case-insensitive), title matches ordered first. An empty result means
// the manual does not cover query — callers must report that honestly,
// not fabricate an answer.
func Search(query string) []Section { ... }
```

`manual.md` lives at `internal/manual/manual.md` — physically inside the
package that embeds it, the same relationship
`internal/ui/static/index.html` already has to `internal/ui/server.go`'s
`//go:embed static/*`. This is a deliberate choice: `go:embed` patterns
cannot reference a parent directory (no `..`), so a top-level `docs/
manual.md` could only be embedded via a build-time copy step — which
would create exactly the "hand-maintained copy that drifts" problem this
PRD exists to eliminate. There is one copy, and it lives where the code
that embeds it lives; `docs/README.md` links to it there.

## Live-marker substitution

`Command reference` and `MCP tools for agents` sections' raw bodies are
exactly:

```
%%LIVE_COMMANDS%%
```

and

```
%%LIVE_MCP_TOOLS%%
```

`substituteLiveMarkers(body string) string` replaces:
- `%%LIVE_COMMANDS%%` with `cmd/sgt`'s command list. Since `internal/
  manual` cannot import `cmd/sgt` (a `main` package), refactor
  `cmd/sgt/main.go`'s `printUsage`'s command-table content into a plain
  function `manual.CommandList() string` (or equivalent) that
  `printUsage` itself calls, so the CLI's own `--help` output and the
  manual's live section are generated from the exact same data, not two
  copies that happen to agree today.
- `%%LIVE_MCP_TOOLS%%` with a formatted listing built from
  `internal/mcp.Tools()` (name + description per tool) — `internal/
  manual` may import `internal/mcp` directly (not a `main` package, no
  import-cycle risk: confirm `internal/mcp` does not import `internal/
  manual`, which it should have no reason to).

## `sgt help` (`cmd/sgt/main.go`)

Replace the `"help"`/`"--help"`/`"-h"` case:

```go
case "--help", "-h", "help":
	if len(os.Args) > 2 {
		printHelpTopic(strings.Join(os.Args[2:], " "))
	} else {
		printUsage()
	}
```

`printUsage()` gains a table-of-contents line before its existing
subcommand list: the section titles from `manual.Sections()`, so a user
who types plain `sgt help` sees more than they do today without needing
to know a topic to ask for.

`printHelpTopic(query string)`:
- `matches := manual.Search(query)`
- 0 matches: print "The manual does not cover %q." plus every section
  title, so the user has somewhere to go next instead of a dead end.
- 1 match: print that section's title and full body.
- 2+ matches: print each matching title with a one-line pointer
  ("run `sgt help \"<title>\"` for the full section") rather than
  dumping every matched section's full body at once.

## `GET /api/manual` (`internal/ui/server.go`)

```go
// handleManual answers GET /api/manual with the parsed, live-substituted
// manual sections. A plain read, like handleAnalytics: no request body,
// no side effects.
func (srv *Server) handleManual(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sections": manual.Sections(),
	})
}
```

Registered alongside the other `/api/*` routes in `Handler()`.

## Dashboard drawer (`internal/ui/static/index.html`)

Follow `btn-open-analytics`/`openAnalyticsDrawer`'s exact shape:

- A new header button (`btn-open-manual`) next to the existing
  Workers/Work-analytics buttons, `onclick="openManualDrawer(this)"`.
- `async function openManualDrawer(opener)`: `fetch('/api/manual')`,
  then `openDrawer('Manual', 'manual', manualHTML(data), opener)`.
- `manualHTML(data)`: a pure function (data in, HTML string out,
  extractable and node-testable via `internal/ui/stage_matrix_test.go`'s
  established `extractJSFunction` harness pattern) rendering a table of
  contents (section titles as anchor links) followed by each section's
  title and body. Body text is rendered via `escapeHTML` inside a
  `<pre class="whitespace-pre-wrap">` block — raw markdown as readable
  preformatted text, no markdown-to-HTML conversion, no new JS
  dependency, matching this dashboard's existing convention (see
  `analyticsHTML`'s precedent).

## `skills/sgt-help/SKILL.md`

Add one row to the top of the existing "Documentation map" table: the
manual (`internal/manual/manual.md`, reachable via `sgt help <topic>` or
`GET /api/manual`) as the first thing to check for any general question,
before the existing per-topic file list — which remains for anything the
manual's own bounded core path does not cover.

## Rejected alternatives

**Copying `docs/manual.md` into `internal/manual/` at build time.**
Rejected: `go:embed` forbids referencing a parent directory, and a
copy step reintroduces the exact two-copies-that-can-drift problem this
change exists to eliminate. One physical file, embedded where it lives.

**Rendering the manual's markdown to HTML in the browser.** Rejected:
this dashboard has zero third-party JS dependencies today (confirmed by
`work-analytics-dashboard`'s equivalent design decision); a markdown
parser is unwarranted for what is, at this scale, quite readable as
preformatted text.

**A single `sgt help` handler covering both no-argument and
with-argument cases without a distinct `printHelpTopic`.** Rejected:
keeping them as two named functions each with one job (show the table of
contents plus static usage; or search and answer) is simpler to test in
isolation than one function branching internally.
