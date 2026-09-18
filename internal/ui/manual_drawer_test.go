package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// renderManual executes manualHTML from the embedded UI against a fixture
// GET /api/manual response, returning the drawer body HTML. Mirrors
// renderAnalytics's extract-and-run-under-node approach: the only honest
// way to assert what an operator sees is to execute the real JavaScript.
func renderManual(t *testing.T, data map[string]interface{}) string {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot execute the embedded UI render logic")
	}

	raw, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	src := string(raw)

	parts := []string{
		extractJSFunction(t, src, "escapeHTML"),
		"const escapeAttr = escapeHTML;",
		extractJSFunction(t, src, "manualSlug"),
		extractJSFunction(t, src, "manualHTML"),
	}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}

	harness := fmt.Sprintf(`
%s

process.stdout.write(manualHTML(%s));
`, strings.Join(parts, "\n\n"), dataJSON)

	dir := t.TempDir()
	script := filepath.Join(dir, "manual.mjs")
	if err := os.WriteFile(script, []byte(harness), 0o644); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	out, err := exec.Command(node, script).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node failed: %v", err)
	}
	return string(out)
}

// Scenario: the dashboard's manual drawer renders the table of contents and
// every section's title and body from a fixture GET /api/manual response.
func TestManualHTMLRendersTableOfContentsAndSectionBodies(t *testing.T) {
	html := renderManual(t, map[string]interface{}{
		"sections": []map[string]string{
			{"title": "What is Sgt", "body": "Sgt is a project-aware first mate."},
			{"title": "Troubleshooting", "body": "See docs/troubleshooting.md for details."},
		},
	})

	for _, want := range []string{
		"What is Sgt",
		"Troubleshooting",
		"Sgt is a project-aware first mate.",
		"See docs/troubleshooting.md for details.",
		"<pre",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("manual drawer html missing %q\n--- html ---\n%s", want, html)
		}
	}
}

// Table-of-contents entries must be anchor links to their section, not just
// a flat list of titles with no way to jump to the body — otherwise a long
// manual is a wall of text with no navigation.
func TestManualHTMLTableOfContentsLinksToSections(t *testing.T) {
	html := renderManual(t, map[string]interface{}{
		"sections": []map[string]string{
			{"title": "Installation", "body": "Clone and build."},
		},
	})

	if !strings.Contains(html, `href="#`) {
		t.Errorf("table of contents must link to sections\n--- html ---\n%s", html)
	}
}

// Body text is rendered as escaped, preformatted text — no markdown-to-HTML
// conversion and no injection from unescaped section content.
func TestManualHTMLEscapesBodyContent(t *testing.T) {
	html := renderManual(t, map[string]interface{}{
		"sections": []map[string]string{
			{"title": "Test", "body": "<script>alert(1)</script>"},
		},
	})

	if strings.Contains(html, "<script>") {
		t.Errorf("section body must be escaped, not rendered as raw HTML\n--- html ---\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("escaped body content should appear as entities\n--- html ---\n%s", html)
	}
}

// An empty manual (an unexpected but possible fixture shape) must not
// crash the render — it should say plainly there is nothing to show.
func TestManualHTMLHandlesNoSections(t *testing.T) {
	html := renderManual(t, map[string]interface{}{"sections": []map[string]string{}})

	if strings.Contains(html, "undefined") {
		t.Errorf("empty manual must not render literal 'undefined'\n--- html ---\n%s", html)
	}
}
