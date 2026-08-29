package manual

import (
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/mcp"
)

// Sections must parse the embedded manual.md into an ordered, non-empty
// list of {Title, Body} sections in the order the design specifies, so
// sgt help, GET /api/manual, and the dashboard drawer can never disagree
// about what the manual contains.
func TestSectionsParsesInDocumentOrder(t *testing.T) {
	secs := Sections()

	want := []string{
		"What is Sgt",
		"Installation",
		"Your first project",
		"Day-to-day workflow",
		"Filesystem and database",
		"Configuration reference",
		"Troubleshooting",
		"Command reference",
		"MCP tools for agents",
	}
	if len(secs) != len(want) {
		t.Fatalf("got %d sections, want %d: %+v", len(secs), len(want), secs)
	}
	for i, title := range want {
		if secs[i].Title != title {
			t.Errorf("section %d title = %q, want %q", i, secs[i].Title, title)
		}
		if strings.TrimSpace(secs[i].Body) == "" {
			t.Errorf("section %q has an empty body", secs[i].Title)
		}
	}
}

// Scenario: a query matching exactly one section (by title) returns that
// one section.
func TestSearchSingleMatch(t *testing.T) {
	matches := Search("Troubleshooting")
	if len(matches) != 1 {
		t.Fatalf("Search(%q) returned %d matches, want 1: %+v", "Troubleshooting", len(matches), matches)
	}
	if matches[0].Title != "Troubleshooting" {
		t.Errorf("match title = %q, want %q", matches[0].Title, "Troubleshooting")
	}
}

// Scenario: a query matching several sections returns every match, title
// matches ordered ahead of body-only matches.
func TestSearchMultipleMatchesTitleFirst(t *testing.T) {
	matches := Search("project")
	if len(matches) < 2 {
		t.Fatalf("Search(%q) returned %d matches, want at least 2: %+v", "project", len(matches), matches)
	}
	if matches[0].Title != "Your first project" {
		t.Errorf("first match = %q, want the title match %q first", matches[0].Title, "Your first project")
	}
}

// Scenario: a query matching no section returns an empty result — the
// caller (sgt help / printHelpTopic) is responsible for reporting that
// honestly rather than fabricating an answer.
func TestSearchNoMatch(t *testing.T) {
	matches := Search("xyzzy-not-a-real-topic-nqfjwe")
	if len(matches) != 0 {
		t.Errorf("Search of a nonsense query returned %d matches, want 0: %+v", len(matches), matches)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	matches := Search("TROUBLESHOOTING")
	if len(matches) != 1 || matches[0].Title != "Troubleshooting" {
		t.Errorf("case-insensitive Search failed: %+v", matches)
	}
}

// The Command reference section's rendered content must come from the same
// data cmd/sgt's own --help output uses (manual.CommandList), not
// hand-written prose that merely happens to agree with it today. Changing
// the live source here and confirming the manual's rendered output changes
// is the only test that would fail if live substitution were silently
// removed in favor of a fixed string.
func TestCommandReferenceReflectsLiveCommandList(t *testing.T) {
	original := commandTable
	defer func() { commandTable = original }()

	commandTable = []commandEntry{
		{Usage: "sgt totally-new-command", Description: "a command that does not exist yet"},
	}

	secs := Sections()
	var body string
	for _, s := range secs {
		if s.Title == "Command reference" {
			body = s.Body
		}
	}
	if !strings.Contains(body, "sgt totally-new-command") {
		t.Errorf("Command reference section did not reflect the swapped-in command list:\n%s", body)
	}
	if strings.Contains(body, "%%LIVE_COMMANDS%%") {
		t.Errorf("Command reference section still contains the raw marker:\n%s", body)
	}
}

// The MCP tools section's rendered content must come from internal/mcp.Tools()
// itself, not separately hand-written prose. Swapping the function this
// package calls and confirming the rendered section changes is the only
// test that would fail if live substitution were silently removed.
func TestMCPToolsSectionReflectsLiveToolList(t *testing.T) {
	original := toolsFn
	defer func() { toolsFn = original }()

	toolsFn = func() []mcp.Tool {
		return []mcp.Tool{
			{Name: "sgt_totally_new_tool", Description: "a tool that does not exist yet"},
		}
	}

	secs := Sections()
	var body string
	for _, s := range secs {
		if s.Title == "MCP tools for agents" {
			body = s.Body
		}
	}
	if !strings.Contains(body, "sgt_totally_new_tool") {
		t.Errorf("MCP tools section did not reflect the swapped-in tool list:\n%s", body)
	}
	if !strings.Contains(body, "a tool that does not exist yet") {
		t.Errorf("MCP tools section did not include the swapped-in tool description:\n%s", body)
	}
	if strings.Contains(body, "%%LIVE_MCP_TOOLS%%") {
		t.Errorf("MCP tools section still contains the raw marker:\n%s", body)
	}
}

// Confirms internal/manual actually reflects the real, unmodified
// internal/mcp.Tools() list when nothing has been swapped — the live
// substitution wiring, not just the override hook, is exercised.
func TestMCPToolsSectionListsRealTools(t *testing.T) {
	secs := Sections()
	var body string
	for _, s := range secs {
		if s.Title == "MCP tools for agents" {
			body = s.Body
		}
	}
	for _, tool := range mcp.Tools() {
		if !strings.Contains(body, tool.Name) {
			t.Errorf("MCP tools section is missing real tool %q:\n%s", tool.Name, body)
		}
	}
}
