package runner

import (
	"encoding/json"
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
// gauntlet_gap and findings, replacing any same-named keys beta wrote into
// its own envelope, so the review record's judgement comes only from the
// verdict line:
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
	}
}

// verdictPattern matches a verdict line once its markdown decoration is
// stripped (verdictLine): VERDICT:, then PASS or HOLD, either optionally
// emphasised, then the rest of the line.
var verdictPattern = regexp.MustCompile(`^VERDICT:(?:\*\*|__)?\s*(?:\*\*|__)?(PASS|HOLD)(?:\*\*|__)?(.*)$`)

// parseVerdict reads beta's verdict from its complete output. The last line
// that begins with "VERDICT:" (after verdictLine strips its decoration)
// decides, so a verdict quoted earlier in the output never wins over the one
// beta ended with.
//
// verdict is "pass" or "hold", and gap is a HOLD's gap. ok is false when no
// line begins with "VERDICT:", or when the last one is not a PASS or a HOLD
// with a non-empty gap — that is no verdict, never a pass. A PASS may be
// followed by any text ("VERDICT: PASS — meets the bar."); a HOLD's gap
// follows a colon.
func parseVerdict(full string) (verdict, gap string, ok bool) {
	lines := strings.Split(full, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := verdictLine(lines[i])
		if !strings.HasPrefix(line, "VERDICT:") {
			continue
		}
		m := verdictPattern.FindStringSubmatch(line)
		if m == nil {
			return "", "", false
		}
		rest := strings.TrimSpace(m[2])
		if rest != "" && isWordByte(rest[0]) {
			return "", "", false // VERDICT: PASSED, VERDICT: HOLDING
		}
		if m[1] == "PASS" {
			return "pass", "", true
		}
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		gap = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		if gap == "" {
			return "", "", false
		}
		return "hold", gap, true
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

func isWordByte(b byte) bool {
	return b == '_' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// routerCopy is the envelope saved to the router for phase, whose files are
// copied into downstream worktrees (InjectHandoffToWorktree) where another
// repo's builder can read them. A review's copy carries no raw_output, so a
// reviewer's output never reaches another builder; only its verdict, gap and
// findings travel. It is staged "review" whatever stage an agent-written
// envelope named, so the router files it as a review and leaves
// envelope_latest.json alone. The store's envelope record keeps the full
// redacted payload for the operator. Every other phase's envelope is saved
// as is.
func routerCopy(phase string, env *handoff.Envelope) *handoff.Envelope {
	if phase != "review" {
		return env
	}
	c := *env
	c.Stage = phase
	var m map[string]interface{}
	if err := json.Unmarshal(env.Payload, &m); err == nil && m != nil {
		delete(m, "raw_output")
		c.Payload = marshalPayload(m)
	}
	return &c
}
