package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it, so printUsage/printHelpTopic can be exercised
// directly (fast) rather than only through a rebuilt binary.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	buf := make([]byte, 0, 64*1024)
	for {
		chunk := make([]byte, 4096)
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

// Scenario: no argument shows the manual's table of contents plus usage.
func TestPrintUsageShowsTableOfContentsAndSubcommands(t *testing.T) {
	out := captureStdout(t, printUsage)

	for _, want := range []string{
		"What is Sgt",
		"Installation",
		"Command reference",
		"Usage:",
		"sgt run",
		"sgt status",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printUsage output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// Scenario: a query matching exactly one section prints that section's
// title and full body.
func TestPrintHelpTopicSingleMatchPrintsFullSection(t *testing.T) {
	out := captureStdout(t, func() { printHelpTopic("Troubleshooting") })

	if !strings.Contains(out, "Troubleshooting") {
		t.Errorf("output missing the matched section title\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "docs/troubleshooting.md") {
		t.Errorf("output missing the section's body content\n--- output ---\n%s", out)
	}
}

// Scenario: a query matching more than one section lists every matching
// title with a pointer to ask again more specifically, not every matched
// section's full body.
func TestPrintHelpTopicMultipleMatchesListsTitlesOnly(t *testing.T) {
	out := captureStdout(t, func() { printHelpTopic("project") })

	if !strings.Contains(out, "Your first project") {
		t.Errorf("output missing a matching title\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "sgt help") {
		t.Errorf("output missing the pointer to ask again\n--- output ---\n%s", out)
	}
	// The full body of "Configuration reference" (a body-only match for
	// "project" via docs/schema.md's project-field prose) must not be
	// dumped in full alongside the title list.
	if strings.Contains(out, "Every project field") {
		t.Errorf("multi-match output must not dump full section bodies\n--- output ---\n%s", out)
	}
}

// Scenario: a query matching no section states that honestly and lists
// the manual's available section titles instead of fabricating an answer.
func TestPrintHelpTopicNoMatchIsHonest(t *testing.T) {
	out := captureStdout(t, func() { printHelpTopic("xyzzy-not-a-real-topic-nqfjwe") })

	if !strings.Contains(out, "does not cover") {
		t.Errorf("output must state the manual does not cover the query\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "What is Sgt") {
		t.Errorf("output must list available section titles\n--- output ---\n%s", out)
	}
}

// The help/--help/-h case must route a bare invocation to printUsage and a
// topic invocation to printHelpTopic, matching design.md's dispatch.
func TestHelpCommandRoutesArgumentsToTopicSearch(t *testing.T) {
	bin := sgtBinary(t)

	outTopic, err := exec.Command(bin, "help", "Troubleshooting").CombinedOutput()
	if err != nil {
		t.Fatalf("sgt help Troubleshooting: %v\n%s", err, outTopic)
	}
	if !strings.Contains(string(outTopic), "docs/troubleshooting.md") {
		t.Errorf("sgt help Troubleshooting: expected the Troubleshooting section body, got:\n%s", outTopic)
	}

	outNoArg, err := exec.Command(bin, "help").CombinedOutput()
	if err != nil {
		t.Fatalf("sgt help: %v\n%s", err, outNoArg)
	}
	if !strings.Contains(string(outNoArg), "Usage:") {
		t.Errorf("sgt help: expected usage text, got:\n%s", outNoArg)
	}
}
