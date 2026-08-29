package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// toggleMobileRail/closeMobileRail are the only two functions this change adds
// that touch `document`, so they cannot be exercised through the pure,
// string-in/string-out harness stage_matrix_test.go's renderLane uses. This
// runs them against a minimal stub `document` instead — just enough
// getElementById/classList/getAttribute/setAttribute to observe the state
// transitions an operator's tap actually drives.
func runMobileRailHarness(t *testing.T) []map[string]map[string]any {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot execute the embedded UI logic")
	}

	raw, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	src := string(raw)

	parts := []string{
		extractJSFunction(t, src, "toggleMobileRail"),
		extractJSFunction(t, src, "closeMobileRail"),
	}

	const stub = `
class FakeClassList {
  constructor() { this.set = new Set(); }
  contains(c) { return this.set.has(c); }
  toggle(c, force) {
    const shouldAdd = force === undefined ? !this.set.has(c) : !!force;
    if (shouldAdd) this.set.add(c); else this.set.delete(c);
    return shouldAdd;
  }
  remove(c) { this.set.delete(c); }
}
class FakeElement {
  constructor() { this.attrs = {}; this.classList = new FakeClassList(); }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null; }
  setAttribute(name, value) { this.attrs[name] = String(value); }
}
const elements = new Map([['master-rail', new FakeElement()], ['btn-toggle-rail', new FakeElement()]]);
const document = { getElementById: (id) => elements.get(id) || null };
`

	snapshot := `() => ({
    rail: { open: rail.classList.contains('mobile-rail-open') },
    btn: { ariaExpanded: btn.getAttribute('aria-expanded') },
  })`

	harness := fmt.Sprintf(`
%s

%s

const rail = document.getElementById('master-rail');
const btn = document.getElementById('btn-toggle-rail');
const snapshot = %s;

const states = [];
states.push({ initial: snapshot() });
toggleMobileRail();
states.push({ afterFirstToggle: snapshot() });
toggleMobileRail();
states.push({ afterSecondToggle: snapshot() });
toggleMobileRail();
closeMobileRail();
states.push({ afterOpenThenClose: snapshot() });

process.stdout.write(JSON.stringify(states));
`, stub, strings.Join(parts, "\n\n"), snapshot)

	dir := t.TempDir()
	script := filepath.Join(dir, "mobile_rail.mjs")
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

	var states []map[string]map[string]any
	if err := json.Unmarshal(out, &states); err != nil {
		t.Fatalf("unmarshal node output: %v\n%s", err, out)
	}
	return states
}

// A phone-width operator taps the header's toggle to reveal the run list,
// then taps it again to dismiss it — the toggle must actually flip both the
// CSS state and aria-expanded, not just one of the two.
func TestToggleMobileRailOpensAndClosesOnRepeatedTaps(t *testing.T) {
	states := runMobileRailHarness(t)

	initial := states[0]["initial"]
	if initial["rail"].(map[string]any)["open"] != false {
		t.Errorf("rail should start closed, got %+v", initial["rail"])
	}
	if initial["btn"].(map[string]any)["ariaExpanded"] != nil {
		t.Errorf("aria-expanded should start unset, got %+v", initial["btn"])
	}

	afterFirst := states[1]["afterFirstToggle"]
	if afterFirst["rail"].(map[string]any)["open"] != true {
		t.Errorf("first toggle should open the rail, got %+v", afterFirst["rail"])
	}
	if afterFirst["btn"].(map[string]any)["ariaExpanded"] != "true" {
		t.Errorf("first toggle should set aria-expanded=true, got %+v", afterFirst["btn"])
	}

	afterSecond := states[2]["afterSecondToggle"]
	if afterSecond["rail"].(map[string]any)["open"] != false {
		t.Errorf("second toggle should close the rail, got %+v", afterSecond["rail"])
	}
	if afterSecond["btn"].(map[string]any)["ariaExpanded"] != "false" {
		t.Errorf("second toggle should set aria-expanded=false, got %+v", afterSecond["btn"])
	}
}

// Selecting a run from the open rail calls closeMobileRail() (see
// renderRunList's button handler) rather than toggleMobileRail() — it must
// force the rail closed regardless of current state, not flip it.
func TestCloseMobileRailForcesClosedRegardlessOfState(t *testing.T) {
	states := runMobileRailHarness(t)

	afterOpenThenClose := states[3]["afterOpenThenClose"]
	if afterOpenThenClose["rail"].(map[string]any)["open"] != false {
		t.Errorf("closeMobileRail should leave the rail closed, got %+v", afterOpenThenClose["rail"])
	}
	if afterOpenThenClose["btn"].(map[string]any)["ariaExpanded"] != "false" {
		t.Errorf("closeMobileRail should set aria-expanded=false, got %+v", afterOpenThenClose["btn"])
	}
}

// The header's mobile nav control and the visible-label spans it depends on
// are markup design.md specifies exactly, not JS this harness can execute
// (they are static HTML, not render functions). Asserting on the raw source
// is the same "read what actually ships" approach analytics/manual drawer
// tests take for their own static wrapper markup.
func TestMobileNavMarkupMatchesDesign(t *testing.T) {
	raw, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	src := string(raw)

	for _, want := range []string{
		// The toggle button: below-lg only, wired to master-rail, announces its
		// own expanded/collapsed state.
		`id="btn-toggle-rail"`,
		`onclick="toggleMobileRail()"`,
		`aria-controls="master-rail"`,
		`aria-expanded="false"`,
		`class="lg:hidden min-w-[44px] min-h-[44px]`,
		// #master-rail itself keeps its existing desktop breakpoint untouched;
		// the toggle must never show at lg and above.
		`id="master-rail" aria-label="Pipeline runs" class="hidden lg:flex`,
		// Visible labels alongside icon-only controls, shown only below lg.
		`<span class="lg:hidden text-[10px] font-bold">Manual</span>`,
		`<span class="lg:hidden text-[10px] font-bold">Work analytics</span>`,
		`<span class="lg:hidden text-[10px] font-bold">Worktrees</span>`,
		`<span class="lg:hidden text-[10px] font-bold">Refresh</span>`,
		`<span class="lg:hidden text-[10px] font-bold">Stop run</span>`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("index.html should contain %q", want)
		}
	}

	// Above lg, every affected control must still carry its original title=
	// tooltip unchanged — the visible label is additive, not a replacement.
	for _, want := range []string{
		`title="Sgt manual"`,
		`title="Work analytics"`,
		`title="Allocated worktrees (includes retained, non-running ones)"`,
		`title="Refresh state"`,
		`title="Stop run"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("index.html should still contain the pre-existing tooltip %q", want)
		}
	}
}

// #workflow-graph has no dedicated narrow-width CSS: laneHTML's own column
// packing (buildGraphLayout/positionLayout) already keys off the container's
// real width and wraps to one column per row once two 232px node columns no
// longer fit side by side (positionLayout's "needed > usable" check). At a
// phone-class width that one-column wrap is what design.md means by
// "overflow-auto's existing horizontal scroll is sufficient" — every lane
// fits inside the viewport without needing that scroll at all. This asserts
// the width laneHTML actually reports (the number #workflow-graph's
// overflow-auto would otherwise have to scroll to reach) stays within a
// phone-class viewport, for a lane with every kind of column this file
// renders (stages, a gates group, and a lifecycle group).
func TestWorkflowGraphFitsAPhoneWidthWithoutHorizontalOverflow(t *testing.T) {
	const phoneWidth = 375 // iPhone SE / smallest common phone viewport, CSS px

	def := defFor(
		WorkflowNode{ID: "stage:build", Label: "build", Kind: "stage", Group: "svc"},
		WorkflowNode{ID: "stage:test", Label: "test", Kind: "stage", Group: "svc"},
		WorkflowNode{ID: "stage:review", Label: "review", Kind: "stage", Group: "svc"},
		WorkflowNode{ID: "gate:lint", Label: "lint", Kind: "gate", Group: "svc"},
		WorkflowNode{ID: "gate:unit", Label: "unit", Kind: "gate", Group: "svc"},
		WorkflowNode{ID: "lifecycle:pending", Label: "pending", Kind: "lifecycle", Group: "svc"},
		WorkflowNode{ID: "lifecycle:red", Label: "red", Kind: "lifecycle", Group: "svc"},
		WorkflowNode{ID: "lifecycle:green", Label: "green", Kind: "lifecycle", Group: "svc"},
		WorkflowNode{ID: "lifecycle:sealed", Label: "sealed", Kind: "lifecycle", Group: "svc"},
		WorkflowNode{ID: "lifecycle:merged", Label: "merged", Kind: "lifecycle", Group: "svc"},
	)

	html := renderLaneWidth(t, "svc", def, nil, nil, phoneWidth)

	m := regexp.MustCompile(`width:(\d+)px;height:(\d+)px`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("could not find the lane's outer width/height in its rendered style\n--- html ---\n%s", html)
	}
	width, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse width: %v", err)
	}
	if width > phoneWidth {
		t.Errorf("lane rendered %dpx wide at a %dpx viewport — the graph's own column packing should have wrapped every column to its own row rather than force horizontal scroll to reach content", width, phoneWidth)
	}
}
