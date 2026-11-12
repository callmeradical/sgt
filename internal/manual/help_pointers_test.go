package manual

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// helpPointer matches a documented `sgt help <topic>` command: the topic is
// one bare word or one double-quoted phrase. Placeholders such as
// `sgt help [topic]` or `sgt help "<title>"` are not pointers to a section.
var helpPointer = regexp.MustCompile("`sgt help (\"[^\"`]+\"|[^\\s`\"\\[<]+)`")

// Every `sgt help <topic>` the operator docs tell someone to run must land
// on exactly one section: sgt help prints a section only for a single
// match, and for several it only says to run `sgt help "<title>"` again.
func TestEveryDocumentedHelpPointerLandsOnOneSection(t *testing.T) {
	root := filepath.Join("..", "..")
	docs := []string{
		"README.md",
		filepath.Join(".agents", "skills", "sgt-setup", "SKILL.md"),
		filepath.Join("internal", "manual", "manual.md"),
	}
	found := 0
	for _, rel := range docs {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, m := range helpPointer.FindAllStringSubmatch(string(body), -1) {
			topic := strings.Trim(m[1], `"`)
			if strings.ContainsAny(topic, "<[") {
				continue
			}
			found++
			matches := Search(topic)
			if len(matches) != 1 {
				titles := make([]string, len(matches))
				for i, s := range matches {
					titles[i] = s.Title
				}
				t.Errorf("%s: `sgt help %s` matches %d sections %v, want exactly 1", rel, m[1], len(matches), titles)
			}
		}
	}
	if found == 0 {
		t.Fatal("found no `sgt help <topic>` pointers in the docs; the pattern is wrong")
	}
}

// The README and setup skill send operators to `sgt help "Supported
// agents"`; it must print that section, not a list of two.
func TestManualSearchForSupportedAgentsLandsOnOneSection(t *testing.T) {
	matches := Search("Supported agents")
	if len(matches) != 1 {
		t.Fatalf(`Search("Supported agents") returned %d sections, want 1: %+v`, len(matches), matches)
	}
	if matches[0].Title != "Supported agents" {
		t.Errorf(`Search("Supported agents") landed on %q`, matches[0].Title)
	}
}
