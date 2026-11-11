package runner

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/redact"
)

// Design decision 1a fixes the order in which RunAgentPhase decides an
// attempt's outcome; the first rule that applies wins:
//
//  1. harness missing;
//  2. cancelled;
//  3. process failure — non-zero exit, budget timeout, or claude's
//     termination notice;
//  4. handoff, for gauntlet:alpha;
//  5. verdict, for gauntlet:beta;
//  6. blocking finding, for any review;
//  7. otherwise passed.
//
// Rules 1–3 are decided in RunAgentPhase before the agent's own envelope is
// read. judgeAttempt decides rules 4–6 for an attempt that got past them.

// roleAttempt is what a role judgement sees of one attempt that got past
// rule 3.
type roleAttempt struct {
	Phase    string
	Role     string
	Worktree string
	// Output is the attempt's complete stdout and stderr, ANSI-stripped but
	// neither truncated nor redacted, so a line at the very end of a long
	// session is still there to read. A judgement that copies any of it into
	// Merge or Reason gets it redacted by judgeAttempt.
	Output string
	// Started and Ended bracket this attempt's process.
	Started, Ended time.Time
	// Payload is the payload the attempt will record, already redacted: the
	// agent's own when it wrote an envelope, sgt's synthesized one otherwise.
	Payload json.RawMessage
}

// roleJudgement is a role's verdict on an attempt (rules 4 and 5). The zero
// value means the role found nothing to judge and decision 1a moves on to
// rule 6.
type roleJudgement struct {
	// Failed records the attempt failed, with Reason as its error.
	Failed bool
	Reason string
	// Retry lets a failure be retried like a process failure. A judgement
	// (a HOLD, a handoff) is not retried; an attempt that produced nothing to
	// judge (no verdict) is.
	Retry bool
	// Merge holds keys sgt writes over the attempt's payload whether or not
	// it failed. sgt's keys replace same-named keys the agent wrote, and rule
	// 6 reads the merged payload.
	Merge map[string]interface{}
}

// roleJudges maps a gauntlet role to its judgement: rule 4 (gauntlet:alpha's
// handoff) and rule 5 (gauntlet:beta's verdict). Each judgement registers
// itself from its own file's init, so RunAgentPhase never changes to add one.
// A role with no entry, and the empty role, are not judged.
var roleJudges = map[string]func(roleAttempt) roleJudgement{}

// attemptOutcome is how rules 4–6 decided an attempt.
type attemptOutcome struct {
	Failed bool
	Reason string
	Retry  bool
}

// judgeAttempt applies rules 4–6 of design decision 1a to an attempt that got
// past rule 3. It returns the payload the attempt records — a.Payload with any
// keys the role's judgement merged in, redacted — and the outcome.
func judgeAttempt(a roleAttempt) (json.RawMessage, attemptOutcome) {
	payload := a.Payload

	// Rules 4 and 5: the attempt's role judgement, when one is registered.
	if judge, ok := roleJudges[a.Role]; ok && a.Role != "" {
		j := judge(a)
		if len(j.Merge) > 0 {
			payload = redact.JSON(mergePayloadKeys(payload, j.Merge))
		}
		if j.Failed {
			return payload, attemptOutcome{Failed: true, Reason: redact.Text(j.Reason), Retry: j.Retry}
		}
	}

	// Rule 6: a review with a blocking finding failed, for every harness. It
	// is a judgement, so it is not retried.
	if a.Phase == "review" {
		if reason := blockingReviewReason(payload); reason != "" {
			return payload, attemptOutcome{Failed: true, Reason: reason}
		}
	}

	return payload, attemptOutcome{}
}

// blockingReviewReason returns "review reported blocking findings: " followed
// by the summaries of payload's severity "error" findings, joined by "; ", or
// "" when it has none.
func blockingReviewReason(payload json.RawMessage) string {
	var summaries []string
	for _, f := range handoff.ReviewFindings(payload) {
		if f.Severity == "error" {
			summaries = append(summaries, f.Summary)
		}
	}
	if len(summaries) == 0 {
		return ""
	}
	return "review reported blocking findings: " + strings.Join(summaries, "; ")
}

// mergePayloadKeys writes keys over payload, replacing same-named keys. A
// payload that is not a JSON object carries none of the keys a judgement
// records, so it is replaced by an object holding just keys rather than
// losing the judgement.
func mergePayloadKeys(payload json.RawMessage, keys map[string]interface{}) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil || m == nil {
		m = map[string]interface{}{}
	}
	for k, v := range keys {
		m[k] = v
	}
	return marshalPayload(m)
}
