package runner

// Tests for the one point where design decision 1a's rules 4 and 5 (a gauntlet
// role's judgement) are consulted: after rule 3 and the envelope read, before
// rule 6. The judgements themselves arrive with later slices; these tests
// register a stand-in through roleJudges, the seam those slices use.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// withRoleJudge registers judge for role for the duration of the test,
// restoring whatever was registered before.
func withRoleJudge(t *testing.T, role string, judge func(roleAttempt) roleJudgement) *[]roleAttempt {
	t.Helper()
	var seen []roleAttempt
	prev, had := roleJudges[role]
	roleJudges[role] = func(a roleAttempt) roleJudgement {
		seen = append(seen, a)
		return judge(a)
	}
	t.Cleanup(func() {
		if had {
			roleJudges[role] = prev
		} else {
			delete(roleJudges, role)
		}
	})
	return &seen
}

// A judged failure of an attempt that wrote no envelope is summarised as
// failed with the judgement's reason, never as completed; a judgement that
// does not allow a retry is attempted once.
func TestJudgedFailureIsSummarisedAsFailedAndNotRetried(t *testing.T) {
	withRoleJudge(t, roleAlpha, func(roleAttempt) roleJudgement {
		return roleJudgement{Failed: true, Reason: "JUDGED-REASON"}
	})
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", "echo built\nexit 0"), 10*time.Second)
	pr.GauntletRoles = true

	_, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 2)
	if err == nil || err.Error() != "JUDGED-REASON" {
		t.Fatalf("error = %v, want JUDGED-REASON", err)
	}
	attempts := phaseAttempts(t, st, "build")
	if len(attempts) != 1 {
		t.Fatalf("build attempts = %d, want 1 (a judgement is not retried)", len(attempts))
	}
	if attempts[0].Status != "failed" || attempts[0].Error != "JUDGED-REASON" {
		t.Errorf("record status/error = %q/%q, want failed/JUDGED-REASON", attempts[0].Status, attempts[0].Error)
	}
	summaries := envelopeSummariesFor(t, st, "build")
	if len(summaries) != 1 {
		t.Fatalf("build envelopes = %q, want 1", summaries)
	}
	if want := "Phase build failed for svc: JUDGED-REASON"; summaries[0] != want {
		t.Errorf("summary = %q, want %q", summaries[0], want)
	}
}

// A judgement that allows a retry is retried like a process failure.
func TestRetryableJudgedFailureIsRetried(t *testing.T) {
	withRoleJudge(t, roleBeta, func(a roleAttempt) roleJudgement {
		if strings.Contains(a.Output, "GOOD") {
			return roleJudgement{}
		}
		return roleJudgement{Failed: true, Reason: "nothing to judge", Retry: true}
	})
	dir := t.TempDir()
	marker := dir + "/second"
	body := "if [ -f " + marker + " ]; then echo GOOD; exit 0; fi\ntouch " + marker + "\necho bad\nexit 0"
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)
	pr.GauntletRoles = true

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	attempts := phaseAttempts(t, st, "review")
	if len(attempts) != 2 || attempts[0].Status != "failed" || attempts[1].Status != "passed" {
		t.Fatalf("review attempts = %+v, want failed then passed", attempts)
	}
}

// Rule 3 wins: a process failure is never handed to a role judgement.
func TestProcessFailureIsNotJudged(t *testing.T) {
	seen := withRoleJudge(t, roleAlpha, func(roleAttempt) roleJudgement { return roleJudgement{} })
	for _, body := range []string{"echo out\nexit 1", "echo '" + terminationNotice + "'\nexit 0"} {
		dir := t.TempDir()
		pr, _ := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)
		pr.GauntletRoles = true
		if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err == nil {
			t.Fatalf("body %q: expected a process failure", body)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("judgement consulted %d times for process failures, want 0", len(*seen))
	}
}

// Only a phase that runs as the role is judged: roles off, or another harness,
// is never consulted.
func TestRoleLessAttemptIsNotJudged(t *testing.T) {
	seen := withRoleJudge(t, roleAlpha, func(roleAttempt) roleJudgement {
		return roleJudgement{Failed: true, Reason: "should not run"}
	})
	for _, c := range []struct {
		harness string
		roles   bool
	}{{"claude", false}, {"goose", true}} {
		dir := t.TempDir()
		pr, _ := newRunner(t, fakeAgent(t, dir, c.harness, "echo built\nexit 0"), 10*time.Second)
		pr.GauntletRoles = c.roles
		if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err != nil {
			t.Fatalf("%+v: unexpected error: %v", c, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("judgement consulted %d times, want 0", len(*seen))
	}
}

// The judgement reads the complete output, past the truncation applied to
// raw_output, and the attempt it judges.
func TestJudgementSeesTheCompleteOutputAndTheAttempt(t *testing.T) {
	seen := withRoleJudge(t, roleBeta, func(roleAttempt) roleJudgement { return roleJudgement{} })
	dir := t.TempDir()
	body := "i=0; while [ $i -lt 2000 ]; do echo 'padding line padding line padding line padding line'; i=$((i+1)); done\necho TAIL-MARKER\nexit 0"
	pr, _ := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)
	pr.GauntletRoles = true

	before := time.Now()
	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("judgement consulted %d times, want 1", len(*seen))
	}
	a := (*seen)[0]
	if len(a.Output) <= maxRawOutputBytes || !strings.HasSuffix(strings.TrimSpace(a.Output), "TAIL-MARKER") {
		t.Errorf("judged output is %d bytes ending %q, want the complete output ending TAIL-MARKER", len(a.Output), a.Output[max(0, len(a.Output)-40):])
	}
	if a.Phase != "review" || a.Role != roleBeta || a.Worktree != pr.Worktree {
		t.Errorf("judged attempt phase/role/worktree = %q/%q/%q", a.Phase, a.Role, a.Worktree)
	}
	if a.Started.Before(before) || a.Ended.Before(a.Started) {
		t.Errorf("attempt window %v..%v is not this attempt's", a.Started, a.Ended)
	}
}

// Keys a judgement merges replace the agent's own, are redacted, and are what
// rule 6 reads: clearing a review's findings lets it pass, while sgt's
// provenance still wins over everything.
func TestMergedKeysOverrideTheAgentsAndFeedRuleSix(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP"
	withRoleJudge(t, roleBeta, func(roleAttempt) roleJudgement {
		return roleJudgement{Merge: map[string]interface{}{
			"findings":     []interface{}{},
			"verdict_note": "gap mentions " + secret,
			"role":         "forged",
		}}
	})
	dir := t.TempDir()
	payload := `{"findings":[{"axis":"spec","severity":"error","summary":"agent-blocker"}],"own":"kept"}`
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", writeEnvelopeScript("beta wrote this", payload)+"exit 0"), 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "passed" {
		t.Errorf("review status = %q, want passed once the judgement cleared the findings", rec.Status)
	}
	m := payloadMap(t, rec.Payload)
	if f, _ := m["findings"].([]interface{}); len(f) != 0 {
		t.Errorf("findings = %v, want the judgement's empty list", m["findings"])
	}
	if m["own"] != "kept" {
		t.Errorf("agent's own key lost: %s", rec.Payload)
	}
	if strings.Contains(string(rec.Payload), secret) {
		t.Errorf("merged key was not redacted: %s", rec.Payload)
	}
	assertProvenance(t, rec.Payload, pr.AgentCLI, roleBeta, "0.1.0")

	// A judgement that passes the attempt but merges a blocking finding is
	// still failed by rule 6.
	withRoleJudge(t, roleBeta, func(roleAttempt) roleJudgement {
		return roleJudgement{Merge: map[string]interface{}{
			"findings": []interface{}{map[string]interface{}{"axis": "gauntlet", "severity": "error", "summary": "merged-blocker"}},
		}}
	})
	pr2, st2 := newRunner(t, fakeAgent(t, t.TempDir(), "claude", "echo reviewed\nexit 0"), 10*time.Second)
	pr2.GauntletRoles = true
	_, _, err := pr2.RunAgentPhase(context.Background(), "review", "review it", 0)
	if err == nil || err.Error() != "review reported blocking findings: merged-blocker" {
		t.Errorf("error = %v, want rule 6's reason for the merged finding", err)
	}
	if rec := finalAgentPhase(t, st2, "run-1", "review"); rec.Status != "failed" {
		t.Errorf("review status = %q, want failed", rec.Status)
	}
}

// mergePayloadKeys keeps a judgement's keys even when the agent's payload is
// not a JSON object.
func TestMergePayloadKeysReplacesANonObjectPayload(t *testing.T) {
	for _, payload := range []string{`null`, `[1,2]`, `"text"`, ``} {
		got := mergePayloadKeys(json.RawMessage(payload), map[string]interface{}{"k": "v"})
		var m map[string]interface{}
		if err := json.Unmarshal(got, &m); err != nil || m["k"] != "v" {
			t.Errorf("payload %q merged to %s, want an object with k=v", payload, got)
		}
	}
}
