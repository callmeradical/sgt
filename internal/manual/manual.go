// Package manual owns Sgt's single embedded manual: its content, its
// parsing into sections, its search, and the live-marker substitution that
// keeps its Command reference and MCP tools sections generated from the
// running system rather than hand-copied. sgt help, GET /api/manual, and
// the dashboard's manual drawer all read through Sections()/Search() so
// they can never disagree about what the manual says.
package manual

import (
	_ "embed"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/callmeradical/sgt/internal/mcp"
)

//go:embed manual.md
var raw string

// Section is one ##-delimited section of the manual, in document order.
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// commandEntry is one row of the command reference. commandTable is a
// package variable (not a literal inline in CommandList) so tests in this
// package can swap it to prove the Command reference section is generated
// from it, not from separately hand-written prose.
type commandEntry struct {
	Usage       string
	Description string
}

// commandTable is the single source for both cmd/sgt's own --help output
// (via CommandList, which printUsage calls) and the manual's
// %%LIVE_COMMANDS%% section — one copy, not two that happen to agree today.
var commandTable = []commandEntry{
	{Usage: "sgt run <project>", Description: "Run a multi-repo factory pipeline DAG"},
	{Usage: "sgt status", Description: "Show recent factory runs and phase states"},
	{Usage: "sgt ui", Description: "Start embedded Web UI dashboard (http://127.0.0.1:8484)"},
	{Usage: "sgt mcp", Description: "Start MCP JSON-RPC stdio server for Goose / Claude"},
	{Usage: "sgt version", Description: "Print version info"},
	{Usage: "sgt help [topic...]", Description: "Show the manual's table of contents, or search it for a topic"},
}

// toolsFn is internal/mcp.Tools by default. It is a package variable, not a
// direct call, so tests in this package can swap it to prove the MCP tools
// section is generated from internal/mcp.Tools() and not separately
// hand-written prose.
var toolsFn = mcp.Tools

// CommandList renders the command table as aligned, indented text.
// cmd/sgt's printUsage calls this directly so the CLI's own --help output
// and the manual's Command reference section render from the same data.
func CommandList() string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 4, ' ', 0)
	for _, c := range commandTable {
		fmt.Fprintf(w, "  %s\t%s\n", c.Usage, c.Description)
	}
	w.Flush()
	return b.String()
}

func mcpToolsList() string {
	tools := toolsFn()
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "- `%s` — %s\n", t.Name, t.Description)
	}
	return b.String()
}

func substituteLiveMarkers(body string) string {
	body = strings.ReplaceAll(body, "%%LIVE_COMMANDS%%", CommandList())
	body = strings.ReplaceAll(body, "%%LIVE_MCP_TOOLS%%", mcpToolsList())
	return body
}

// parseSections splits the manual on top-level "## " headings, discarding
// any preamble before the first one (manual.md's leading "# Sgt manual"
// title).
func parseSections(src string) []Section {
	lines := strings.Split(src, "\n")
	var sections []Section
	var title string
	var body []string
	inSection := false

	flush := func() {
		if inSection {
			sections = append(sections, Section{
				Title: title,
				Body:  strings.TrimSpace(strings.Join(body, "\n")),
			})
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			title = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			body = nil
			inSection = true
			continue
		}
		if inSection {
			body = append(body, line)
		}
	}
	flush()

	return sections
}

// Sections parses the embedded manual into an ordered list of sections,
// with each %%LIVE_...%% marker in a section's body already substituted.
// It is the single entry point sgt help, GET /api/manual, and the
// dashboard drawer all call, so they can never see different content.
func Sections() []Section {
	sections := parseSections(raw)
	for i := range sections {
		sections[i].Body = substituteLiveMarkers(sections[i].Body)
	}
	return sections
}

// Search returns every section whose title or body contains query
// (case-insensitive), title matches ordered ahead of body-only matches. An
// empty result means the manual does not cover query — callers must report
// that honestly, not fabricate an answer.
func Search(query string) []Section {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	sections := Sections()
	var titleMatches, bodyMatches []Section
	for _, s := range sections {
		if strings.Contains(strings.ToLower(s.Title), q) {
			titleMatches = append(titleMatches, s)
			continue
		}
		if strings.Contains(strings.ToLower(s.Body), q) {
			bodyMatches = append(bodyMatches, s)
		}
	}

	return append(titleMatches, bodyMatches...)
}
