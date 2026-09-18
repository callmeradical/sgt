package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

// fakeAgentScript writes an executable script that stands in for an agent
// CLI, mirroring internal/runner's own fakeAgent test helper (unexported
// there, so duplicated here for package ui).
func fakeAgentScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Regression coverage for issue #14 ("fix(engine): terminalize run after
// failed agent phase"): dispatch a run with two agent phases, let the first
// pass and the second exhaust its (zero, i.e. single-attempt) retry budget,
// and require the run to end up terminal ("failed"), not stuck "running"
// forever.
func TestARunWithAnExhaustedFailedAgentPhaseIsTerminalizedToFailed(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))

	repoA := filepath.Join(base, "repoA")
	initGitRepo(t, repoA)

	agentDir := t.TempDir()
	callCountFile := filepath.Join(agentDir, "calls")
	// Exits 0 on its first-ever invocation ("plan"), nonzero on every call
	// after that ("build") — a phase-agnostic way to make the second agent
	// phase fail without needing to parse the prompt for which phase it is.
	agent := fakeAgentScript(t, agentDir, "agent.sh", fmt.Sprintf(`
n=$(cat %q 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > %q
[ "$n" -eq 1 ]
`, callCountFile, callCountFile))

	projYAML := fmt.Sprintf(`name: term-fail-proj
repos:
  - name: repoA
    path: %s
    factory:
      pipeline: [plan, build, test]
defaults:
  agent: %s
`, repoA, agent)
	if err := os.WriteFile(filepath.Join(cfgDir, "term-fail-proj.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := NewServer(st, 0)
	proj, err := config.LoadProject("term-fail-proj")
	if err != nil {
		t.Fatal(err)
	}

	const runID = "sgt-term-fail-test"
	if err := st.CreateRun(&store.RunRecord{
		ID: runID, Project: proj.Name, TaskID: runID, Type: "feat", ChangeID: "term-fail-change", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	router := handoff.NewRouter(filepath.Join(dag.FleetRoot(), runID, "handoff"))
	engine := dag.NewEngine(proj, st, router)

	ctx, cancel := context.WithCancel(context.Background())
	srv.executeRun(ctx, cancel, engine, proj, runID, "terminalize on failure test", nil, "")

	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" {
		t.Fatalf("run status = %q, want %q — a run with an exhausted failed agent phase must not stay running", run.Status, "failed")
	}
	if !store.IsTerminalRunStatus(run.Status) {
		t.Errorf("IsTerminalRunStatus(%q) = false, want true", run.Status)
	}

	phases, err := st.ListPhasesForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	var sawPassedPlan, sawFailedBuild, sawTest bool
	for _, p := range phases {
		switch {
		case p.Name == "plan" && p.Status == "passed":
			sawPassedPlan = true
		case p.Name == "build" && p.Status == "failed":
			sawFailedBuild = true
		case p.Name == "test":
			sawTest = true
		}
	}
	if !sawPassedPlan {
		t.Errorf("phases = %+v, want a passed \"plan\" phase recorded", phases)
	}
	if !sawFailedBuild {
		t.Errorf("phases = %+v, want a failed \"build\" phase recorded", phases)
	}
	if sawTest {
		t.Errorf("phases = %+v, want no \"test\" phase — it must never run after \"build\" already failed", phases)
	}
}
