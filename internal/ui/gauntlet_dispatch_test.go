package ui

// Tests for specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles) at POST /api/dispatch: a roles-on claude project is
// preflighted for the gauntlet plugin before anything is written, refused
// with a message naming what is missing, never probed when roles do not
// apply, and — when the probe passes — runs build as gauntlet:alpha and
// review as gauntlet:beta, recording the probed version.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/runner"
	"github.com/callmeradical/sgt/internal/store"
)

const rolesChangeID = "add-stripe-webhooks"

type rolesFixture struct {
	srv      *Server
	mux      http.Handler
	st       *store.Store
	probe    *recordingProbe
	repoPath string
	fleet    string
	claude   string // the stub claude's absolute path
	argsLog  string // where the stub claude logs its invocations
}

// newRolesFixture builds a project "roles" with one git repo "svc" whose
// defaults block is defaultsYAML (indented under "defaults:") and whose
// factory block is factoryYAML (indented under the repo). A stub claude is
// both first on PATH and available by absolute path; it logs its arguments,
// creates the worktree-relative file $STUB_TOUCH when that is set, and ends
// its output with a PASS verdict. The server's probe is a
// recordingProbe that, by default, reports an enabled user-scope gauntlet
// 0.1.0 with both role files.
func newRolesFixture(t *testing.T, defaultsYAML, factoryYAML string) *rolesFixture {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := filepath.Join(base, "fleet")
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", fleet)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SGT_WIKI_ROOT", filepath.Join(base, "wiki"))
	t.Setenv("SGT_ARTIFACTS_ROOT", filepath.Join(base, "artifacts"))

	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(base, "claude.args")
	t.Setenv("STUB_ARGS_LOG", argsLog)
	claude := filepath.Join(bin, "claude")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\037' \"$a\" >> \"$STUB_ARGS_LOG\"; done\n" +
		"printf '\\036' >> \"$STUB_ARGS_LOG\"\n" +
		"if [ -n \"$STUB_TOUCH\" ]; then mkdir -p \"$(dirname \"$STUB_TOUCH\")\" && touch \"$STUB_TOUCH\"; fi\n" +
		"echo 'VERDICT: PASS'\n" +
		"exit 0\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoPath := filepath.Join(base, "svc")
	initGitRepo(t, repoPath)

	projYAML := "name: roles\n" +
		"defaults:\n" + indent(defaultsYAML, "  ") +
		"repos:\n" +
		"  - name: svc\n" +
		"    path: " + repoPath + "\n" +
		indent(factoryYAML, "    ")
	projYAML = strings.ReplaceAll(projYAML, "$CLAUDE", claude)
	if err := os.WriteFile(filepath.Join(cfgDir, "roles.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	probe := &recordingProbe{out: pluginListJSON(t, pluginEntryJSON{
		ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true,
		InstallPath: gauntletInstall(t, "alpha", "beta"),
	})}
	srv := NewServer(st, 0)
	srv.GauntletProbe = probe.probe
	return &rolesFixture{
		srv: srv, mux: srv.Handler(), st: st, probe: probe,
		repoPath: repoPath, fleet: fleet, claude: claude, argsLog: argsLog,
	}
}

func indent(block, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		if line == "" {
			continue
		}
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}

// dispatch posts a dispatch for repo svc naming the fixture's pre-created
// change. extra overrides request fields; a nil value removes the field.
func (f *rolesFixture) dispatch(t *testing.T, extra map[string]interface{}) (int, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.repoPath, "openspec", "changes", rolesChangeID), 0o755); err != nil {
		t.Fatal(err)
	}
	fields := map[string]interface{}{
		"project": "roles", "brief": "add stripe webhooks", "change_id": rolesChangeID,
		"repos": []string{"svc"}, "type": "feat",
	}
	for k, v := range extra {
		if v == nil {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	w := postDispatch(t, f.mux, dispatchBody(t, fields))
	return w.Code, w.Body.String()
}

// claudeCalls reads back every invocation of the stub claude.
func (f *rolesFixture) claudeCalls(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(f.argsLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, rec := range strings.Split(string(data), "\036") {
		if rec != "" {
			calls = append(calls, strings.Split(strings.TrimSuffix(rec, "\037"), "\037"))
		}
	}
	return calls
}

// callForPhase finds the claude invocation whose prompt (the last argument)
// is the given phase's.
func callForPhase(t *testing.T, calls [][]string, phase string) []string {
	t.Helper()
	marker := "# Phase objective: " + phase + "\n"
	for _, c := range calls {
		if len(c) > 0 && strings.HasPrefix(c[len(c)-1], marker) {
			return c
		}
	}
	t.Fatalf("no claude invocation for phase %s among %d calls", phase, len(calls))
	return nil
}

func argAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func dispatchedRunID(t *testing.T, body string) string {
	t.Helper()
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.TaskID == "" {
		t.Fatalf("dispatch response has no task_id: %v; body=%s", err, body)
	}
	return resp.TaskID
}

// assertNothingCreated checks the refusal left no record anywhere: no run, no
// intent, no worktree, no run branch, no scaffolded change, and no agent
// started.
func (f *rolesFixture) assertNothingCreated(t *testing.T) {
	t.Helper()
	runs, err := f.st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused dispatch left %d run(s): %+v", len(runs), runs)
	}
	intents, err := f.st.ListIntentsForProject("roles")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Errorf("a refused dispatch left %d intent(s)", len(intents))
	}
	if wts := worktreesUnder(t, f.fleet); len(wts) != 0 {
		t.Errorf("a refused dispatch left worktrees: %v", wts)
	}
	if br := runBranches(t, f.repoPath); len(br) != 0 {
		t.Errorf("a refused dispatch left run branches: %v", br)
	}
	if calls := f.claudeCalls(t); len(calls) != 0 {
		t.Errorf("a refused dispatch started claude %d time(s)", len(calls))
	}
}

func phaseRecord(t *testing.T, st *store.Store, runID, name string) store.PhaseRecord {
	t.Helper()
	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var found []store.PhaseRecord
	for _, p := range phases {
		if p.Name == name && p.Kind == "agent" && p.Status != "running" {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected one %s record, got %d: %+v", name, len(found), found)
	}
	return found[0]
}

func provenanceOf(t *testing.T, p store.PhaseRecord) (agent, role, version interface{}) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(p.Payload, &m); err != nil {
		t.Fatalf("%s payload is not a JSON object: %v", p.Name, err)
	}
	for _, k := range []string{"agent", "role", "plugin_version"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s payload has no %q key: %s", p.Name, k, p.Payload)
		}
	}
	return m["agent"], m["role"], m["plugin_version"]
}

const noPluginMessage = "gauntlet roles are on for project roles but claude reports no enabled gauntlet plugin " +
	"(install: /plugin install gauntlet@gauntlet), or set defaults.gauntlet_roles: false"

const missingClaudeMessage = `agent harness "claude" not found on PATH; install it or set defaults.agent in the project`

// Scenario: The probed version reaches the phase record — and, with it, a
// claude build runs as alpha, a review as beta, and a plan with no role.
func TestADispatchedRolesRunRunsBuildAsAlphaAndRecordsThePluginVersion(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [plan, build, review]")

	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)

	if run, _ := f.st.GetRun(runID); run.Status != "passed" {
		t.Fatalf("run status = %s, want passed", run.Status)
	}
	if f.probe.count() != 1 {
		t.Errorf("probe invoked %d times, want 1", f.probe.count())
	}

	calls := f.claudeCalls(t)
	if role, ok := argAfter(callForPhase(t, calls, "build"), "--agent"); !ok || role != "gauntlet:alpha" {
		t.Errorf("build ran with --agent %q (present=%v), want gauntlet:alpha", role, ok)
	}
	if role, ok := argAfter(callForPhase(t, calls, "review"), "--agent"); !ok || role != "gauntlet:beta" {
		t.Errorf("review ran with --agent %q (present=%v), want gauntlet:beta", role, ok)
	}
	if _, ok := argAfter(callForPhase(t, calls, "plan"), "--agent"); ok {
		t.Errorf("plan ran with an --agent argument: %q", callForPhase(t, calls, "plan"))
	}

	for name, want := range map[string][2]string{
		"plan":   {"", ""},
		"build":  {"gauntlet:alpha", "0.1.0"},
		"review": {"gauntlet:beta", "0.1.0"},
	} {
		agent, role, version := provenanceOf(t, phaseRecord(t, f.st, runID, name))
		if agent != f.claude || role != want[0] || version != want[1] {
			t.Errorf("%s payload agent/role/plugin_version = %v/%v/%v, want %s/%q/%q",
				name, agent, role, version, f.claude, want[0], want[1])
		}
	}
}

// Scenario: A default pipeline gains a review phase when roles apply, as
// dispatched.
func TestADispatchedRolesRunWithNoPipelineEndsWithReview(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "")

	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)

	phases, err := f.st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range phases {
		if p.Status != "running" {
			names = append(names, p.Name)
		}
	}
	if want := []string{"plan", "build", "test", "review"}; !reflect.DeepEqual(names, want) {
		t.Errorf("phases recorded = %v, want %v", names, want)
	}
}

// Scenario: A dispatch without the plugin creates no run.
func TestARolesDispatchWithoutThePluginIsRefusedAndCreatesNothing(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	f.probe.set(pluginListJSON(t), nil)

	code, body := f.dispatch(t, map[string]interface{}{"change_id": nil})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != noPluginMessage {
		t.Errorf("body = %q, want %q", strings.TrimSpace(body), noPluginMessage)
	}
	f.assertNothingCreated(t)
	// The change would have been scaffolded from the brief had the refusal
	// come after change resolution.
	entries, _ := os.ReadDir(filepath.Join(f.repoPath, "openspec", "changes"))
	for _, e := range entries {
		if e.Name() != rolesChangeID {
			t.Errorf("a refused dispatch scaffolded change %q", e.Name())
		}
	}
}

// Scenario: A missing claude names the harness, not the plugin.
func TestARolesDispatchWithNoClaudeNamesTheHarness(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	f.probe.set(nil, fmt.Errorf("exec: \"claude\": %w", exec.ErrNotFound))

	code, body := f.dispatch(t, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != missingClaudeMessage {
		t.Errorf("body = %q, want %q", strings.TrimSpace(body), missingClaudeMessage)
	}
	if strings.Contains(body, "plugin") {
		t.Errorf("a missing harness was reported as a missing plugin: %q", body)
	}
	f.assertNothingCreated(t)
}

// Scenario: A probe failure refuses with its own message — a timeout, a
// failing claude, and output that is not the expected JSON alike.
func TestARolesDispatchWhoseProbeFailsIsRefusedWithTheProbeMessage(t *testing.T) {
	cases := []struct {
		name string
		out  []byte
		err  error
		want string
	}{
		{"timeout", nil, fmt.Errorf("claude plugin list: %w", context.DeadlineExceeded), context.DeadlineExceeded.Error()},
		{"non-zero exit", nil, errors.New("claude plugin list --json: exit status 2"), "exit status 2"},
		{"not JSON", []byte("Plugins:\n  gauntlet@gauntlet 0.1.0\n"), nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
			f.probe.set(tc.out, tc.err)

			code, body := f.dispatch(t, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", code, body)
			}
			body = strings.TrimSpace(body)
			if !strings.HasPrefix(body, "could not read claude's plugin list for project roles: ") ||
				!strings.HasSuffix(body, "; retry, or set defaults.gauntlet_roles: false") {
				t.Errorf("body = %q, want the probe-failure message", body)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("body = %q, want it to carry the probe error %q", body, tc.want)
			}
			f.assertNothingCreated(t)
		})
	}
}

// Scenarios: A project-scope install does not satisfy the probe / A plugin
// without both roles is refused (and a disabled install, likewise).
func TestAGauntletInstallThatCannotServeAFleetWorktreeIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		entry func(t *testing.T) pluginEntryJSON
	}{
		{"project scope only", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "project", Enabled: true,
				InstallPath: gauntletInstall(t, "alpha", "beta"), ProjectPath: "/home/operator/src/sgt"}
		}},
		{"local scope only", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "local", Enabled: true,
				InstallPath: gauntletInstall(t, "alpha", "beta")}
		}},
		{"no beta role", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true,
				InstallPath: gauntletInstall(t, "alpha")}
		}},
		{"no alpha role", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true,
				InstallPath: gauntletInstall(t, "beta")}
		}},
		{"disabled", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: false,
				InstallPath: gauntletInstall(t, "alpha", "beta")}
		}},
		{"not a gauntlet plugin", func(t *testing.T) pluginEntryJSON {
			return pluginEntryJSON{ID: "other@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true,
				InstallPath: gauntletInstall(t, "alpha", "beta")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
			f.probe.set(pluginListJSON(t, tc.entry(t)), nil)

			code, body := f.dispatch(t, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", code, body)
			}
			if strings.TrimSpace(body) != noPluginMessage {
				t.Errorf("body = %q, want the no-plugin message", strings.TrimSpace(body))
			}
			f.assertNothingCreated(t)
		})
	}
}

// Scenario: An opted-out project is not probed — and its build runs with
// exactly the role-less command (The opt-out restores the previous command).
func TestAnOptedOutProjectIsNotProbedAndRunsTheRoleLessCommand(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE\ngauntlet_roles: false", "factory:\n  pipeline: [build]")
	f.probe.set(nil, errors.New("must not be probed"))

	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	runID := dispatchedRunID(t, body)
	waitForTerminalRun(t, f.st, runID)

	if f.probe.count() != 0 {
		t.Errorf("an opted-out project was probed %d time(s)", f.probe.count())
	}
	build := callForPhase(t, f.claudeCalls(t), "build")
	_, want, _ := runner.BuildAgentCommand(f.claude, "", build[len(build)-1])
	if !reflect.DeepEqual(build, want) {
		t.Errorf("opted-out build ran with %q, want the role-less command %q", build, want)
	}
	_, role, version := provenanceOf(t, phaseRecord(t, f.st, runID, "build"))
	if role != "" || version != "" {
		t.Errorf("opted-out build recorded role %v / version %v, want both empty", role, version)
	}
}

// Scenario: A non-claude project is not probed.
func TestANonClaudeProjectIsNotProbed(t *testing.T) {
	f := newRolesFixture(t, "agent: goose", "factory:\n  pipeline: [test]\n  gates:\n    unit: \"true\"")
	f.probe.set(nil, errors.New("must not be probed"))

	code, body := f.dispatch(t, nil)
	if code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", code, body)
	}
	waitForTerminalRun(t, f.st, dispatchedRunID(t, body))
	if f.probe.count() != 0 {
		t.Errorf("a goose project was probed %d time(s)", f.probe.count())
	}
}

// Scenario: A plan proposal is not probed.
func TestARolesPlanProposalIsNotProbed(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")
	f.probe.set(pluginListJSON(t), nil) // would refuse, if it were asked

	code, body := f.dispatch(t, map[string]interface{}{"repos": nil})
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", code, body)
	}
	if f.probe.count() != 0 {
		t.Errorf("a plan proposal was probed %d time(s)", f.probe.count())
	}
	var resp struct {
		Status   string `json:"status"`
		IntentID string `json:"intent_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	intent, err := f.st.GetIntent(resp.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "proposed" || intent.Status != "proposed" {
		t.Errorf("response status %q, intent status %q; want both proposed", resp.Status, intent.Status)
	}
}

// Scenario: A repeated request is answered before the probe.
func TestARepeatedRolesRequestIsAnsweredWithoutProbing(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [test]\n  gates:\n    unit: \"true\"")

	firstCode, firstBody := f.dispatch(t, map[string]interface{}{"request_id": "R"})
	if firstCode != http.StatusOK {
		t.Fatalf("first dispatch status = %d; body=%s", firstCode, firstBody)
	}
	waitForTerminalRun(t, f.st, dispatchedRunID(t, firstBody))
	if f.probe.count() != 1 {
		t.Fatalf("first dispatch probed %d time(s), want 1", f.probe.count())
	}

	f.probe.set(pluginListJSON(t), nil) // the plugin is now gone
	secondCode, secondBody := f.dispatch(t, map[string]interface{}{"request_id": "R"})
	if f.probe.count() != 1 {
		t.Errorf("the repeat probed again: %d calls, want 1", f.probe.count())
	}
	if secondCode != firstCode {
		t.Fatalf("repeat status %d, original %d; body=%s", secondCode, firstCode, secondBody)
	}
	var a, b map[string]interface{}
	if err := json.Unmarshal([]byte(firstBody), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(secondBody), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("repeat response %v differs from original %v", b, a)
	}
	for _, k := range []string{"change_id", "change_repo", "change_dir"} {
		if _, ok := b[k]; !ok {
			t.Errorf("repeat response has no %s", k)
		}
	}
}

// The preflight runs after the agent override: a goose project dispatched
// with agent claude is preflighted as the claude run it becomes.
func TestTheDispatchPreflightFollowsTheAgentOverride(t *testing.T) {
	f := newRolesFixture(t, "agent: goose", "factory:\n  pipeline: [build]")
	f.probe.set(pluginListJSON(t), nil)

	code, body := f.dispatch(t, map[string]interface{}{"agent": f.claude})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", code, body)
	}
	if strings.TrimSpace(body) != noPluginMessage {
		t.Errorf("body = %q, want the no-plugin message", strings.TrimSpace(body))
	}
	f.assertNothingCreated(t)
}

// The preflight runs after ValidateAgent: an unsupported agent is refused
// for what it is, without probing.
func TestAnUnsupportedAgentIsRefusedBeforeTheProbe(t *testing.T) {
	f := newRolesFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]")

	code, body := f.dispatch(t, map[string]interface{}{"agent": "not-a-harness"})
	if code != http.StatusBadRequest || !strings.Contains(body, "unsupported agent") {
		t.Fatalf("status = %d body = %q, want 400 naming the unsupported agent", code, body)
	}
	if f.probe.count() != 0 {
		t.Errorf("an unsupported agent was probed %d time(s)", f.probe.count())
	}
}
