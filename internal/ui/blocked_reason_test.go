package ui

// Slice blocked-reason (S4): verification-freshness, "A blocked reason says
// why the phase failed" (design decision 8a). Each test seeds the phase
// records and envelopes the runner writes — the same shapes and the same
// sgt-authored Error texts (decisions 1, 6 and 13) — and then drives the
// terminal seam, recordTerminalRun(runID, "failed"), reading the outcome
// back through Store.ListBulletsForIntent.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/callmeradical/sgt/internal/store"
)

// handoffError is decision 13's interim failure reason, verbatim.
func handoffError(path string) string {
	return "gauntlet:alpha handed off at " + path + "; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue"
}

// noVerdictError is decision 6's reason for a review with no verdict, verbatim.
const noVerdictError = "gauntlet:beta produced no PASS/HOLD verdict"

// blockedReasonFixture is terminalRunFixture with every path a terminal run
// writes to pointed at temp dirs.
func blockedReasonFixture(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	t.Setenv("SGT_CONFIG", t.TempDir())
	t.Setenv("SGT_WIKI_ROOT", t.TempDir())
	t.Setenv("SGT_ARTIFACTS_ROOT", t.TempDir())
	return terminalRunFixture(t, "pending", "pending")
}

// seeder writes records in a strictly increasing created_at order, the
// order the store lists them in.
type seeder struct {
	t  *testing.T
	st *store.Store
}

func (s seeder) tick() { time.Sleep(2 * time.Millisecond) }

// agentAttempt records one agent attempt as RunAgentPhase does: the phase
// record with its sgt-authored Error and payload, and the envelope carrying
// the attempt-1 phase id. It returns the phase id.
func (s seeder) agentAttempt(repo, phase, status, errText string, payload map[string]any) string {
	s.t.Helper()
	s.tick()
	id := fmt.Sprintf("%s-%s-%d", repo, phase, time.Now().UnixNano())
	data, err := json.Marshal(payload)
	if err != nil {
		s.t.Fatal(err)
	}
	summary := fmt.Sprintf("Phase %s completed for %s", phase, repo)
	if status == "failed" {
		summary = fmt.Sprintf("Phase %s failed for %s: %s", phase, repo, errText)
	}
	if err := s.st.RecordEnvelope(&store.EnvelopeRecord{
		ID: fmt.Sprintf("%s-%s-env-%d", repo, phase, time.Now().UnixNano()), RunID: "sgt-run", Repo: repo, Stage: phase,
		Summary: summary, Data: data, Type: "phase.completed", SchemaVersion: "1",
		Producer: "sgt/runner", CorrelationID: "sgt-run", PhaseID: id,
	}); err != nil {
		s.t.Fatal(err)
	}
	if err := s.st.RecordPhase(&store.PhaseRecord{
		ID: id, RunID: "sgt-run", Repo: repo, Name: phase, Kind: "agent",
		Status: status, Error: errText, Payload: data, Attempt: 1,
	}); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// gate records a code gate as RunCodeGate does.
func (s seeder) gate(repo, name, status, output string) {
	s.t.Helper()
	s.tick()
	errText := ""
	if status == "failed" {
		errText = "exit status 1: " + output
	}
	if err := s.st.RecordPhase(&store.PhaseRecord{
		ID: fmt.Sprintf("%s-%s-%d", repo, name, time.Now().UnixNano()), RunID: "sgt-run", Repo: repo, Name: name,
		Kind: "code", Status: status, Error: errText,
		Payload: []byte(fmt.Sprintf(`{"gate_name":%q,"passed":%t,"output":%q}`, name, status == "passed", output)),
	}); err != nil {
		s.t.Fatal(err)
	}
}

// roleProvenance is the provenance the runner merges into a role phase.
func roleProvenance(role string, p map[string]any) map[string]any {
	p["agent"] = "claude"
	p["role"] = role
	p["plugin_version"] = "0.1.0"
	return p
}

func holdPayload(gap string) map[string]any {
	return roleProvenance("gauntlet:beta", map[string]any{
		"gauntlet_verdict": "hold",
		"gauntlet_gap":     gap,
		"findings": []map[string]any{{
			"axis": "gauntlet", "severity": "error", "summary": gap, "disposition": "hold",
		}},
	})
}

func passPayload() map[string]any {
	return roleProvenance("gauntlet:beta", map[string]any{
		"gauntlet_verdict": "pass",
		"findings":         []map[string]any{},
	})
}

func rawOutputPayload(raw string) map[string]any {
	return map[string]any{
		"raw_output": raw, "agent": "claude", "attempt": "1/1",
		"worktree": "/fleet/sgt-run/api", "branch": "sgt/sgt-run",
	}
}

func blockedReasons(t *testing.T, st *store.Store) []string {
	t.Helper()
	bullets, err := st.ListBulletsForIntent("sgt-run-intent")
	if err != nil {
		t.Fatal(err)
	}
	if len(bullets) == 0 {
		t.Fatal("fixture has no bullets")
	}
	var out []string
	for i, b := range bullets {
		if b.Status != "blocked" {
			t.Errorf("bullet %d status = %q, want blocked", i+1, b.Status)
		}
		out = append(out, b.BlockedReason)
	}
	return out
}

// Scenario: A handoff failure is not described as a gate failure.
func TestBlockedReasonNamesAHandoffNotAGateFailure(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	path := "/home/op/.local/state/gauntlet/handoffs/claude-default-api.md"
	s.agentAttempt("api", "build", "failed", handoffError(path),
		roleProvenance("gauntlet:alpha", map[string]any{"handoff_path": path, "raw_output": "wrote handoff\nHANDOFF: " + path}))

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if !strings.HasPrefix(r, "api: build:") {
			t.Errorf("bullet %d blocked reason = %q, want it to begin %q", i+1, r, "api: build:")
		}
		if !strings.Contains(r, "handed off") {
			t.Errorf("bullet %d blocked reason = %q, want it to name the handoff", i+1, r)
		}
		if strings.Contains(r, "gates did not pass") {
			t.Errorf("bullet %d blocked reason = %q, must not describe a handoff as a gate failure", i+1, r)
		}
	}
}

// Scenario: A resolved HOLD is not the reason for a later no-verdict review.
func TestBlockedReasonIgnoresAResolvedHoldForALaterNoVerdictReview(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}
	const gap = "GAP-G retries are not bounded"

	s.agentAttempt("api", "build", "passed", "", roleProvenance("gauntlet:alpha", map[string]any{}))
	s.gate("api", "test", "passed", "ok")
	s.agentAttempt("api", "review", "failed", "gauntlet:beta HOLD: "+gap, holdPayload(gap))
	// The run is resumed and the fresh beta produces no verdict (retried once).
	s.agentAttempt("api", "review", "failed", noVerdictError, roleProvenance("gauntlet:beta", rawOutputPayload("I looked around; incomplete")))

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if !strings.Contains(r, "no PASS/HOLD verdict") {
			t.Errorf("bullet %d blocked reason = %q, want it to name the missing verdict", i+1, r)
		}
		if strings.Contains(r, gap) {
			t.Errorf("bullet %d blocked reason = %q, must not repeat the resolved HOLD's gap", i+1, r)
		}
	}
}

// Scenario: A resolved HOLD in one repo is not the reason for another repo's
// failure.
func TestBlockedReasonIgnoresAnotherReposResolvedHold(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}
	const gap = "GAP-G the webhook signature is never checked"

	s.agentAttempt("a", "build", "passed", "", roleProvenance("gauntlet:alpha", map[string]any{}))
	s.gate("a", "test", "passed", "ok")
	s.agentAttempt("a", "review", "failed", "gauntlet:beta HOLD: "+gap, holdPayload(gap))
	// run-fix corrects repo a: the fix passes, gates and review re-run green.
	s.agentAttempt("a", "fix", "passed", "", roleProvenance("gauntlet:alpha", map[string]any{}))
	s.gate("a", "test", "passed", "ok")
	s.agentAttempt("a", "review", "passed", "", passPayload())
	// The run is resumed and repo b's gate fails.
	s.agentAttempt("b", "build", "passed", "", roleProvenance("gauntlet:alpha", map[string]any{}))
	s.gate("b", "test", "failed", "FAIL TestWebhookRetry")

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if !strings.HasPrefix(r, "b: test:") {
			t.Errorf("bullet %d blocked reason = %q, want it to name repo b and its gate (%q)", i+1, r, "b: test:")
		}
		if strings.Contains(r, gap) {
			t.Errorf("bullet %d blocked reason = %q, must not repeat repo a's resolved HOLD gap", i+1, r)
		}
	}
}

// Scenario: An agent-reported reason still wins.
func TestBlockedReasonAgentReportedReasonStillWinsOverAGateFailure(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.agentAttempt("api", "build", "passed", "", map[string]any{"blocked_reason": "needs a human decision", "agent": "opencode"})
	s.gate("api", "test", "failed", "FAIL TestCheckout")

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if r != "needs a human decision" {
			t.Errorf("bullet %d blocked reason = %q, want %q", i+1, r, "needs a human decision")
		}
	}
}

// Scenario: A later current HOLD outranks an earlier agent reason.
func TestBlockedReasonLaterCurrentHoldOutranksAnEarlierAgentReason(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}
	const gap = "GAP-G no test covers the refund path"

	s.agentAttempt("api", "plan", "passed", "", map[string]any{"blocked_reason": "R", "agent": "claude"})
	s.agentAttempt("api", "build", "passed", "", roleProvenance("gauntlet:alpha", map[string]any{}))
	s.gate("api", "test", "passed", "ok")
	s.agentAttempt("api", "review", "failed", "gauntlet:beta HOLD: "+gap, holdPayload(gap))

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if r != gap {
			t.Errorf("bullet %d blocked reason = %q, want the current HOLD's gap %q", i+1, r, gap)
		}
	}
}

// Scenario: Raw output never reaches the reason.
func TestBlockedReasonNeverCarriesRawAgentOutput(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.agentAttempt("api", "build", "failed", "agent claude exited with error: exit status 1",
		rawOutputPayload("thinking...\nRAW-TRANSCRIPT the model's own words\n"))

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if strings.Contains(r, "RAW-TRANSCRIPT") {
			t.Errorf("bullet %d blocked reason = %q, leaked raw agent output", i+1, r)
		}
		if r != "api: build: agent claude exited with error: exit status 1" {
			t.Errorf("bullet %d blocked reason = %q, want the attempt's sgt-authored error with its repo and phase", i+1, r)
		}
	}
}

// Decision 8a: an envelope with no PhaseID, such as run.delivered (recorded
// with Stage "review"), is not a review envelope for freshness, so it never
// makes a current HOLD stale and never supplies a finding.
func TestBlockedReasonRunDeliveredEnvelopeDoesNotDisplaceACurrentHold(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}
	const gap = "GAP-G the migration is not reversible"

	s.agentAttempt("api", "review", "failed", "gauntlet:beta HOLD: "+gap, holdPayload(gap))
	s.tick()
	if err := st.RecordEnvelope(&store.EnvelopeRecord{
		ID: "delivery-sgt-run-1", RunID: "sgt-run", Repo: "api", Stage: "review",
		Summary: "1 commit on sgt/sgt-run", Data: []byte(`{"repo":"api","commits":1}`),
		Type: "run.delivered", SchemaVersion: "1", Producer: "sgt/ui", CorrelationID: "sgt-run",
	}); err != nil {
		t.Fatal(err)
	}

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if r != gap {
			t.Errorf("bullet %d blocked reason = %q, want the current HOLD's gap %q", i+1, r, gap)
		}
	}
}

// Decision 8a: a phase-less envelope with blocking findings in a repo that
// has review records is not a current review finding.
func TestBlockedReasonPhaselessFindingsDoNotCountOnceTheRepoHasReviewRecords(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.agentAttempt("api", "review", "failed", noVerdictError, roleProvenance("gauntlet:beta", rawOutputPayload("incomplete")))
	s.tick()
	if err := st.RecordEnvelope(&store.EnvelopeRecord{
		ID: "env-phaseless", RunID: "sgt-run", Repo: "api", Stage: "review",
		Data: []byte(`{"findings":[{"severity":"error","summary":"PHASELESS finding"}]}`),
		Type: "phase.completed", SchemaVersion: "1", Producer: "sgt/mcp", CorrelationID: "sgt-run",
	}); err != nil {
		t.Fatal(err)
	}

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if r != "api: review: "+noVerdictError {
			t.Errorf("bullet %d blocked reason = %q, want %q", i+1, r, "api: review: "+noVerdictError)
		}
	}
}

// Decision 8a: a failed record that a later record of the same repo, name
// and kind superseded is not current; the newest current one supplies the
// reason.
func TestBlockedReasonUsesTheNewestCurrentFailedPhase(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.gate("api", "lint", "failed", "LINT-OLD unused import")
	s.gate("api", "test", "failed", "TEST-CURRENT FAIL TestA")
	s.gate("api", "lint", "passed", "ok")

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if !strings.HasPrefix(r, "api: test: ") || !strings.Contains(r, "TEST-CURRENT") {
			t.Errorf("bullet %d blocked reason = %q, want the current failed test gate", i+1, r)
		}
	}
}

// Decision 8a: the phase reason, prefix included, is cut to 300 characters,
// on a character boundary.
func TestBlockedReasonFromAPhaseIsCutTo300Characters(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.gate("api", "test", "failed", strings.Repeat("é", 1000))

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if n := utf8.RuneCountInString(r); n != 300 {
			t.Errorf("bullet %d blocked reason has %d characters, want 300", i+1, n)
		}
		if !utf8.ValidString(r) {
			t.Errorf("bullet %d blocked reason is not valid UTF-8: %q", i+1, r)
		}
		if !strings.HasPrefix(r, "api: test: exit status 1: ") {
			t.Errorf("bullet %d blocked reason = %q, want the repo and phase prefix", i+1, r)
		}
	}
}

// Decision 8a: the generic text remains only for a failed run with no
// current failed phase record.
func TestBlockedReasonFallsBackToTheGenericTextWithNoCurrentFailedPhase(t *testing.T) {
	srv, st := blockedReasonFixture(t)
	s := seeder{t, st}

	s.gate("api", "test", "failed", "FAIL once")
	s.gate("api", "test", "passed", "ok")

	srv.recordTerminalRun("sgt-run", "failed")

	for i, r := range blockedReasons(t, st) {
		if r != genericBlockedReason {
			t.Errorf("bullet %d blocked reason = %q, want the generic text", i+1, r)
		}
	}
}
