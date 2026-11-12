package runner

// Tests for slice handoff-detect of change claude-is-the-default-harness-and-
// runs-gauntlet-roles: specs/gauntlet-roles/spec.md, "A builder handoff is
// never recorded as a pass" (design decision 13, rule 4 of decision 1a).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

const wantHandoffContract = "If you hand off, end your output with exactly one line: HANDOFF: <absolute path>"

func handoffReason(path string) string {
	return "gauntlet:alpha handed off at " + path +
		"; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue"
}

// withHandoffsDir points HOME at a temp dir and returns the gauntlet handoffs
// directory under it, created.
func withHandoffsDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "state", "gauntlet", "handoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// alphaRunner is a roles-on runner whose harness is a stub claude running body.
func alphaRunner(t *testing.T, body string) (*PhaseRunner, *store.Store) {
	t.Helper()
	pr, st := newRunner(t, fakeAgent(t, t.TempDir(), "claude", body), 10*time.Second)
	pr.GauntletRoles = true
	return pr, st
}

// writeHandoffScript writes content, verbatim, to name under the handoffs
// directory the stub resolves from its own HOME.
func writeHandoffScript(name, content string) string {
	return "cat > \"$HOME/.local/state/gauntlet/handoffs/" + name + "\" <<'EOF'\n" + content + "\nEOF\n"
}

func payloadString(t *testing.T, raw json.RawMessage, key string) (string, bool) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// assertHandedOff checks the phase's single attempt was failed as a handoff at
// path, not retried, and never summarised as completed.
func assertHandedOff(t *testing.T, pr *PhaseRunner, st *store.Store, phase, path string) {
	t.Helper()
	_, _, err := pr.RunAgentPhase(context.Background(), phase, "do the work", 2)
	if err == nil {
		t.Fatalf("%s: RunAgentPhase returned nil error for a handed-off attempt", phase)
	}
	if err.Error() != handoffReason(path) {
		t.Errorf("%s: error = %q, want %q", phase, err, handoffReason(path))
	}
	attempts := phaseAttempts(t, st, phase)
	if len(attempts) != 1 {
		t.Fatalf("%s: attempts = %d, want 1 (a handoff is not retried)", phase, len(attempts))
	}
	rec := attempts[0]
	if rec.Status != "failed" {
		t.Errorf("%s: status = %q, want failed", phase, rec.Status)
	}
	if rec.Error != handoffReason(path) {
		t.Errorf("%s: record error = %q, want %q", phase, rec.Error, handoffReason(path))
	}
	if got, ok := payloadString(t, rec.Payload, "handoff_path"); !ok || got != path {
		t.Errorf("%s: payload handoff_path = %q (present %v), want %q", phase, got, ok, path)
	}
	for _, s := range envelopeSummariesFor(t, st, phase) {
		if strings.Contains(s, "completed") {
			t.Errorf("%s: envelope summary %q describes a handed-off attempt as completed", phase, s)
		}
	}
	if want := "Phase " + phase + " failed for svc: " + handoffReason(path); len(envelopeSummariesFor(t, st, phase)) != 1 || envelopeSummariesFor(t, st, phase)[0] != want {
		t.Errorf("%s: envelope summaries = %q, want [%q]", phase, envelopeSummariesFor(t, st, phase), want)
	}
}

func assertPassed(t *testing.T, pr *PhaseRunner, st *store.Store, phase string) {
	t.Helper()
	if _, _, err := pr.RunAgentPhase(context.Background(), phase, "do the work", 2); err != nil {
		t.Fatalf("%s: RunAgentPhase error = %v, want a pass", phase, err)
	}
	attempts := phaseAttempts(t, st, phase)
	if len(attempts) != 1 || attempts[0].Status != "passed" {
		t.Fatalf("%s: attempts = %+v, want one passed", phase, attempts)
	}
	if _, ok := payloadString(t, attempts[0].Payload, "handoff_path"); ok {
		t.Errorf("%s: passed attempt's payload carries handoff_path: %s", phase, attempts[0].Payload)
	}
}

// Scenario: A reported handoff is not a pass.
func TestReportedHandoffIsNotAPass(t *testing.T) {
	for _, phase := range []string{"build", "fix"} {
		t.Run(phase, func(t *testing.T) {
			withHandoffsDir(t)
			h := filepath.Join(t.TempDir(), "x.md")
			if err := os.WriteFile(h, []byte("# handoff\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			pr, st := alphaRunner(t, "echo 'stopping at my context threshold'\necho 'HANDOFF: "+h+"'\nexit 0")
			assertHandedOff(t, pr, st, phase, h)
		})
	}
}

// A HANDOFF: line counts within the last three non-empty lines, with blank
// lines and trailing whitespace around it, and names a handoff even when the
// file it names does not exist (R11).
func TestHandoffLineWithinTheLastThreeNonEmptyLinesIsAHandoff(t *testing.T) {
	withHandoffsDir(t)
	pr, st := alphaRunner(t, "echo work\necho 'HANDOFF:   /h/missing.md  '\necho\necho 'one more'\necho\necho 'and another'\nexit 0")
	assertHandedOff(t, pr, st, "build", "/h/missing.md")
}

// A HANDOFF: line further back than the last three non-empty lines, or one
// that does not name an absolute path, is not a handoff.
func TestHandoffLineOutsideTheContractIsNotAHandoff(t *testing.T) {
	cases := map[string]string{
		"too far back":  "echo 'HANDOFF: /h/x.md'\necho a\necho b\necho c\nexit 0",
		"relative path": "echo 'HANDOFF: h/x.md'\nexit 0",
		"mid-line":      "echo 'I will write HANDOFF: /h/x.md if I stop'\nexit 0",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			withHandoffsDir(t)
			pr, st := alphaRunner(t, body)
			assertPassed(t, pr, st, "build")
		})
	}
}

// Scenario: An unreported handoff is still not a pass.
func TestUnreportedHandoffIsStillNotAPass(t *testing.T) {
	for _, phase := range []string{"build", "fix"} {
		t.Run(phase, func(t *testing.T) {
			dir := withHandoffsDir(t)
			pr, st := alphaRunner(t, "")
			pr.AgentCLI = fakeAgent(t, t.TempDir(), "claude", writeHandoffScript("alpha-1.md", "Worktree: "+pr.Worktree+"\nBranch: x")+"echo done\nexit 0")
			assertHandedOff(t, pr, st, phase, filepath.Join(dir, "alpha-1.md"))
		})
	}
}

// claude reports its working directory with symlinks resolved, so a handoff
// it writes may name the worktree's real path rather than the one sgt holds.
func TestUnreportedHandoffNamingTheResolvedWorktreePathIsNotAPass(t *testing.T) {
	dir := withHandoffsDir(t)
	pr, st := alphaRunner(t, "echo \"Worktree: $(pwd -P)\" > \"$HOME/.local/state/gauntlet/handoffs/alpha-1.md\"\nexit 0")
	// Reach the worktree through a symlink whose resolved path does not
	// contain the linked one, so only the resolved form can match.
	base := t.TempDir()
	if err := os.Symlink(filepath.Dir(pr.Worktree), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	pr.Worktree = filepath.Join(base, "link", filepath.Base(pr.Worktree))
	assertHandedOff(t, pr, st, "build", filepath.Join(dir, "alpha-1.md"))
}

// Scenario: A backticked worktree path in a handoff file is a match; and the
// other whole-path forms decision 13 lists.
func TestWorktreePathInAHandoffFileMatchesAsAWholePath(t *testing.T) {
	cases := map[string]string{
		"backticked":     "`WT`, branch `x`",
		"trailing dot":   "Worktree is WT.",
		"comma":          "WT, branch x",
		"bracket":        "[WT]",
		"colon":          "WT: clean",
		"subpath":        "edited WT/sub/a.go",
		"end of file":    "WT",
		"after sibling":  "not WT-api but WT here",
		"dot then space": "WT. Next",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			dir := withHandoffsDir(t)
			pr, st := alphaRunner(t, "")
			pr.AgentCLI = fakeAgent(t, t.TempDir(), "claude", writeHandoffScript("h.md", strings.ReplaceAll(line, "WT", pr.Worktree))+"exit 0")
			assertHandedOff(t, pr, st, "build", filepath.Join(dir, "h.md"))
		})
	}
}

// Scenario: A sibling worktree's path is not a match; nor is any other path
// the worktree's is only a prefix of.
func TestSiblingWorktreePathIsNotAMatch(t *testing.T) {
	cases := map[string]string{
		"hyphen sibling":  "WT-api",
		"dotted sibling":  "WT.bak",
		"underscore":      "WT_old",
		"digit":           "WT2",
		"letter":          "WTx",
		"dot then digit":  "WT.2",
		"sibling in tick": "`WT-api`, branch `x`",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			withHandoffsDir(t)
			pr, st := alphaRunner(t, "")
			pr.AgentCLI = fakeAgent(t, t.TempDir(), "claude", writeHandoffScript("h.md", strings.ReplaceAll(line, "WT", pr.Worktree))+"exit 0")
			assertPassed(t, pr, st, "build")
		})
	}
}

// Scenario: Another session's handoff file is ignored.
func TestAnotherSessionsHandoffFileIsIgnored(t *testing.T) {
	withHandoffsDir(t)
	pr, st := alphaRunner(t, writeHandoffScript("other.md", "Worktree: /fleet/other-run/svc\nBranch: y")+"exit 0")
	assertPassed(t, pr, st, "build")
}

// A handoff file naming the worktree that was last modified before the
// attempt started was not written during it, so it is not this attempt's
// handoff.
func TestHandoffFileModifiedBeforeTheAttemptIsIgnored(t *testing.T) {
	dir := withHandoffsDir(t)
	pr, st := alphaRunner(t, "echo done\nexit 0")
	old := filepath.Join(dir, "old.md")
	if err := os.WriteFile(old, []byte("Worktree: "+pr.Worktree+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	assertPassed(t, pr, st, "build")
}

// If several handoff files written during the attempt name the worktree, the
// most recently modified one is the handoff.
func TestMostRecentlyModifiedMatchingHandoffFileWins(t *testing.T) {
	dir := withHandoffsDir(t)
	pr, st := alphaRunner(t, "")
	body := writeHandoffScript("first.md", "Worktree: "+pr.Worktree) +
		"sleep 0.2\n" +
		writeHandoffScript("second.md", "Worktree: "+pr.Worktree) +
		"sleep 0.2\n" +
		writeHandoffScript("unrelated.md", "Worktree: /fleet/other/svc") +
		"exit 0"
	pr.AgentCLI = fakeAgent(t, t.TempDir(), "claude", body)
	assertHandedOff(t, pr, st, "build", filepath.Join(dir, "second.md"))
}

// A reported HANDOFF: line is the handoff even when a handoff file written
// during the attempt also names the worktree.
func TestReportedHandoffLineTakesPrecedenceOverAHandoffFile(t *testing.T) {
	withHandoffsDir(t)
	pr, st := alphaRunner(t, "")
	pr.AgentCLI = fakeAgent(t, t.TempDir(), "claude", writeHandoffScript("h.md", "Worktree: "+pr.Worktree)+"echo 'HANDOFF: /h/reported.md'\nexit 0")
	assertHandedOff(t, pr, st, "build", "/h/reported.md")
}

// Handoff detection is rule 4: only gauntlet:alpha attempts that got past
// rule 3 are judged. A process failure keeps its own reason; a review, a
// roles-off build, and a non-claude build are never judged as handoffs.
func TestHandoffIsJudgedOnlyForAlphaAttemptsThatExitedCleanly(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		withHandoffsDir(t)
		pr, st := alphaRunner(t, "echo 'HANDOFF: /h/x.md'\nexit 3")
		_, _, err := pr.RunAgentPhase(context.Background(), "build", "do it", 0)
		if err == nil || strings.Contains(err.Error(), "handed off") {
			t.Fatalf("error = %v, want the process failure, not a handoff", err)
		}
		attempts := phaseAttempts(t, st, "build")
		if len(attempts) != 1 || !strings.Contains(attempts[0].Error, "exited with error") {
			t.Fatalf("attempts = %+v, want one process failure", attempts)
		}
		if _, ok := payloadString(t, attempts[0].Payload, "handoff_path"); ok {
			t.Errorf("process failure's payload carries handoff_path: %s", attempts[0].Payload)
		}
	})
	t.Run("roles off", func(t *testing.T) {
		withHandoffsDir(t)
		pr, st := alphaRunner(t, "echo 'HANDOFF: /h/x.md'\nexit 0")
		pr.GauntletRoles = false
		assertPassed(t, pr, st, "build")
	})
	t.Run("not claude", func(t *testing.T) {
		withHandoffsDir(t)
		pr, st := alphaRunner(t, "")
		pr.AgentCLI = fakeAgent(t, t.TempDir(), "goose", "echo 'HANDOFF: /h/x.md'\nexit 0")
		assertPassed(t, pr, st, "build")
	})
	t.Run("plan phase", func(t *testing.T) {
		withHandoffsDir(t)
		pr, st := alphaRunner(t, "echo 'HANDOFF: /h/x.md'\nexit 0")
		assertPassed(t, pr, st, "plan")
	})
}

// captureLastArgScript saves the stub's final argument (claude's prompt) to
// path.
func captureLastArgScript(path string) string {
	return "for a; do last=$a; done\nprintf '%s' \"$last\" > '" + path + "'\n"
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	return s[strings.LastIndex(s, "\n")+1:]
}

// Scenario: The handoff contract is the last line of every alpha prompt —
// after the plan progress instructions when the worktree has .sgt/plan.json.
func TestHandoffContractIsTheLastLineOfEveryAlphaPrompt(t *testing.T) {
	for _, tc := range []struct {
		phase string
		plan  bool
	}{{"fix", true}, {"build", true}, {"build", false}, {"fix", false}} {
		name := tc.phase
		if tc.plan {
			name += " with plan"
		}
		t.Run(name, func(t *testing.T) {
			withHandoffsDir(t)
			capture := filepath.Join(t.TempDir(), "prompt.txt")
			pr, _ := alphaRunner(t, captureLastArgScript(capture)+"exit 0")
			if tc.plan {
				if err := os.MkdirAll(filepath.Join(pr.Worktree, ".sgt"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(pr.Worktree, ".sgt", "plan.json"), []byte(`{"tasks":[]}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := pr.RunAgentPhase(context.Background(), tc.phase, "the task\nwith lines", 0); err != nil {
				t.Fatalf("RunAgentPhase: %v", err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			prompt := string(data)
			if got := lastLine(prompt); got != wantHandoffContract {
				t.Errorf("final prompt line = %q, want %q", got, wantHandoffContract)
			}
			if strings.Count(prompt, wantHandoffContract) != 1 {
				t.Errorf("prompt carries the contract %d times, want once", strings.Count(prompt, wantHandoffContract))
			}
			if !strings.HasPrefix(prompt, "the task\nwith lines") {
				t.Errorf("prompt %q does not start with the caller's prompt", prompt)
			}
			if tc.plan {
				planAt := strings.Index(prompt, strings.TrimSpace(planPromptExtension))
				if planAt < 0 {
					t.Fatalf("prompt lacks the plan progress instructions: %q", prompt)
				}
				if planAt > strings.Index(prompt, wantHandoffContract) {
					t.Errorf("plan progress instructions follow the handoff contract")
				}
			}
		})
	}
}

// Only gauntlet:alpha prompts carry the contract: a beta review, a role-less
// phase, and a roles-off build are prompted as before.
func TestHandoffContractIsOnlyOnAlphaPrompts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase string
		roles bool
	}{{"review as beta", "review", true}, {"plan", "plan", true}, {"roles off build", "build", false}} {
		t.Run(tc.name, func(t *testing.T) {
			withHandoffsDir(t)
			capture := filepath.Join(t.TempDir(), "prompt.txt")
			pr, _ := alphaRunner(t, captureLastArgScript(capture)+"echo 'VERDICT: PASS'\nexit 0")
			pr.GauntletRoles = tc.roles
			_, _, _ = pr.RunAgentPhase(context.Background(), tc.phase, "the task", 0)
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "the task" {
				t.Errorf("%s prompt = %q, want the caller's prompt unchanged", tc.phase, data)
			}
		})
	}
}

// A handoff file naming the worktree whose modification time is after the
// attempt ended was not written during it, so it is not this attempt's
// handoff.
func TestHandoffFileModifiedAfterTheAttemptIsIgnored(t *testing.T) {
	dir := withHandoffsDir(t)
	pr, st := alphaRunner(t, "echo done\nexit 0")
	later := filepath.Join(dir, "later.md")
	if err := os.WriteFile(later, []byte("Worktree: "+pr.Worktree+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(later, future, future); err != nil {
		t.Fatal(err)
	}
	assertPassed(t, pr, st, "build")
}

// The contract ends the prompt in every form it is delivered in: the role
// phase's per-attempt prompt file, and stdin when the prompt is too long for
// an argument.
func TestHandoffContractEndsEveryDeliveredAlphaPrompt(t *testing.T) {
	t.Run("prompt file", func(t *testing.T) {
		withHandoffsDir(t)
		pr, _ := alphaRunner(t, "echo done\nexit 0")
		if _, _, err := pr.RunAgentPhase(context.Background(), "build", "the task", 0); err != nil {
			t.Fatalf("RunAgentPhase: %v", err)
		}
		data, err := os.ReadFile(pr.rolePromptPath("build", 1))
		if err != nil {
			t.Fatal(err)
		}
		if got := lastLine(string(data)); got != wantHandoffContract {
			t.Errorf("prompt file's final line = %q, want %q", got, wantHandoffContract)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		withHandoffsDir(t)
		capture := filepath.Join(t.TempDir(), "stdin.txt")
		pr, _ := alphaRunner(t, "cat > '"+capture+"'\nexit 0")
		long := strings.Repeat("a long brief line\n", 8*1024) // > 64 KiB
		if _, _, err := pr.RunAgentPhase(context.Background(), "build", long, 0); err != nil {
			t.Fatalf("RunAgentPhase: %v", err)
		}
		data, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) <= len(long) {
			t.Fatalf("stdin carried %d bytes, want the %d-byte prompt plus the contract", len(data), len(long))
		}
		if got := lastLine(string(data)); got != wantHandoffContract {
			t.Errorf("stdin prompt's final line = %q, want %q", got, wantHandoffContract)
		}
	})
}
