package runner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/redact"
)

// Rule 5 of design decision 1a: a gauntlet:beta attempt that got past rule 3
// is decided by the PASS/HOLD verdict it ended its output with (design
// decision 6). The verdict is read from the complete output, never from the
// truncated raw_output, so a verdict at the very end of a long session is
// still seen.

// noVerdictReason is the error of a beta attempt whose output carried no
// usable verdict. It is not a judgement, so the attempt is retried.
const noVerdictReason = "gauntlet:beta produced no PASS/HOLD verdict"

func init() { roleJudges[roleBeta] = judgeVerdict }

// judgeVerdict records beta's verdict. sgt always writes gauntlet_verdict,
// gauntlet_gap and findings, and clears blocked_reason, replacing any
// same-named keys beta wrote into its own envelope, so the review record's
// judgement comes only from the verdict line:
//
//   - HOLD with a gap: failed with "gauntlet:beta HOLD: <gap>", one blocking
//     finding carrying the gap, and no retry;
//   - PASS: passed, with no findings;
//   - no verdict (including HOLD with an empty gap): failed and retried,
//     with the verdict keys empty, so nothing beta wrote itself stands in
//     for a verdict or becomes the bullet's blocked reason.
func judgeVerdict(a roleAttempt) roleJudgement {
	verdict, gap, ok := parseVerdict(a.Output)
	switch {
	case !ok:
		return roleJudgement{
			Failed: true,
			Reason: noVerdictReason,
			Retry:  true,
			Merge:  verdictKeys("", "", []handoff.ReviewFinding{}),
		}
	case verdict == "pass":
		return roleJudgement{Merge: verdictKeys("pass", "", []handoff.ReviewFinding{})}
	default:
		gap = redact.Text(gap)
		return roleJudgement{
			Failed: true,
			Reason: "gauntlet:beta HOLD: " + gap,
			Merge: verdictKeys("hold", gap, []handoff.ReviewFinding{{
				Axis: "gauntlet", Severity: "error", Summary: gap, Disposition: "hold",
			}}),
		}
	}
}

func verdictKeys(verdict, gap string, findings []handoff.ReviewFinding) map[string]interface{} {
	return map[string]interface{}{
		"gauntlet_verdict": verdict,
		"gauntlet_gap":     gap,
		"findings":         findings,
		"blocked_reason":   "",
	}
}

// verdictPattern matches a verdict line once its markdown decoration is
// stripped (verdictLine): VERDICT:, then PASS or HOLD, either optionally
// emphasised, then the rest of the line.
var verdictPattern = regexp.MustCompile(`^VERDICT:(?:\*\*|__)?\s*(?:\*\*|__)?(PASS|HOLD)(?:\*\*|__)?(.*)$`)

// passRemark matches what may follow PASS: a colon, an em or en dash, or a
// spaced hyphen, then a remark.
var passRemark = regexp.MustCompile(`^(?:\s*[:—–]|\s+-(?:\s|$))(.*)$`)

// holdGap matches what must follow HOLD: a colon, then the gap.
var holdGap = regexp.MustCompile(`^\s*:(.*)$`)

// verdictWord finds a PASS or HOLD token in a PASS remark, which makes the
// line an alternative or a change of mind rather than a verdict.
var verdictWord = regexp.MustCompile(`\b(?:PASS|HOLD)\b`)

// parseVerdict reads beta's verdict from its complete output.
//
// The rule: the last line that begins with "VERDICT:" (after verdictLine
// strips its decoration) decides, and it is a verdict only when it contains
// "VERDICT:" once and is either "VERDICT: PASS", optionally followed by a
// colon, an em or en dash, or a spaced hyphen and a remark that names
// neither PASS nor HOLD and contains no "|" or "/", or "VERDICT: HOLD: <gap>"
// with a non-empty gap.
//
// verdict is "pass" or "hold", and gap is a HOLD's gap. ok is false for
// everything else — no VERDICT: line, a quoted contract, an alternative such
// as "PASS/HOLD", or a HOLD with no gap — which is no verdict, never a pass.
// A verdict quoted earlier in the output never wins over the line beta
// ended with.
func parseVerdict(full string) (verdict, gap string, ok bool) {
	lines := strings.Split(full, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := verdictLine(lines[i])
		if !strings.HasPrefix(line, "VERDICT:") {
			continue
		}
		if strings.Count(line, "VERDICT:") != 1 {
			return "", "", false // a quoted contract, not a verdict
		}
		m := verdictPattern.FindStringSubmatch(line)
		if m == nil {
			return "", "", false
		}
		rest := m[2]
		if m[1] == "PASS" {
			if strings.TrimSpace(rest) == "" {
				return "pass", "", true
			}
			r := passRemark.FindStringSubmatch(rest)
			if r == nil || strings.ContainsAny(r[1], "|/") || verdictWord.MatchString(r[1]) {
				return "", "", false
			}
			return "pass", "", true
		}
		g := holdGap.FindStringSubmatch(rest)
		if g == nil || strings.TrimSpace(g[1]) == "" {
			return "", "", false
		}
		return "hold", strings.TrimSpace(g[1]), true
	}
	return "", "", false
}

// verdictLine strips the markdown a verdict line may be dressed in: leading
// quote, list and heading markers, whitespace and backticks, and emphasis
// (**, __ or a backtick) wrapping the whole line.
func verdictLine(line string) string {
	line = strings.TrimSpace(line)
	trimmed := line
	for {
		next := strings.TrimLeft(trimmed, ">-*#` \t")
		next = strings.TrimPrefix(next, "__")
		if next == trimmed {
			break
		}
		trimmed = next
	}
	lead := line[:len(line)-len(trimmed)]
	for _, mark := range []string{"**", "__", "`"} {
		if strings.Contains(lead, mark) {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, mark))
		}
	}
	return trimmed
}

// routerCopy is the envelope saved to the router for phase, whose files are
// copied into downstream worktrees (InjectHandoffToWorktree) where another
// repo's builder can read them. A review's copy carries only its verdict,
// gap and findings, under an sgt-authored summary, so nothing else a
// reviewer wrote or printed reaches another builder. It is staged "review"
// whatever stage an agent-written envelope named, so the router files it as
// a review and leaves envelope_latest.json alone. The store's envelope
// record keeps the full redacted payload for the operator. Every other
// phase's envelope is saved as is.
func routerCopy(phase string, env *handoff.Envelope) *handoff.Envelope {
	if phase != "review" {
		return env
	}
	kept := map[string]interface{}{}
	var m map[string]interface{}
	if err := json.Unmarshal(env.Payload, &m); err == nil {
		for _, k := range []string{"gauntlet_verdict", "gauntlet_gap", "findings"} {
			if v, ok := m[k]; ok {
				kept[k] = v
			}
		}
	}
	return &handoff.Envelope{
		TaskID:  env.TaskID,
		Repo:    env.Repo,
		Stage:   phase,
		Summary: fmt.Sprintf("Phase review for %s", env.Repo),
		Payload: marshalPayload(kept),
	}
}
