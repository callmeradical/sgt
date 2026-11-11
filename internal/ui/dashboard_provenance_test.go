package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/store"
)

// Slice dashboard-provenance (decision 11): the phase row shows the harness,
// gauntlet role and plugin version an agent phase ran with, with em-dash
// fallbacks when one is absent, and none of it for gate rows. The activity
// view gives the pull-request icon only to run.delivered envelopes.
//
// Both are asserted by executing the real functions lifted from the embedded
// index.html under node (see renderPhaseDetail in phase_detail_test.go).

const (
	fallbackNoRole         = "— no gauntlet role"
	fallbackVersionUnknown = "— version unknown"
	fallbackHarnessUnknown = "— harness unknown"
)

func provenancePhase(t *testing.T, name, kind string, payload map[string]interface{}) store.PhaseRecord {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return store.PhaseRecord{Repo: "svc", Name: name, Kind: kind, Status: "passed", DurationMs: 1200, Payload: b}
}

// renderActivity executes activityHTML from the embedded UI against a list of
// envelope records, returning the activity view's HTML.
func renderActivity(t *testing.T, envelopes []store.EnvelopeRecord) string {
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
		extractJSFunction(t, src, "copyToClipboard"),
		extractJSFunction(t, src, "activityHTML"),
	}

	envJSON, err := json.Marshal(envelopes)
	if err != nil {
		t.Fatalf("marshal envelopes: %v", err)
	}

	harness := fmt.Sprintf(`
%s

process.stdout.write(activityHTML(%s));
`, strings.Join(parts, "\n\n"), envJSON)

	dir := t.TempDir()
	script := filepath.Join(dir, "activity.mjs")
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

// "Phase provenance shows the role and plugin version": a phase that ran as a
// gauntlet role shows its harness, role and version, and no fallback.
func TestPhaseDetailShowsHarnessRoleAndPluginVersionForARolePhase(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "build", "agent", map[string]interface{}{
		"agent":          "claude",
		"role":           "gauntlet:alpha",
		"plugin_version": "0.1.0",
	}))

	for _, want := range []string{"Harness", "claude", "Role", "gauntlet:alpha", "Plugin", "0.1.0"} {
		if !strings.Contains(html, want) {
			t.Errorf("phase detail does not show %q\n%s", want, html)
		}
	}
	for _, fb := range []string{fallbackNoRole, fallbackVersionUnknown, fallbackHarnessUnknown} {
		if strings.Contains(html, fb) {
			t.Errorf("phase detail shows fallback %q for a fully recorded phase\n%s", fb, html)
		}
	}
}

// "A role-less phase shows one fallback": a plan phase with no role shows
// "— no gauntlet role" once, and no version text at all.
func TestRoleLessPhaseShowsOneFallback(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "plan", "agent", map[string]interface{}{
		"agent": "claude",
	}))

	if n := strings.Count(html, fallbackNoRole); n != 1 {
		t.Errorf("want %q exactly once, got %d\n%s", fallbackNoRole, n, html)
	}
	if strings.Contains(html, fallbackVersionUnknown) {
		t.Errorf("a role-less phase shows a version fallback\n%s", html)
	}
	if strings.Contains(html, "Plugin") || strings.Contains(strings.ToLower(html), "version") {
		t.Errorf("a role-less phase shows version text\n%s", html)
	}
	if strings.Contains(html, fallbackHarnessUnknown) {
		t.Errorf("a phase with a recorded harness shows the harness fallback\n%s", html)
	}
}

// Render half of "A role-less phase records empty provenance": the runner
// records role = "" and plugin_version = "" for a plan phase with roles on;
// the dashboard renders "— no gauntlet role" for exactly that payload.
func TestRecordedEmptyProvenanceRendersNoGauntletRole(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "plan", "agent", map[string]interface{}{
		"agent":          "claude",
		"role":           "",
		"plugin_version": "",
	}))

	if n := strings.Count(html, fallbackNoRole); n != 1 {
		t.Errorf("want %q exactly once, got %d\n%s", fallbackNoRole, n, html)
	}
	if strings.Contains(html, fallbackVersionUnknown) || strings.Contains(html, "Plugin") {
		t.Errorf("a role-less phase shows version text\n%s", html)
	}
}

// A non-empty role whose plugin version was not recorded says so rather than
// rendering a blank or guessed version.
func TestRolePhaseWithoutVersionShowsVersionUnknown(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "review", "agent", map[string]interface{}{
		"agent":          "claude",
		"role":           "gauntlet:beta",
		"plugin_version": "",
	}))

	if !strings.Contains(html, "gauntlet:beta") {
		t.Errorf("phase detail does not show the role\n%s", html)
	}
	if n := strings.Count(html, fallbackVersionUnknown); n != 1 {
		t.Errorf("want %q exactly once, got %d\n%s", fallbackVersionUnknown, n, html)
	}
	if strings.Contains(html, fallbackNoRole) {
		t.Errorf("a phase with a role shows the no-role fallback\n%s", html)
	}
}

// An agent phase whose payload names no harness (a record from before the
// harness was recorded) says the harness is unknown.
func TestAgentPhaseWithoutHarnessShowsHarnessUnknown(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "build", "agent", map[string]interface{}{}))

	if n := strings.Count(html, fallbackHarnessUnknown); n != 1 {
		t.Errorf("want %q exactly once, got %d\n%s", fallbackHarnessUnknown, n, html)
	}
	if n := strings.Count(html, fallbackNoRole); n != 1 {
		t.Errorf("want %q exactly once, got %d\n%s", fallbackNoRole, n, html)
	}
}

// Every value rendered from a payload is escaped: a role is agent-influenced
// data, not markup.
func TestPhaseDetailEscapesProvenanceValues(t *testing.T) {
	html := renderPhaseDetail(t, provenancePhase(t, "build", "agent", map[string]interface{}{
		"agent":          "<b>claude</b>",
		"role":           "<i>gauntlet:alpha</i>",
		"plugin_version": "<s>0.1.0</s>",
	}))

	for _, raw := range []string{"<b>claude</b>", "<i>gauntlet:alpha</i>", "<s>0.1.0</s>"} {
		if strings.Contains(html, raw) {
			t.Errorf("phase detail renders %q unescaped\n%s", raw, html)
		}
	}
	for _, escaped := range []string{
		"&lt;b&gt;claude&lt;/b&gt;",
		"&lt;i&gt;gauntlet:alpha&lt;/i&gt;",
		"&lt;s&gt;0.1.0&lt;/s&gt;",
	} {
		if !strings.Contains(html, escaped) {
			t.Errorf("phase detail does not show the escaped value %q\n%s", escaped, html)
		}
	}
}

// "Gate rows show no harness fields": with a gate record and an agent record
// rendered, the gate row shows no harness, role or version text and none of
// their fallbacks, while the agent row still shows them.
func TestGateRowsShowNoHarnessFields(t *testing.T) {
	agentHTML := renderPhaseDetail(t, provenancePhase(t, "build", "agent", map[string]interface{}{
		"agent": "claude",
	}))
	gateHTML := renderPhaseDetail(t, provenancePhase(t, "test", "code", map[string]interface{}{
		"command": "go test ./...",
		"output":  "ok",
	}))

	if !strings.Contains(agentHTML, "Harness") || !strings.Contains(agentHTML, fallbackNoRole) {
		t.Errorf("agent row lost its harness fields\n%s", agentHTML)
	}
	if !strings.Contains(gateHTML, "go test ./...") {
		t.Errorf("gate row lost its command\n%s", gateHTML)
	}
	for _, forbidden := range []string{
		"Harness", "Role", "Plugin", "Agent",
		fallbackNoRole, fallbackVersionUnknown, fallbackHarnessUnknown,
	} {
		if strings.Contains(gateHTML, forbidden) {
			t.Errorf("gate row shows %q\n%s", forbidden, gateHTML)
		}
	}
}

const prIcon = "fa-code-pull-request"

// "A HOLD review is not shown as a delivered PR": a review envelope that
// recorded a HOLD does not get the pull-request icon, even when its summary
// mentions a PR.
func TestHoldReviewIsNotShownAsADeliveredPR(t *testing.T) {
	html := renderActivity(t, []store.EnvelopeRecord{{
		ID:      "env-review",
		RunID:   "run-1",
		Repo:    "svc",
		Stage:   "review",
		Summary: "VERDICT: HOLD - the PR has no test for the empty input",
		Type:    "phase.completed",
	}})

	if strings.Contains(html, prIcon) {
		t.Errorf("a HOLD review envelope is shown with the pull-request icon\n%s", html)
	}
}

// The pull-request icon is reserved for run.delivered: a non-review envelope
// whose summary contains "pr" no longer gets it, and the delivery envelope
// does.
func TestPullRequestIconIsOnlyForRunDelivered(t *testing.T) {
	html := renderActivity(t, []store.EnvelopeRecord{
		{ID: "env-build", RunID: "run-1", Repo: "svc", Stage: "build", Summary: "Improved the parser", Type: "phase.completed"},
		{ID: "env-staged", RunID: "run-1", Repo: "svc", Stage: "review", Summary: "PR staged", Type: "pr.staged"},
	})
	if strings.Contains(html, prIcon) {
		t.Errorf("a non-delivered envelope is shown with the pull-request icon\n%s", html)
	}

	delivered := renderActivity(t, []store.EnvelopeRecord{{
		ID: "delivery-run-1", RunID: "run-1", Repo: "svc", Stage: "review",
		Summary: "Branch committed", Type: "run.delivered",
	}})
	if n := strings.Count(delivered, prIcon); n != 1 {
		t.Errorf("run.delivered envelope: want the pull-request icon once, got %d\n%s", n, delivered)
	}
}
