package runner

// Tests for specs/agent-harness/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles): "An unnamed harness resolves to claude" and "A missing
// harness fails with a reason that names it" (design decisions 1a rule 1 and 2).

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
)

// isolatedPath points PATH at dir alone plus the system directories /bin/sh
// stubs need, so no test here can reach an installed claude.
func isolatedPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
}

// Scenario: An empty agent builds a claude command.
func TestAnEmptyAgentBuildsAClaudeCommand(t *testing.T) {
	exe, args, _ := BuildAgentCommand("", "", "do the thing")
	if exe != "claude" {
		t.Errorf("exe for an empty agent = %q, want claude", exe)
	}
	if len(args) < 2 || args[0] != "--print" || args[1] != "--dangerously-skip-permissions" {
		t.Errorf("args for an empty agent = %q, want them to begin with --print --dangerously-skip-permissions", args)
	}
	if config.DefaultAgent != "claude" || DefaultAgent != config.DefaultAgent {
		t.Errorf("DefaultAgent = %q (config %q), want both claude", DefaultAgent, config.DefaultAgent)
	}
}

// Scenario: A named harness is unchanged. The literals are what the base
// branch (ad0f434) builds for opencode.
func TestANamedHarnessIsUnchanged(t *testing.T) {
	cases := []struct {
		model    string
		wantArgs []string
	}{
		{"", []string{"run", "--auto", "do the thing"}},
		{"anthropic/claude-sonnet-4-6", []string{"run", "--auto", "--model", "anthropic/claude-sonnet-4-6", "do the thing"}},
	}
	for _, tc := range cases {
		exe, args, env := BuildAgentCommand("opencode", tc.model, "do the thing")
		if exe != "opencode" {
			t.Errorf("model %q: exe = %q, want opencode", tc.model, exe)
		}
		if !reflect.DeepEqual(args, tc.wantArgs) {
			t.Errorf("model %q: args = %q, want %q", tc.model, args, tc.wantArgs)
		}
		if len(env) != 0 {
			t.Errorf("model %q: env = %q, want none", tc.model, env)
		}
	}
}

// Scenario: Provenance names the resolved default.
func TestProvenanceNamesTheResolvedDefault(t *testing.T) {
	bin := t.TempDir()
	_, logPath := recordingAgent(t, bin, "claude", "exit 0")
	isolatedPath(t, bin)

	pr, st := newRunner(t, "", 10*time.Second)
	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "do the thing", 0); err != nil {
		t.Fatalf("RunAgentPhase with an empty agent: %v", err)
	}
	if calls := stubInvocations(t, logPath); len(calls) != 1 {
		t.Fatalf("stub claude ran %d times, want 1", len(calls))
	}
	phases, err := st.ListPhasesForRun("run-1")
	if err != nil || len(phases) != 1 {
		t.Fatalf("phases = %v (err %v), want one", phases, err)
	}
	if phases[0].Status != "passed" {
		t.Fatalf("phase status = %q (error %q), want passed", phases[0].Status, phases[0].Error)
	}
	var payload map[string]any
	if err := json.Unmarshal(phases[0].Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["agent"] != "claude" {
		t.Errorf("payload agent = %v, want claude", payload["agent"])
	}
}

// Scenario: claude is absent from PATH. The attempt fails with the decision 2
// reason, starts no process, and is not retried even with retries left.
func TestClaudeAbsentFromPathFailsWithAReasonThatNamesIt(t *testing.T) {
	bin := t.TempDir()
	isolatedPath(t, bin)

	pr, st := newRunner(t, "", 10*time.Second)
	env, _, err := pr.RunAgentPhase(context.Background(), "build", "do the thing", 2)
	if err == nil || env != nil {
		t.Fatalf("RunAgentPhase with claude absent returned env=%v err=%v, want a failure", env, err)
	}
	const want = `agent harness "claude" not found on PATH; install it or set defaults.agent in the project`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	phases, gerr := st.ListPhasesForRun("run-1")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if len(phases) != 1 {
		t.Fatalf("got %d phase records, want exactly 1: a missing harness is never retried", len(phases))
	}
	p := phases[0]
	if p.Status != "failed" || p.Error != want || p.Attempt != 1 {
		t.Errorf("phase = status %q error %q attempt %d, want failed %q 1", p.Status, p.Error, p.Attempt, want)
	}
	if !strings.Contains(p.Error, "claude") || !strings.Contains(p.Error, "defaults.agent") {
		t.Errorf("error %q must name claude and defaults.agent", p.Error)
	}
	var payload map[string]any
	if err := json.Unmarshal(p.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["agent"] != "claude" {
		t.Errorf("payload agent = %v, want claude", payload["agent"])
	}

	envs, eerr := st.ListEnvelopesForRun("run-1")
	if eerr != nil {
		t.Fatal(eerr)
	}
	if len(envs) != 1 || !strings.HasPrefix(envs[0].Summary, "Phase build failed for svc: "+want) {
		t.Errorf("envelopes = %+v, want one sgt failure envelope naming the reason", envs)
	}
}

// A harness named explicitly is named in the reason too: the reason names the
// actual executable, not the default.
func TestAMissingNamedHarnessIsNamedInTheReason(t *testing.T) {
	bin := t.TempDir()
	isolatedPath(t, bin)

	pr, st := newRunner(t, "goose", 10*time.Second)
	_, _, err := pr.RunAgentPhase(context.Background(), "build", "do the thing", 1)
	const want = `agent harness "goose" not found on PATH; install it or set defaults.agent in the project`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	phases, _ := st.ListPhasesForRun("run-1")
	if len(phases) != 1 || phases[0].Status != "failed" {
		t.Fatalf("phases = %+v, want one failed record", phases)
	}
}

// Rule 1 comes before rule 2: an attempt whose harness is missing is failed,
// not cancelled, even when the context is already cancelled.
func TestAMissingHarnessIsDecidedBeforeCancellation(t *testing.T) {
	bin := t.TempDir()
	isolatedPath(t, bin)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, st := newRunner(t, "", 10*time.Second)
	_, _, err := pr.RunAgentPhase(ctx, "build", "do the thing", 0)
	if err == nil || !strings.Contains(err.Error(), `agent harness "claude" not found on PATH`) {
		t.Fatalf("error = %v, want the harness-missing reason", err)
	}
	phases, _ := st.ListPhasesForRun("run-1")
	if len(phases) != 1 || phases[0].Status != "failed" {
		t.Fatalf("phases = %+v, want one failed record", phases)
	}
}
