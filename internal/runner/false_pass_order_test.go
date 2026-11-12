package runner

// Tests for slice false-pass-order of change claude-is-the-default-harness-
// and-runs-gauntlet-roles: specs/agent-harness/spec.md ("claude's
// background-wait ceiling is disabled", "claude sessions share no auto
// memory", "A cut-off claude session never records a passed phase", "A phase
// never adopts another phase's envelope", "Process failure decides an attempt
// before its output does") and specs/gauntlet-roles/spec.md ("A roles-off
// blocking review keeps its own findings", "A roles-off blocking review is
// not retried"), decided in design decision 1a's order.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

func envValue(env []string, key string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			val, found = v, true // last value wins, as in os/exec
		}
	}
	return val, found
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// Scenario: The claude command carries an unbounded background-wait ceiling.
func TestClaudeCommandCarriesAnUnboundedBackgroundWaitCeiling(t *testing.T) {
	_, _, env := BuildAgentCommand("claude", "", "do it")
	if v, ok := envValue(env, "CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS"); !ok || v != "0" {
		t.Errorf("claude env %q: CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS = %q (present %v), want 0", env, v, ok)
	}
}

// Scenario: The claude command disables auto memory.
func TestClaudeCommandDisablesAutoMemoryAndSessionPersistence(t *testing.T) {
	for _, role := range []string{"", roleAlpha, roleBeta} {
		_, args, env, _ := BuildAgentCommandForRole("claude", "m", role, "do it")
		if v, ok := envValue(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY"); !ok || v != "1" {
			t.Errorf("role %q: claude env %q: CLAUDE_CODE_DISABLE_AUTO_MEMORY = %q (present %v), want 1", role, env, v, ok)
		}
		if !hasArg(args, "--no-session-persistence") {
			t.Errorf("role %q: claude args %q lack --no-session-persistence", role, args)
		}
		if args[len(args)-1] != "do it" {
			t.Errorf("role %q: claude args %q do not end with the prompt", role, args)
		}
	}
}

// Neither variable nor the flag reaches another harness.
func TestOtherHarnessesGetNoClaudeSessionSettings(t *testing.T) {
	for _, agent := range []string{"opencode", "goose", "codex", "pi", "copilot"} {
		_, args, env := BuildAgentCommand(agent, "", "do it")
		for _, key := range []string{"CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS", "CLAUDE_CODE_DISABLE_AUTO_MEMORY"} {
			if _, ok := envValue(env, key); ok {
				t.Errorf("%s env %q carries %s", agent, env, key)
			}
		}
		if hasArg(args, "--no-session-persistence") {
			t.Errorf("%s args %q carry --no-session-persistence", agent, args)
		}
	}
}

// Scenarios: An inherited ceiling is overridden; An inherited auto-memory
// setting is overridden.
func TestClaudePhaseOverridesInheritedSessionSettings(t *testing.T) {
	t.Setenv("CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS", "600000")
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "0")
	dir := t.TempDir()
	agent := fakeAgent(t, dir, "claude", `echo "ceiling=$CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS memory=$CLAUDE_CODE_DISABLE_AUTO_MEMORY"`)
	pr, st := newRunner(t, agent, 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := finalAgentPhase(t, st, "run-1", "build")
	raw, _ := payloadMap(t, rec.Payload)["raw_output"].(string)
	if !strings.Contains(raw, "ceiling=0 ") {
		t.Errorf("agent observed %q, want CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0", raw)
	}
	if !strings.Contains(raw, "memory=1") {
		t.Errorf("agent observed %q, want CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", raw)
	}
}

const terminationNotice = "Background tasks still running after 600s; terminating. Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely."

// Scenario: A zero exit with the termination notice is a failed phase.
func TestClaudeZeroExitWithTerminationNoticeIsAFailedPhase(t *testing.T) {
	dir := t.TempDir()
	agent := fakeAgent(t, dir, "claude", "echo 'did some work'\necho '"+terminationNotice+"'\nexit 0")
	pr, st := newRunner(t, agent, 10*time.Second)

	_, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0)
	if err == nil {
		t.Fatal("RunAgentPhase returned nil error for a session claude terminated")
	}
	rec := finalAgentPhase(t, st, "run-1", "build")
	if rec.Status != "failed" {
		t.Errorf("phase status = %q, want failed", rec.Status)
	}
	const want = "agent claude terminated with background tasks still running"
	if rec.Error != want {
		t.Errorf("phase error = %q, want %q", rec.Error, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("returned error %q does not name the termination", err)
	}
}

// claude 2.1.322 words the notice differently from the earlier
// "after <N>s; terminating." form, and writes it to stderr. Both forms are a
// termination (strings in the 2.1.322 binary: "Background tasks still running
// <duration> after the last turn (<tasks>); stopping them.").
const terminationNotice2_1_322 = `Background tasks still running 10m after the last turn (agent "zeta"); stopping them. Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely.`

// Scenario: A zero exit with the termination notice is a failed phase
// (claude 2.1.322 wording, on stderr).
func TestClaudeZeroExitWithTheCurrentTerminationNoticeIsAFailedPhase(t *testing.T) {
	dir := t.TempDir()
	agent := fakeAgent(t, dir, "claude", "echo 'did some work'\necho '"+terminationNotice2_1_322+"' >&2\nexit 0")
	pr, st := newRunner(t, agent, 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err == nil {
		t.Fatal("RunAgentPhase returned nil error for a session claude 2.1.322 stopped")
	}
	rec := finalAgentPhase(t, st, "run-1", "build")
	if rec.Status != "failed" {
		t.Errorf("phase status = %q, want failed", rec.Status)
	}
	if want := "agent claude terminated with background tasks still running"; rec.Error != want {
		t.Errorf("phase error = %q, want %q", rec.Error, want)
	}
}

// The notice counts on any of the last three non-empty lines, followed only
// by blank lines or up to two more lines.
func TestClaudeTerminationNoticeNearTheEndIsDetected(t *testing.T) {
	dir := t.TempDir()
	agent := fakeAgent(t, dir, "claude", "echo '"+terminationNotice+"'\necho ''\necho 'one'\necho '   '\necho 'two'\nexit 0")
	pr, st := newRunner(t, agent, 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err == nil {
		t.Fatal("RunAgentPhase returned nil error with the notice on the third-last non-empty line")
	}
	if rec := finalAgentPhase(t, st, "run-1", "build"); rec.Status != "failed" {
		t.Errorf("phase status = %q, want failed", rec.Status)
	}
}

// Scenario: A quoted notice mid-output is not a termination.
func TestClaudeQuotedTerminationNoticeMidOutputIsNotATermination(t *testing.T) {
	dir := t.TempDir()
	body := "echo 'The CLI said: Background tasks still running after 600s; terminating. in a paragraph'\n" +
		"echo '```'\necho 'Background tasks still running after 600s; terminating.'\necho '```'\n" +
		"echo 'line a'\necho 'line b'\necho 'line c'\necho 'line d'\nexit 0"
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", body), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec := finalAgentPhase(t, st, "run-1", "build"); rec.Status != "passed" {
		t.Errorf("phase status = %q, want passed", rec.Status)
	}
}

// A line that merely contains the notice, not at its start, is not one.
func TestClaudeNoticeNotAtLineStartIsNotATermination(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", "echo 'quoting: "+terminationNotice+"'\nexit 0"), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec := finalAgentPhase(t, st, "run-1", "build"); rec.Status != "passed" {
		t.Errorf("phase status = %q, want passed", rec.Status)
	}
}

// Scenario: Other harnesses are not scanned for the notice.
func TestGooseIsNotScannedForTheTerminationNotice(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "goose", "echo '"+terminationNotice+"'\nexit 0"), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec := finalAgentPhase(t, st, "run-1", "build"); rec.Status != "passed" {
		t.Errorf("phase status = %q, want passed", rec.Status)
	}
}

// Scenario: A PASS before the termination notice is a failure.
func TestBetaPassBeforeTheTerminationNoticeIsAFailure(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", "echo 'VERDICT: PASS'\necho '"+terminationNotice+"'\nexit 0"), 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("RunAgentPhase returned nil error for a terminated beta")
	}
	rec := finalAgentPhase(t, st, "run-1", "review")
	if rec.Status != "failed" {
		t.Errorf("review status = %q, want failed", rec.Status)
	}
	if rec.Error != "agent claude terminated with background tasks still running" {
		t.Errorf("review error = %q, want the termination reason", rec.Error)
	}
}

// Scenario: A PASS with a non-zero exit is a failure.
func TestBetaPassWithANonZeroExitIsAFailure(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "claude", "echo 'VERDICT: PASS'\nexit 1"), 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err == nil {
		t.Fatal("RunAgentPhase returned nil error for a beta that exited 1")
	}
	if rec := finalAgentPhase(t, st, "run-1", "review"); rec.Status != "failed" {
		t.Errorf("review status = %q, want failed", rec.Status)
	}
}

func writeEnvelopeScript(summary, payload string) string {
	return "mkdir -p .sgt\ncat > .sgt/envelope.json <<'EOF'\n" +
		`{"task_id":"run-1","repo":"svc","summary":"` + summary + `","payload":` + payload + "}\nEOF\n"
}

func envelopeSummariesFor(t *testing.T, st *store.Store, stage string) []string {
	t.Helper()
	envs, err := st.ListEnvelopesForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range envs {
		if e.Stage == stage {
			out = append(out, e.Summary)
		}
	}
	return out
}

// Scenario: A review does not inherit the build's envelope.
func TestReviewDoesNotInheritTheBuildsEnvelope(t *testing.T) {
	dir := t.TempDir()
	builder := fakeAgent(t, dir, "goose", writeEnvelopeScript("BUILD-ENVELOPE", `{}`)+"exit 0")
	pr, st := newRunner(t, builder, 10*time.Second)
	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err != nil {
		t.Fatalf("build: %v", err)
	}

	reviewerDir := filepath.Join(dir, "reviewer")
	if err := os.MkdirAll(reviewerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pr.AgentCLI = fakeAgent(t, reviewerDir, "goose", "echo reviewed\nexit 0")
	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("review: %v", err)
	}

	summaries := envelopeSummariesFor(t, st, "review")
	if len(summaries) != 1 {
		t.Fatalf("review envelopes = %q, want exactly one", summaries)
	}
	if strings.Contains(summaries[0], "BUILD-ENVELOPE") {
		t.Errorf("review envelope summary = %q, adopted the build's envelope", summaries[0])
	}
}

// A retry never adopts the envelope a failed earlier attempt left behind.
func TestRetryDoesNotInheritAFailedAttemptsEnvelope(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "second")
	body := "if [ -f " + marker + " ]; then echo again; exit 0; fi\ntouch " + marker + "\n" +
		writeEnvelopeScript("FIRST-ATTEMPT", `{}`) + "exit 1"
	pr, st := newRunner(t, fakeAgent(t, dir, "goose", body), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range envelopeSummariesFor(t, st, "build") {
		if strings.Contains(s, "FIRST-ATTEMPT") {
			t.Errorf("build envelope summary %q carries the failed first attempt's envelope", s)
		}
	}
}

// Scenario: A failed attempt does not store its own completed envelope (pin).
func TestFailedAttemptDoesNotStoreItsOwnCompletedEnvelope(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "goose", writeEnvelopeScript("DONE", `{}`)+"exit 1"), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err == nil {
		t.Fatal("expected an error for an attempt that exited 1")
	}
	summaries := envelopeSummariesFor(t, st, "build")
	if len(summaries) != 1 {
		t.Fatalf("build envelopes = %q, want exactly one", summaries)
	}
	if strings.Contains(summaries[0], "DONE") || !strings.HasPrefix(summaries[0], "Phase build failed for svc: ") {
		t.Errorf("build envelope summary = %q, want sgt's failure summary without DONE", summaries[0])
	}
}

const blockingFindingsPayload = `{"findings":[{"axis":"spec","severity":"error","summary":"S-missing-retry-test","disposition":"add one"},{"axis":"style","severity":"info","summary":"rename x","disposition":"optional"}]}`

func phaseAttempts(t *testing.T, st *store.Store, name string) []store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	var out []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == "agent" {
			out = append(out, p)
		}
	}
	return out
}

// Scenarios: A roles-off blocking review keeps its own findings; A roles-off
// blocking review is not retried. Rule 6 holds for every harness, claude with
// roles off included.
func TestBlockingReviewFailsKeepsItsFindingsAndIsNotRetried(t *testing.T) {
	for _, harness := range []string{"goose", "claude"} {
		t.Run(harness, func(t *testing.T) {
			dir := t.TempDir()
			pr, st := newRunner(t, fakeAgent(t, dir, harness, writeEnvelopeScript("reviewed the diff", blockingFindingsPayload)+"exit 0"), 10*time.Second)

			env, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 2)
			if err == nil {
				t.Fatal("RunAgentPhase returned nil error for a review with a blocking finding")
			}
			if env != nil {
				t.Errorf("RunAgentPhase returned envelope %+v for a failed review, want nil", env)
			}
			const wantErr = "review reported blocking findings: S-missing-retry-test"
			if err.Error() != wantErr {
				t.Errorf("returned error = %q, want %q", err, wantErr)
			}

			attempts := phaseAttempts(t, st, "review")
			if len(attempts) != 1 {
				t.Fatalf("review attempt records = %d (%+v), want exactly 1", len(attempts), attempts)
			}
			if attempts[0].Status != "failed" || attempts[0].Error != wantErr {
				t.Errorf("review record status/error = %q/%q, want failed/%q", attempts[0].Status, attempts[0].Error, wantErr)
			}

			envs, err := st.ListEnvelopesForRun("run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(envs) != 1 {
				t.Fatalf("envelopes = %d, want 1", len(envs))
			}
			if envs[0].Summary != "reviewed the diff" {
				t.Errorf("review envelope summary = %q, want the agent's own", envs[0].Summary)
			}
			findings := handoff.ReviewFindings(envs[0].Data)
			if len(findings) != 2 || findings[0].Summary != "S-missing-retry-test" {
				t.Errorf("recorded findings = %+v, want the agent's two findings", findings)
			}
			if !handoff.HasBlockingFinding(handoff.ReviewFindings(attempts[0].Payload)) {
				t.Errorf("phase record payload %s lost the blocking finding", attempts[0].Payload)
			}
			assertProvenance(t, envs[0].Data, pr.AgentCLI, "", "")
		})
	}
}

// Non-blocking findings still pass.
func TestReviewWithOnlyNonBlockingFindingsPasses(t *testing.T) {
	dir := t.TempDir()
	payload := `{"findings":[{"axis":"style","severity":"warning","summary":"rename x","disposition":"optional"}]}`
	pr, st := newRunner(t, fakeAgent(t, dir, "goose", writeEnvelopeScript("ok", payload)+"exit 0"), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec := finalAgentPhase(t, st, "run-1", "review"); rec.Status != "passed" {
		t.Errorf("review status = %q, want passed", rec.Status)
	}
}

// Rule 6 applies to review only: a build envelope carrying an error finding is
// not a review judgement.
func TestBlockingFindingOutsideReviewDoesNotFailThePhase(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "goose", writeEnvelopeScript("built", blockingFindingsPayload)+"exit 0"), 10*time.Second)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec := finalAgentPhase(t, st, "run-1", "build"); rec.Status != "passed" {
		t.Errorf("build status = %q, want passed", rec.Status)
	}
}

// Scenario: the cancelled-attempt record carries FixCycle (tasks.md 6.3c).
func TestCancelledAttemptRecordCarriesFixCycle(t *testing.T) {
	dir := t.TempDir()
	pr, st := newRunner(t, fakeAgent(t, dir, "slow-agent", "sleep 30"), 0)
	pr.FixCycle = 2

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if _, _, err := pr.RunAgentPhase(ctx, "fix", "fix it", 0); err == nil {
		t.Fatal("expected a cancellation error")
	}
	attempts := phaseAttempts(t, st, "fix")
	if len(attempts) != 1 || attempts[0].Status != "cancelled" {
		t.Fatalf("fix records = %+v, want one cancelled record", attempts)
	}
	if attempts[0].FixCycle != 2 {
		t.Errorf("cancelled record FixCycle = %d, want 2", attempts[0].FixCycle)
	}
}
