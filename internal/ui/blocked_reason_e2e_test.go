package ui

// Integration seam check for verification-freshness, "A blocked reason says
// why the phase failed" (design decision 8a), against real runner output.
// blocked_reason_test.go seeds the records the runner writes; these tests
// let the runner write them: POST /api/dispatch and POST /api/run-resume
// over real git repos, with the stub claude of holdfix_test.go playing
// gauntlet:alpha and gauntlet:beta, and the bullet's reason read back from
// the store.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handoffFileAction is a stub alpha action added for these tests: the
// session writes a handoff file naming its working directory into the
// gauntlet handoff directory under HOME and prints no HANDOFF: line, as a
// builder that stops without honouring the contract does.
const handoffFileAction = `alpha:handoff-file) d="$HOME/.local/state/gauntlet/handoffs"; mkdir -p "$d"; printf 'Worktree: %s\n' "$(pwd)" > "$d/alpha-$n.md"; echo "stopping at my context threshold"; exit 0 ;;
`

// withHandoffFileStub replaces env's stub claude with holdClaude plus
// handoffFileAction.
func withHandoffFileStub(t *testing.T, env holdEnv) {
	t.Helper()
	const anchor = "alpha:handoff) "
	if !strings.Contains(holdClaude, anchor) {
		t.Fatalf("holdClaude has no %q case to extend", anchor)
	}
	script := strings.Replace(holdClaude, anchor, handoffFileAction+anchor, 1)
	if err := os.WriteFile(env.claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Scenario: A handoff failure is not described as a gate failure — driven by
// a real gauntlet:alpha session that hands off.
func TestBlockedReasonEndToEndAHandoffBlocksWithTheBuildAndTheHandoff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		path   func(env holdEnv) string
	}{
		{"reported with a HANDOFF line", "handoff", func(env holdEnv) string {
			return filepath.Join(env.stubDir, "handoff-1.md")
		}},
		{"unreported handoff file naming the worktree", "handoff-file", func(env holdEnv) string {
			return filepath.Join(os.Getenv("HOME"), ".local", "state", "gauntlet", "handoffs", "alpha-1.md")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHoldEnv(t, 2, "", svcRepo("true"))
			withHandoffFileStub(t, env)
			env.script(t, "alpha", tc.action)

			runID := env.dispatchFailed(t, "svc")

			build := phaseRecord(t, env.st, runID, "build")
			path := tc.path(env)
			if got, _ := payloadField(t, build, "handoff_path"); got != path {
				t.Fatalf("build handoff_path = %q, want %q; record %+v", got, path, build)
			}
			b := env.bullet(t, runID, "svc")
			if b.Status != "blocked" {
				t.Fatalf("bullet status = %q, want blocked", b.Status)
			}
			if !strings.HasPrefix(b.BlockedReason, "svc: build:") {
				t.Errorf("blocked reason = %q, want it to begin %q", b.BlockedReason, "svc: build:")
			}
			if want := "gauntlet:alpha handed off at " + path; !strings.Contains(b.BlockedReason, want) {
				t.Errorf("blocked reason = %q, want it to name the handoff %q", b.BlockedReason, want)
			}
			if strings.Contains(b.BlockedReason, genericBlockedReason) {
				t.Errorf("blocked reason = %q, must not describe a handoff as a gate failure", b.BlockedReason)
			}
			if strings.Contains(b.BlockedReason, "stopping at my context threshold") || strings.Contains(b.BlockedReason, "saved my place") {
				t.Errorf("blocked reason = %q, must not carry the builder's raw output", b.BlockedReason)
			}
		})
	}
}

// Scenario: A resolved HOLD is not the reason for a later no-verdict review —
// the HOLD and the no-verdict review are both real beta sessions, the second
// reached by resuming the run.
func TestBlockedReasonEndToEndAResumedNoVerdictReviewNamesTheMissingVerdict(t *testing.T) {
	env := newHoldEnv(t, 2, "", svcRepo("true"))
	env.script(t, "alpha", "ok")
	env.script(t, "beta", "hold:"+testGap, "none")
	runID := env.dispatchHeld(t, testGap)

	w := postJSON(t, env.mux, "/api/run-resume", fmt.Sprintf(`{"id":%q}`, runID))
	if w.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if status := env.waitIdle(t, runID); status != "failed" {
		t.Fatalf("resumed run settled %q, want failed", status)
	}

	reviews := phaseRecords(t, env.st, runID, "review", "agent")
	if len(reviews) != 2 {
		t.Fatalf("review records = %d, want 2 (the HOLD, then the resumed review); %+v", len(reviews), reviews)
	}
	if newest := reviews[len(reviews)-1]; newest.Status != "failed" || newest.Error != noVerdictError {
		t.Fatalf("resumed review = %s with error %q, want failed with %q", newest.Status, newest.Error, noVerdictError)
	}

	b := env.bullet(t, runID, "svc")
	if b.Status != "blocked" {
		t.Fatalf("bullet status = %q, want blocked", b.Status)
	}
	if want := "svc: review: " + noVerdictError; b.BlockedReason != want {
		t.Errorf("blocked reason = %q, want %q", b.BlockedReason, want)
	}
	if strings.Contains(b.BlockedReason, testGap) {
		t.Errorf("blocked reason = %q, must not repeat the resolved HOLD's gap", b.BlockedReason)
	}
	if strings.Contains(b.BlockedReason, betaReasoning) {
		t.Errorf("blocked reason = %q, must not carry the reviewer's raw output", b.BlockedReason)
	}
}
