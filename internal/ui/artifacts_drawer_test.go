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

// renderArtifactsSection executes the real embedded artifactsSectionHTML/
// artifactItemHTML against a list of artifact records, the same
// extract-and-run-under-node approach stage_matrix_test.go/
// manual_drawer_test.go use — the only honest way to assert what an
// operator sees is to execute the real JavaScript, not a Go reimplementation
// of it. Review 35 found this rendering had no automated test anywhere,
// only an ad-hoc manual check; this harness closes that gap permanently.
func renderArtifactsSection(t *testing.T, artifacts []map[string]interface{}) string {
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
		extractJSFunction(t, src, "artifactItemHTML"),
		extractJSFunction(t, src, "artifactsSectionHTML"),
	}

	dataJSON, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatalf("marshal artifacts: %v", err)
	}

	harness := fmt.Sprintf(`
%s

process.stdout.write(artifactsSectionHTML(%s));
`, strings.Join(parts, "\n\n"), dataJSON)

	dir := t.TempDir()
	script := filepath.Join(dir, "artifacts.mjs")
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

// Scenario: a run with no artifacts shows no artifacts section.
func TestArtifactsSectionHTMLShowsNoSectionWhenEmpty(t *testing.T) {
	html := renderArtifactsSection(t, []map[string]interface{}{})
	if html != "" {
		t.Errorf("expected no artifacts to render nothing, got:\n%s", html)
	}
}

// Scenario: captured artifacts render beneath the workflow graph, grouped by
// phase — an image gets a thumbnail, a non-image gets a filename link, and a
// dropped-count row (never silently omitted) gets its own notice.
func TestArtifactsSectionHTMLGroupsByPhaseAndRendersEachKind(t *testing.T) {
	html := renderArtifactsSection(t, []map[string]interface{}{
		{"id": "art-1", "phase_id": "build", "filename": "screenshot.png", "content_type": "image/png"},
		{"id": "art-2", "phase_id": "build", "filename": "report.txt", "content_type": "text/plain"},
		{"id": "art-3", "phase_id": "test", "dropped_count": 2, "dropped_reason": "exceeded max artifact count (20)"},
	})

	if !strings.Contains(html, "Artifacts") {
		t.Errorf("missing the Artifacts section header:\n%s", html)
	}
	for _, want := range []string{"build", "test"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing phase group %q:\n%s", want, html)
		}
	}
	if !strings.Contains(html, `/api/artifacts/art-1/content`) || !strings.Contains(html, `<img`) {
		t.Errorf("expected an <img> thumbnail for the image artifact:\n%s", html)
	}
	if !strings.Contains(html, `/api/artifacts/art-2/content`) || !strings.Contains(html, "report.txt") {
		t.Errorf("expected a filename link for the non-image artifact:\n%s", html)
	}
	if !strings.Contains(html, "2 artifact(s) dropped") {
		t.Errorf("expected the dropped-count notice to be rendered, not omitted:\n%s", html)
	}
}
