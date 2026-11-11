package runner

// Tests for specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles): which phases run as a gauntlet role, how the role
// reaches the claude command line, and the provenance every agent phase
// payload records.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

// recordingAgent writes a stub agent CLI named name into dir. Every
// invocation appends its arguments to the file named by $STUB_ARGS_LOG
// (arguments separated by \037, invocations terminated by \036, so prompts
// with newlines survive), then runs body. The log path is returned.
func recordingAgent(t *testing.T, dir, name, body string) (exe, logPath string) {
	t.Helper()
	logPath = filepath.Join(dir, name+".args")
	t.Setenv("STUB_ARGS_LOG", logPath)
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\037' \"$a\" >> \"$STUB_ARGS_LOG\"; done\n" +
		"printf '\\036' >> \"$STUB_ARGS_LOG\"\n" +
		body + "\n"
	exe = filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, logPath
}

// stubInvocations reads back every invocation recordingAgent logged.
func stubInvocations(t *testing.T, logPath string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading stub log: %v", err)
	}
	var calls [][]string
	for _, rec := range strings.Split(string(data), "\036") {
		if rec == "" {
			continue
		}
		args := strings.Split(strings.TrimSuffix(rec, "\037"), "\037")
		calls = append(calls, args)
	}
	return calls
}

// agentFlag returns the value following --agent in args, and whether the
// flag was present at all.
func agentFlag(args []string) (string, bool) {
	for i, a := range args {
		if a == "--agent" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func payloadMap(t *testing.T, payload json.RawMessage) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload is not a JSON object: %v (%s)", err, payload)
	}
	return m
}

// finalAgentPhase returns the single non-running record of phase name.
func finalAgentPhase(t *testing.T, st *store.Store, runID, name string) store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var final []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == "agent" && p.Status != "running" {
			final = append(final, p)
		}
	}
	if len(final) != 1 {
		t.Fatalf("expected 1 final %s record, got %d: %+v", name, len(final), final)
	}
	return final[0]
}

func assertProvenance(t *testing.T, payload json.RawMessage, agent, role, version string) {
	t.Helper()
	m := payloadMap(t, payload)
	for key, want := range map[string]string{"agent": agent, "role": role, "plugin_version": version} {
		got, present := m[key]
		if !present {
			t.Errorf("payload has no %q key; want %q (payload %s)", key, want, payload)
			continue
		}
		if got != want {
			t.Errorf("payload %s = %v, want %q", key, got, want)
		}
	}
}

func TestRoleForMapsPhasesToGauntletRoles(t *testing.T) {
	cases := map[string]string{
		"build":  "gauntlet:alpha",
		"fix":    "gauntlet:alpha",
		"review": "gauntlet:beta",
		"plan":   "",
		"test":   "",
		"docs":   "",
		"":       "",
	}
	for phase, want := range cases {
		if got := roleFor(phase); got != want {
			t.Errorf("roleFor(%q) = %q, want %q", phase, got, want)
		}
	}
}

// Scenario: A claude build phase runs as alpha / A claude review phase runs as beta.
func TestBuildAgentCommandForRolePassesTheRoleToClaude(t *testing.T) {
	for _, role := range []string{"gauntlet:alpha", "gauntlet:beta"} {
		exe, args, _, stdin := BuildAgentCommandForRole("claude", "claude-opus-5", role, "do the thing")
		if exe != "claude" {
			t.Errorf("exe = %q, want claude", exe)
		}
		if got, ok := agentFlag(args); !ok || got != role {
			t.Errorf("role %s: args %q carry --agent %q (present=%v), want --agent %s", role, args, got, ok, role)
		}
		if args[len(args)-1] != "do the thing" {
			t.Errorf("role %s: the prompt must stay the final argument, got %q", role, args)
		}
		if stdin != "" {
			t.Errorf("role %s: stdin = %q, want empty while the prompt is an argument", role, stdin)
		}
	}
}

// Scenario: The opt-out restores the previous command. The role-less command
// is the reference (risk R1), not a hard-coded argument list.
func TestBuildAgentCommandForRoleWithNoRoleIsTheRoleLessCommand(t *testing.T) {
	for _, model := range []string{"", "claude-opus-5"} {
		wantExe, wantArgs, wantEnv := BuildAgentCommand("claude", model, "do the thing")
		exe, args, env, stdin := BuildAgentCommandForRole("claude", model, "", "do the thing")
		if exe != wantExe || !reflect.DeepEqual(args, wantArgs) || !reflect.DeepEqual(env, wantEnv) || stdin != "" {
			t.Errorf("model %q: role-less command = %q %q %q stdin=%q, want %q %q %q",
				model, exe, args, env, stdin, wantExe, wantArgs, wantEnv)
		}
		if _, ok := agentFlag(args); ok {
			t.Errorf("model %q: role-less claude command carries --agent: %q", model, args)
		}
	}
}

// Scenario: Other harnesses never receive a role.
func TestBuildAgentCommandForRoleNeverGivesAnotherHarnessARole(t *testing.T) {
	for _, agent := range []string{"goose", "opencode", "oc", "codex", "pi", "copilot"} {
		_, args, env, _ := BuildAgentCommandForRole(agent, "", "gauntlet:alpha", "do the thing")
		_, wantArgs, wantEnv := BuildAgentCommand(agent, "", "do the thing")
		if _, ok := agentFlag(args); ok {
			t.Errorf("%s received --agent: %q", agent, args)
		}
		joined := strings.Join(append(append([]string{}, args...), env...), " ")
		if strings.Contains(joined, "gauntlet") {
			t.Errorf("%s command mentions gauntlet: %q", agent, joined)
		}
		if !reflect.DeepEqual(args, wantArgs) || !reflect.DeepEqual(env, wantEnv) {
			t.Errorf("%s: a role changed the command: %q %q, want %q %q", agent, args, env, wantArgs, wantEnv)
		}
	}
}

// An empty harness name resolves to DefaultAgent, the one default literal.
func TestBuildAgentCommandResolvesAnEmptyNameToDefaultAgent(t *testing.T) {
	exe, _, _ := BuildAgentCommand("", "", "do the thing")
	if exe != DefaultAgent {
		t.Errorf("exe for an empty agent = %q, want DefaultAgent %q", exe, DefaultAgent)
	}
}

// Scenario: The probed version reaches the phase record (runner half): a
// claude build phase with roles on is invoked as alpha and records the role
// and the version it was given.
func TestClaudeBuildPhaseWithRolesRunsAsAlphaAndRecordsProvenance(t *testing.T) {
	dir := t.TempDir()
	agent, logPath := recordingAgent(t, dir, "claude", "exit 0")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	env, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stubInvocations(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("stub invoked %d times, want 1", len(calls))
	}
	if got, ok := agentFlag(calls[0]); !ok || got != "gauntlet:alpha" {
		t.Errorf("build invoked with --agent %q (present=%v), want gauntlet:alpha; args %q", got, ok, calls[0])
	}
	assertProvenance(t, env.Payload, agent, "gauntlet:alpha", "0.1.0")
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "build").Payload, agent, "gauntlet:alpha", "0.1.0")
}

func TestClaudeReviewPhaseWithRolesRunsAsBeta(t *testing.T) {
	dir := t.TempDir()
	agent, logPath := recordingAgent(t, dir, "claude", "echo 'VERDICT: PASS'\nexit 0")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "review", "review this", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := stubInvocations(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("stub invoked %d times, want 1", len(calls))
	}
	if got, ok := agentFlag(calls[0]); !ok || got != "gauntlet:beta" {
		t.Errorf("review invoked with --agent %q (present=%v), want gauntlet:beta; args %q", got, ok, calls[0])
	}
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "review").Payload, agent, "gauntlet:beta", "0.1.0")
}

// Scenario: A plan phase runs without a role / A role-less phase records
// empty provenance (payload half). The version is "" because no role ran
// (risk R6), even though the runner was given one.
func TestClaudePlanPhaseWithRolesOnRunsWithoutARole(t *testing.T) {
	dir := t.TempDir()
	agent, logPath := recordingAgent(t, dir, "claude", "exit 0")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "plan", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := stubInvocations(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("stub invoked %d times, want 1", len(calls))
	}
	if _, ok := agentFlag(calls[0]); ok {
		t.Errorf("plan phase was invoked with --agent: %q", calls[0])
	}
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "plan").Payload, agent, "", "")
}

// Scenario: The opt-out restores the previous command, at the phase seam: a
// claude build phase whose caller left roles off is invoked with exactly the
// role-less command.
func TestClaudeBuildPhaseWithRolesOffRunsTheRoleLessCommand(t *testing.T) {
	dir := t.TempDir()
	agent, logPath := recordingAgent(t, dir, "claude", "exit 0")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.Model = "claude-opus-5"
	pr.GauntletVersion = "0.1.0" // set but unused: roles are off

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := stubInvocations(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("stub invoked %d times, want 1", len(calls))
	}
	_, want, _ := BuildAgentCommand(agent, "claude-opus-5", "brief")
	if !reflect.DeepEqual(calls[0], want) {
		t.Errorf("roles-off build invoked with %q, want the role-less command %q", calls[0], want)
	}
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "build").Payload, agent, "", "")
}

// Scenario: Other harnesses never receive a role, at the phase seam. Even
// if a caller set roles on, a non-claude harness gets no --agent, and its
// record claims no role.
func TestNonClaudeHarnessNeverRunsAsARole(t *testing.T) {
	dir := t.TempDir()
	agent, logPath := recordingAgent(t, dir, "goose", "exit 0")
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := stubInvocations(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("stub invoked %d times, want 1", len(calls))
	}
	if strings.Contains(strings.Join(calls[0], " "), "gauntlet") {
		t.Errorf("goose was invoked with gauntlet text: %q", calls[0])
	}
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "build").Payload, agent, "", "")
}

// Scenario: An agent-written envelope still records the harness.
func TestAgentWrittenEnvelopeStillRecordsTheHarnessAndRole(t *testing.T) {
	dir := t.TempDir()
	body := `mkdir -p .sgt
cat > .sgt/envelope.json <<'EOF'
{"task_id":"run-1","repo":"svc","stage":"build","summary":"agent wrote this","payload":{"custom":"value"}}
EOF
exit 0`
	agent, _ := recordingAgent(t, dir, "claude", body)
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	env, _, err := pr.RunAgentPhase(context.Background(), "build", "brief", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Summary != "agent wrote this" {
		t.Fatalf("the agent's own envelope was not read: %+v", env)
	}
	rec := finalAgentPhase(t, st, "run-1", "build")
	if payloadMap(t, rec.Payload)["custom"] != "value" {
		t.Errorf("agent-authored payload keys were lost: %s", rec.Payload)
	}
	assertProvenance(t, rec.Payload, agent, "gauntlet:alpha", "0.1.0")
}

// An agent-written envelope cannot claim a role or version sgt did not run:
// sgt's provenance keys replace same-named keys the agent wrote.
func TestAgentWrittenEnvelopeCannotOverrideProvenance(t *testing.T) {
	dir := t.TempDir()
	body := `mkdir -p .sgt
cat > .sgt/envelope.json <<'EOF'
{"task_id":"run-1","repo":"svc","stage":"plan","summary":"s","payload":{"agent":"someone-else","role":"gauntlet:alpha","plugin_version":"9.9.9"}}
EOF
exit 0`
	agent, _ := recordingAgent(t, dir, "claude", body)
	pr, st := newRunner(t, agent, 10*time.Second)
	pr.GauntletRoles = true
	pr.GauntletVersion = "0.1.0"

	if _, _, err := pr.RunAgentPhase(context.Background(), "plan", "brief", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertProvenance(t, finalAgentPhase(t, st, "run-1", "plan").Payload, agent, "", "")
}
