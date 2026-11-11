package manual

import (
	"strings"
	"testing"
)

// manualText is the manual as every reader sees it: the embedded content,
// parsed into sections with live markers substituted.
func manualText() string {
	var b strings.Builder
	for _, s := range Sections() {
		b.WriteString(s.Title)
		b.WriteString("\n")
		b.WriteString(s.Body)
		b.WriteString("\n")
	}
	return b.String()
}

// The manual states that claude is the default harness, and tells the
// operator how gauntlet roles work, how to opt out, how to pin another
// harness, and how to install the plugin the preflight looks for
// (tasks.md 10.1).
func TestManualStatesClaudeIsTheDefaultHarnessAndHowRolesWork(t *testing.T) {
	text := manualText()
	for _, want := range []string{
		"The default agent harness is claude.",
		"gauntlet_roles: false",
		"defaults.agent",
		"gauntlet:alpha",
		"gauntlet:beta",
		"/plugin install gauntlet@gauntlet",
		"plan, build, test, review",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("manual does not contain %q", want)
		}
	}
}

// An operator asking `sgt help gauntlet` finds the section that explains
// the roles, not an empty result.
func TestManualSearchFindsGauntletRoles(t *testing.T) {
	matches := Search("gauntlet")
	if len(matches) == 0 {
		t.Fatal(`Search("gauntlet") returned no section`)
	}
	found := false
	for _, m := range matches {
		if strings.Contains(m.Body, "gauntlet_roles: false") {
			found = true
		}
	}
	if !found {
		t.Errorf(`no section matching "gauntlet" documents the gauntlet_roles: false opt-out: %+v`, matches)
	}
}

// The README sends operators to `sgt help gauntlet_roles` for the full
// description, so that query must land on exactly one section: the one that
// documents the roles and the opt-out.
func TestManualSearchForTheOptOutKeyLandsOnOneSection(t *testing.T) {
	matches := Search("gauntlet_roles")
	if len(matches) != 1 {
		t.Fatalf(`Search("gauntlet_roles") returned %d sections, want 1: %+v`, len(matches), matches)
	}
	if !strings.Contains(matches[0].Body, "The default agent harness is claude.") {
		t.Errorf(`the section Search("gauntlet_roles") lands on does not describe the default harness: %q`, matches[0].Title)
	}
}
