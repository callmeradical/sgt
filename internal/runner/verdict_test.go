package runner

// Tests for slice beta-verdict of change claude-is-the-default-harness-and-
// runs-gauntlet-roles: design decision 6's verdict rules, at parseVerdict and
// at PhaseRunner.RunAgentPhase for a gauntlet:beta review run by a stub
// claude (specs/gauntlet-roles/spec.md, "Beta's verdict is read from its
// complete output", "A HOLD verdict records a failed review carrying the
// gap", "A review with no verdict is never a pass"; specs/agent-harness,
// "A judged failure is not summarised as completed").

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

func TestParseVerdictReadsTheLastVerdictLine(t *testing.T) {
	cases := []struct {
		name, out    string
		verdict, gap string
		ok           bool
	}{
		{"plain pass", "reviewed\nVERDICT: PASS\n", "pass", "", true},
		{"pass with trailing text", "reviewed\nVERDICT: PASS — meets the bar.\n", "pass", "", true},
		{"pass with colon text", "VERDICT: PASS: all scenarios covered", "pass", "", true},
		{"hold with gap", "VERDICT: HOLD: the empty-input scenario has no test", "hold", "the empty-input scenario has no test", true},
		{"hold gap is trimmed", "VERDICT: HOLD:   x  \n", "hold", "x", true},
		{"bold line", "**VERDICT: PASS**", "pass", "", true},
		{"bold hold", "**VERDICT: HOLD: missing test**", "hold", "missing test", true},
		{"underscore emphasis", "__VERDICT: HOLD: g__", "hold", "g", true},
		{"backticked line", "`VERDICT: PASS`", "pass", "", true},
		{"bold label only", "**VERDICT:** PASS", "pass", "", true},
		{"quote and bullet", "> - VERDICT: HOLD: q", "hold", "q", true},
		{"heading", "## VERDICT: PASS", "pass", "", true},
		{"trailing blank lines", "VERDICT: PASS\n\n  \n", "pass", "", true},
		{"crlf", "VERDICT: HOLD: y\r\n", "hold", "y", true},
		{"gap keeps inner code", "VERDICT: HOLD: `parse()` has no test", "hold", "`parse()` has no test", true},
		{"last verdict line wins", "quoted:\nVERDICT: PASS\nmore\nVERDICT: HOLD: x\n", "hold", "x", true},
		{"later prose does not hide it", "VERDICT: PASS\nthanks for reading\n", "pass", "", true},
		{"mid-sentence quote is not a verdict", "the contract is VERDICT: PASS, see above\n", "", "", false},
		{"no verdict", "I could not finish the review.\n", "", "", false},
		{"empty output", "", "", "", false},
		{"hold with empty gap", "VERDICT: HOLD", "", "", false},
		{"hold with colon and empty gap", "VERDICT: HOLD:   ", "", "", false},
		{"incomplete report", "VERDICT: INCOMPLETE — ran out of time", "", "", false},
		{"lowercase label", "verdict: pass", "", "", false},
		{"word that only starts with PASS", "VERDICT: PASSED", "", "", false},
		{"last verdict line is malformed", "VERDICT: PASS\nVERDICT: maybe\n", "", "", false},
		{"pass with en dash remark", "VERDICT: PASS – fine", "pass", "", true},
		{"pass with spaced hyphen remark", "VERDICT: PASS - fine", "pass", "", true},
		{"emphasised pass with remark", "**VERDICT: PASS** — meets the bar.", "pass", "", true},
		{"pass or hold alternative", "VERDICT: PASS/HOLD", "", "", false},
		{"pass or hold alternative with gap", "VERDICT: PASS/HOLD: <gap>", "", "", false},
		{"pass pipe hold", "VERDICT: PASS | HOLD", "", "", false},
		{"pass then changes its mind", "VERDICT: PASS — no wait, HOLD: missing test", "", "", false},
		{"quoted contract line", "`VERDICT: PASS`, or `VERDICT: HOLD: <gap>`", "", "", false},
		{"pass remark with a slash", "VERDICT: PASS — see a/b", "", "", false},
		{"pass remark naming pass", "VERDICT: PASS: PASS", "", "", false},
		{"pass glued to text", "VERDICT: PASS.", "", "", false},
		{"pass glued to a hyphen", "VERDICT: PASS-ish", "", "", false},
		{"hold quoting the contract", "VERDICT: HOLD: end with VERDICT: PASS", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verdict, gap, ok := parseVerdict(c.out)
			if verdict != c.verdict || gap != c.gap || ok != c.ok {
				t.Errorf("parseVerdict(%q) = (%q, %q, %v), want (%q, %q, %v)", c.out, verdict, gap, ok, c.verdict, c.gap, c.ok)
			}
		})
	}
}

// betaRunner builds a roles-on PhaseRunner whose agent is a stub named claude
// running body.
func betaRunner(t *testing.T, body string) (*PhaseRunner, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"
	return pr, st
}

func reviewEnvelopes(t *testing.T, st *store.Store) []store.EnvelopeRecord {
	t.Helper()
	envs, err := st.ListEnvelopesForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	var out []store.EnvelopeRecord
	for _, e := range envs {
		if e.Stage == "review" {
			out = append(out, e)
		}
	}
	return out
}

// Scenario: HOLD records a blocking finding carrying the gap.
func TestBetaHoldRecordsAFailedReviewCarryingTheGap(t *testing.T) {
	const gap = "the empty-input scenario has no test"
	pr, st := betaRunner(t, "echo 'looked at everything'\necho 'VERDICT: HOLD: "+gap+"'\nexit 0")

	_, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 2)
	if err == nil || err.Error() != "gauntlet:beta HOLD: "+gap {
		t.Fatalf("error = %v, want gauntlet:beta HOLD: %s", err, gap)
	}

	attempts := phaseAttempts(t, st, "review")
	if len(attempts) != 1 {
		t.Fatalf("review attempts = %d, want 1 (a HOLD is a judgement, not retried)", len(attempts))
	}
	rec := attempts[0]
	if rec.Status != "failed" || rec.Error != "gauntlet:beta HOLD: "+gap {
		t.Errorf("record status/error = %q/%q, want failed/gauntlet:beta HOLD: %s", rec.Status, rec.Error, gap)
	}
	m := payloadMap(t, rec.Payload)
	if m["gauntlet_verdict"] != "hold" || m["gauntlet_gap"] != gap {
		t.Errorf("payload verdict/gap = %v/%v, want hold/%s", m["gauntlet_verdict"], m["gauntlet_gap"], gap)
	}
	want := []handoff.ReviewFinding{{Axis: "gauntlet", Severity: "error", Summary: gap, Disposition: "hold"}}
	if got := handoff.ReviewFindings(rec.Payload); len(got) != 1 || got[0] != want[0] {
		t.Errorf("phase findings = %+v, want %+v", got, want)
	}
	if !handoff.HasBlockingFinding(handoff.ReviewFindings(rec.Payload)) {
		t.Error("phase payload has no blocking finding")
	}

	envs := reviewEnvelopes(t, st)
	if len(envs) != 1 {
		t.Fatalf("review envelopes = %d, want 1", len(envs))
	}
	if !handoff.HasBlockingFinding(handoff.ReviewFindings(envs[0].Data)) {
		t.Errorf("recorded review envelope %s has no blocking finding", envs[0].Data)
	}
	if want := "Phase review failed for svc: gauntlet:beta HOLD: " + gap; envs[0].Summary != want {
		t.Errorf("envelope summary = %q, want %q", envs[0].Summary, want)
	}
}

// The gap passes redact.Text before it reaches the error, gauntlet_gap or
// the finding.
func TestBetaHoldGapIsRedacted(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	pr, st := betaRunner(t, "echo 'VERDICT: HOLD: token "+secret+" is logged'\nexit 0")

	_, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %v, want a HOLD error without the secret", err)
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if strings.Contains(rec.Error, secret) || strings.Contains(string(rec.Payload), secret) {
		t.Errorf("record leaks the secret: error=%q payload=%s", rec.Error, rec.Payload)
	}
	gap, _ := payloadMap(t, rec.Payload)["gauntlet_gap"].(string)
	if !strings.Contains(gap, "is logged") {
		t.Errorf("gauntlet_gap = %q, want the redacted gap", gap)
	}
}

// Scenario: A verdict after the truncation limit is still read.
func TestBetaVerdictAfterTheTruncationLimitIsRead(t *testing.T) {
	body := "i=0; while [ $i -lt 2000 ]; do echo 'padding line padding line padding line padding line'; i=$((i+1)); done\necho 'VERDICT: PASS'\nexit 0"
	pr, st := betaRunner(t, body)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "passed" {
		t.Fatalf("review status = %q, want passed", rec.Status)
	}
	m := payloadMap(t, rec.Payload)
	if raw, _ := m["raw_output"].(string); strings.Contains(raw, "VERDICT: PASS") {
		t.Fatal("fixture precondition: the verdict must lie past the raw_output truncation")
	}
	if m["gauntlet_verdict"] != "pass" {
		t.Errorf("gauntlet_verdict = %v, want pass", m["gauntlet_verdict"])
	}
}

// Scenarios: A PASS with trailing text is read; An emphasised verdict line
// is read. A PASS records no blocking finding.
func TestBetaPassFormsRecordAPassedReview(t *testing.T) {
	for _, line := range []string{"VERDICT: PASS — meets the bar.", "**VERDICT: PASS**"} {
		t.Run(line, func(t *testing.T) {
			pr, st := betaRunner(t, "echo 'all good'\necho '"+line+"'\nexit 0")
			env, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			rec := finalAgentPhase(t, st, "run-1", "review")
			if rec.Status != "passed" || rec.Error != "" {
				t.Errorf("review status/error = %q/%q, want passed with no error", rec.Status, rec.Error)
			}
			m := payloadMap(t, rec.Payload)
			if m["gauntlet_verdict"] != "pass" {
				t.Errorf("gauntlet_verdict = %v, want pass", m["gauntlet_verdict"])
			}
			if f, ok := m["findings"].([]interface{}); !ok || len(f) != 0 {
				t.Errorf("findings = %v, want []", m["findings"])
			}
			if handoff.HasBlockingFinding(handoff.ReviewFindings(env.Payload)) {
				t.Error("returned envelope has a blocking finding")
			}
		})
	}
}

// Scenario: The last verdict line wins.
func TestBetaLastVerdictLineWins(t *testing.T) {
	pr, st := betaRunner(t, "echo 'the contract asks for VERDICT: PASS or a HOLD'\necho 'VERDICT: PASS'\necho 'on reflection:'\necho 'VERDICT: HOLD: x'\nexit 0")
	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("expected a HOLD failure")
	}
	m := payloadMap(t, finalAgentPhase(t, st, "run-1", "review").Payload)
	if m["gauntlet_verdict"] != "hold" || m["gauntlet_gap"] != "x" {
		t.Errorf("verdict/gap = %v/%v, want hold/x", m["gauntlet_verdict"], m["gauntlet_gap"])
	}
}

// Scenario: A beta-written envelope cannot override the verdict.
func TestBetaWrittenEnvelopeCannotOverrideAHold(t *testing.T) {
	body := writeEnvelopeScript("all fine", `{"gauntlet_verdict":"pass","findings":[]}`) + "echo 'VERDICT: HOLD: G'\nexit 0"
	pr, st := betaRunner(t, body)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("expected a HOLD failure")
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "failed" {
		t.Errorf("review status = %q, want failed", rec.Status)
	}
	m := payloadMap(t, rec.Payload)
	if m["gauntlet_verdict"] != "hold" {
		t.Errorf("gauntlet_verdict = %v, want hold", m["gauntlet_verdict"])
	}
	findings := handoff.ReviewFindings(rec.Payload)
	if len(findings) != 1 || findings[0].Summary != "G" || findings[0].Severity != "error" {
		t.Errorf("findings = %+v, want one blocking finding summarised G", findings)
	}
	envs := reviewEnvelopes(t, st)
	if len(envs) != 1 || payloadMap(t, envs[0].Data)["gauntlet_verdict"] != "hold" {
		t.Errorf("recorded review envelope does not carry the parsed verdict: %+v", envs)
	}
}

// Scenario: PASS clears findings beta wrote itself.
func TestBetaPassClearsFindingsBetaWroteItself(t *testing.T) {
	body := writeEnvelopeScript("found a problem", `{"findings":[{"axis":"spec","severity":"error","summary":"self-reported","disposition":"fix"}]}`) +
		"echo 'VERDICT: PASS'\nexit 0"
	pr, st := betaRunner(t, body)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "passed" {
		t.Errorf("review status = %q, want passed", rec.Status)
	}
	if f := handoff.ReviewFindings(rec.Payload); len(f) != 0 {
		t.Errorf("findings = %+v, want empty", f)
	}
	envs := reviewEnvelopes(t, st)
	if len(envs) != 1 || len(handoff.ReviewFindings(envs[0].Data)) != 0 {
		t.Errorf("recorded review envelope still carries beta's own findings: %+v", envs)
	}
}

// Scenario: No verdict is never a pass. Scenario (agent-harness): A judged
// failure is not summarised as completed.
func TestBetaWithNoVerdictIsAFailedReviewNotSummarisedAsCompleted(t *testing.T) {
	pr, st := betaRunner(t, "echo 'I ran out of room and could not finish.'\nexit 0")

	_, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0)
	const reason = "gauntlet:beta produced no PASS/HOLD verdict"
	if err == nil || err.Error() != reason {
		t.Fatalf("error = %v, want %q", err, reason)
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "failed" || rec.Error != reason {
		t.Errorf("review status/error = %q/%q, want failed/%s", rec.Status, rec.Error, reason)
	}
	envs := reviewEnvelopes(t, st)
	if len(envs) != 1 {
		t.Fatalf("review envelopes = %d, want 1", len(envs))
	}
	if s := envs[0].Summary; !strings.HasPrefix(s, "Phase review failed for") || strings.Contains(s, "completed") {
		t.Errorf("envelope summary = %q, want it to begin %q and not say completed", s, "Phase review failed for")
	}
}

// A beta that wrote its own verdict keys and findings but printed no verdict
// line records none of them: the verdict comes only from the output.
func TestBetaWrittenVerdictKeysDoNotSurviveAMissingVerdict(t *testing.T) {
	body := writeEnvelopeScript("held", `{"gauntlet_verdict":"hold","gauntlet_gap":"SELF-GAP","findings":[{"axis":"gauntlet","severity":"error","summary":"SELF-GAP","disposition":"hold"}]}`) +
		"echo 'no verdict line here'\nexit 0"
	pr, st := betaRunner(t, body)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("expected a no-verdict failure")
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if strings.Contains(string(rec.Payload), "SELF-GAP") {
		t.Errorf("phase payload keeps beta's own verdict keys: %s", rec.Payload)
	}
	for _, e := range reviewEnvelopes(t, st) {
		if strings.Contains(string(e.Data), "SELF-GAP") {
			t.Errorf("review envelope keeps beta's own verdict keys: %s", e.Data)
		}
	}
}

// Beta's "incomplete" report that quotes the contract but gives no verdict
// is no verdict: failed with the no-verdict error, and retried.
func TestBetaIncompleteReportQuotingTheContractIsNoVerdict(t *testing.T) {
	body := "echo 'I was asked to end with one of:'\n" +
		"echo '`VERDICT: PASS`, or `VERDICT: HOLD: <gap>`'\n" +
		"echo 'Status: incomplete — I ran out of budget before reading the tests.'\n" +
		"exit 0"
	pr, st := betaRunner(t, body)

	_, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 1)
	if err == nil || err.Error() != noVerdictReason {
		t.Fatalf("error = %v, want %q", err, noVerdictReason)
	}
	attempts := phaseAttempts(t, st, "review")
	if len(attempts) != 2 {
		t.Fatalf("review attempts = %d, want 2 (no verdict is retried)", len(attempts))
	}
	for _, a := range attempts {
		if a.Status != "failed" || a.Error != noVerdictReason {
			t.Errorf("attempt %d status/error = %q/%q, want failed/%s", a.Attempt, a.Status, a.Error, noVerdictReason)
		}
	}
}

// Scenario: A review with no verdict is retried.
func TestBetaWithNoVerdictIsRetried(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "second")
	body := "if [ -f " + marker + " ]; then echo 'VERDICT: PASS'; exit 0; fi\ntouch " + marker + "\necho 'no verdict'\nexit 0"
	pr, st := betaRunner(t, body)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	attempts := phaseAttempts(t, st, "review")
	if len(attempts) != 2 || attempts[0].Status != "failed" || attempts[1].Status != "passed" {
		t.Fatalf("review attempts = %+v, want failed then passed", attempts)
	}
}

// The router copy of a review envelope carries no raw_output, and
// envelope_latest.json keeps pointing at the last non-review phase; the
// store's envelope record keeps the full redacted payload.
func TestReviewEnvelopeRouterCopyHasNoRawOutput(t *testing.T) {
	dir := t.TempDir()
	body := "case \"$*\" in *gauntlet:beta*) echo 'BETA-REASONING'; echo 'VERDICT: HOLD: G';; *) echo 'built it';; esac\nexit 0"
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("review: expected a HOLD")
	}

	repoDir := filepath.Join(pr.Router.BaseDir, "svc")
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(repoDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "BETA-REASONING") {
			t.Errorf("router file %s carries beta's output", e.Name())
		}
	}

	reviewCopy, err := os.ReadFile(filepath.Join(repoDir, "envelope_review.json"))
	if err != nil {
		t.Fatalf("no router copy of the review envelope: %v", err)
	}
	var env handoff.Envelope
	if err := json.Unmarshal(reviewCopy, &env); err != nil {
		t.Fatal(err)
	}
	m := payloadMap(t, env.Payload)
	if _, has := m["raw_output"]; has {
		t.Error("router copy of the review envelope has raw_output")
	}
	if m["gauntlet_verdict"] != "hold" || m["gauntlet_gap"] != "G" || len(handoff.ReviewFindings(env.Payload)) != 1 {
		t.Errorf("router copy payload = %s, want the verdict, gap and finding", env.Payload)
	}

	latest, err := pr.Router.ReadLatestEnvelope("svc")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Stage != "build" {
		t.Errorf("envelope_latest.json stage = %q, want build (the last non-review phase)", latest.Stage)
	}

	envs := reviewEnvelopes(t, st)
	if len(envs) != 1 || !strings.Contains(string(envs[0].Data), "BETA-REASONING") {
		t.Errorf("store review envelope should keep the full redacted payload, got %+v", envs)
	}
}

// The router copy of a beta-written review envelope carries only sgt's
// verdict, gap and findings: neither beta's own summary nor its other
// payload keys travel to a downstream worktree.
func TestReviewRouterCopyCarriesOnlyTheVerdictGapAndFindings(t *testing.T) {
	body := writeEnvelopeScript("BETA-SUMMARY", `{"notes":"BETA-NOTES"}`) + "echo 'VERDICT: HOLD: G'\nexit 0"
	pr, _ := betaRunner(t, body)
	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("expected a HOLD")
	}

	data, err := os.ReadFile(filepath.Join(pr.Router.BaseDir, "svc", "envelope_review.json"))
	if err != nil {
		t.Fatalf("no router copy of the review envelope: %v", err)
	}
	for _, leaked := range []string{"BETA-SUMMARY", "BETA-NOTES"} {
		if strings.Contains(string(data), leaked) {
			t.Errorf("router copy carries %s: %s", leaked, data)
		}
	}
	var env handoff.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	m := payloadMap(t, env.Payload)
	if len(m) != 3 || m["gauntlet_verdict"] != "hold" || m["gauntlet_gap"] != "G" || len(handoff.ReviewFindings(env.Payload)) != 1 {
		t.Errorf("router copy payload = %s, want exactly the verdict, gap and findings", env.Payload)
	}
}

// A blocked_reason beta wrote into its own envelope is not the attempt's
// blocked reason: the verdict is beta's only judgement.
func TestBetaWrittenBlockedReasonIsNotReturned(t *testing.T) {
	body := writeEnvelopeScript("held", `{"blocked_reason":"BETA-BLOCKED"}`) + "echo 'VERDICT: HOLD: G'\nexit 0"
	pr, st := betaRunner(t, body)

	_, reason, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0)
	if err == nil {
		t.Fatal("expected a HOLD")
	}
	if reason != "" {
		t.Errorf("returned blocked reason = %q, want none", reason)
	}
	if got := handoff.BlockedReason(finalAgentPhase(t, st, "run-1", "review").Payload); got != "" {
		t.Errorf("recorded payload blocked_reason = %q, want none", got)
	}
}
