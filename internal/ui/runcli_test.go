package ui

// Tests for specs/cli-factory-run/spec.md and the `sgt run` scenario in
// specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-runs-
// gauntlet-roles, design decision 10): `sgt run <project>` goes through
// RunProjectCLI, which runs the project's DAG through executeRun — the
// dispatch execution path — and answers with the run's stored terminal
// status and the reason recorded with it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/naming"
	"github.com/callmeradical/sgt/internal/store"
)

const cliRunProject = "clirun"

type cliRunFixture struct {
	st       *store.Store
	proj     *config.Project
	probe    *recordingProbe
	repoPath string
	fleet    string
	wikiRoot string
	claude   string
	argsLog  string
}

// newCLIRunFixture writes project "clirun" with one git repo "svc" and loads
// it. defaultsYAML is indented under "defaults:", factoryYAML under the repo,
// and dagYAML is appended at the top level ("" for a project with no DAG).
// A stub claude is first on PATH and named by absolute path as $CLAUDE; it
// logs its arguments, creates the worktree-relative file $STUB_TOUCH when set,
// and ends its output with a PASS verdict. The probe passed in by a test is a
// recordingProbe reporting an enabled user-scope gauntlet 0.1.0 with both
// role files.
func newCLIRunFixture(t *testing.T, defaultsYAML, factoryYAML, dagYAML string) *cliRunFixture {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := filepath.Join(base, "fleet")
	wikiRoot := filepath.Join(base, "wiki")
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", fleet)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SGT_WIKI_ROOT", wikiRoot)
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
		"if [ -n \"$STUB_TOUCH\" ]; then mkdir -p \"$(dirname \"$STUB_TOUCH\")\" && echo work > \"$STUB_TOUCH\"; fi\n" +
		"echo 'VERDICT: PASS'\n" +
		"exit 0\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoPath := filepath.Join(base, "svc")
	initGitRepo(t, repoPath)

	projYAML := "name: " + cliRunProject + "\n" +
		"defaults:\n" + indent(defaultsYAML, "  ") +
		"repos:\n" +
		"  - name: svc\n" +
		"    path: " + repoPath + "\n" +
		indent(factoryYAML, "    ") +
		dagYAML
	projYAML = strings.ReplaceAll(projYAML, "$CLAUDE", claude)
	if err := os.WriteFile(filepath.Join(cfgDir, cliRunProject+".yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	proj, err := config.LoadProject(cliRunProject)
	if err != nil {
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
	return &cliRunFixture{
		st: st, proj: proj, probe: probe, repoPath: repoPath, fleet: fleet,
		wikiRoot: wikiRoot, claude: claude, argsLog: argsLog,
	}
}

const oneStageDAG = "dag:\n" +
	"  name: ship\n" +
	"  stages:\n" +
	"    - name: only\n" +
	"      repos: [svc]\n"

// onlyRun returns the single run the store holds.
func (f *cliRunFixture) onlyRun(t *testing.T) store.RunRecord {
	t.Helper()
	runs, err := f.st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("store holds %d runs, want exactly 1: %+v", len(runs), runs)
	}
	return runs[0]
}

func (f *cliRunFixture) assertNoRunAndNoWork(t *testing.T) {
	t.Helper()
	runs, err := f.st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused sgt run left %d run(s): %+v", len(runs), runs)
	}
	if wts := worktreesUnder(t, f.fleet); len(wts) != 0 {
		t.Errorf("a refused sgt run left worktrees: %v", wts)
	}
	if _, err := os.Stat(f.argsLog); err == nil {
		t.Errorf("a refused sgt run started claude")
	}
	if _, err := os.Stat(f.wikiRoot); err == nil {
		t.Errorf("a refused sgt run wrote a wiki entry under %s", f.wikiRoot)
	}
}

// Scenario: sgt run records and exits by the stored outcome.
func TestRunProjectCLIWithAFailingGateReturnsTheStoredFailureAndItsRecordedReason(t *testing.T) {
	f := newCLIRunFixture(t, "agent: $CLAUDE",
		"factory:\n  pipeline: [test]\n  gates:\n    unit: \"echo boom; exit 3\"\n", oneStageDAG)

	status, reason, err := RunProjectCLI(context.Background(), f.st, f.proj, f.probe.probe)
	if err != nil {
		t.Fatalf("RunProjectCLI error = %v, want nil (a failed run is an outcome, not an error)", err)
	}
	run := f.onlyRun(t)
	if run.Status != "failed" {
		t.Fatalf("stored run status = %q, want failed", run.Status)
	}
	if status != run.Status {
		t.Errorf("returned status = %q, want the stored %q", status, run.Status)
	}
	if reason == "" {
		t.Fatalf("a failed run returned no reason")
	}

	// The reason recorded with the terminal status is the one the wiki page
	// carries (recordTerminalRun hands the same text to bullets and the wiki;
	// an sgt run has no intent, so the wiki is where it is on disk).
	page := filepath.Join(f.wikiRoot, cliRunProject, run.UpdatedAt.UTC().Format("2006-01-02"), run.ID+".md")
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("reading the run's wiki page: %v", err)
	}
	if want := "## Blocked\n\n" + reason + "\n"; !strings.Contains(string(data), want) {
		t.Errorf("wiki page does not record the returned reason %q:\n%s", reason, data)
	}

	// The gate itself is what failed, and it ran through the engine.
	var gate *store.PhaseRecord
	phases, err := f.st.ListPhasesForRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range phases {
		if phases[i].Name == "unit" {
			gate = &phases[i]
		}
	}
	if gate == nil || gate.Status != "failed" {
		t.Errorf("gate phase unit = %+v, want a failed record", gate)
	}
}

// Scenario: sgt run commits agent output and records delivery.
func TestRunProjectCLICommitsAgentOutputOnTheRunBranchAndRecordsDelivery(t *testing.T) {
	f := newCLIRunFixture(t, "agent: $CLAUDE\ngauntlet_roles: false",
		"factory:\n  pipeline: [build, test]\n  gates:\n    unit: \"test -f feature.txt\"\n", oneStageDAG)
	t.Setenv("STUB_TOUCH", "feature.txt")

	status, reason, err := RunProjectCLI(context.Background(), f.st, f.proj, f.probe.probe)
	if err != nil {
		t.Fatalf("RunProjectCLI error = %v", err)
	}
	if status != "passed" || reason != "" {
		t.Fatalf("RunProjectCLI = %q, %q; want passed with no reason", status, reason)
	}
	run := f.onlyRun(t)
	if run.Status != "passed" {
		t.Fatalf("stored run status = %q, want passed", run.Status)
	}
	if run.IntentID != "" || run.Brief != "" {
		t.Errorf("sgt run recorded intent %q, brief %q; want neither", run.IntentID, run.Brief)
	}
	if f.probe.count() != 0 {
		t.Errorf("an opted-out project was probed %d time(s)", f.probe.count())
	}

	wt := dag.FleetDir(run.ID, "svc")
	if out := gitOutT(t, wt, "status", "--porcelain"); out != "" {
		t.Errorf("worktree left uncommitted changes:\n%s", out)
	}
	if files := gitOutT(t, wt, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "feature.txt") {
		t.Errorf("HEAD of the run branch does not commit feature.txt; files: %q", files)
	}
	// A CLI run carries no work type and no change, so its branch is the
	// per-run fallback the delivery report and create-pr already look for.
	wantBranch := naming.BranchNameForRun(run.ID, run.Type, run.ChangeID)
	if branch := gitOutT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); branch != wantBranch {
		t.Errorf("worktree is on %q, want the run branch %q", branch, wantBranch)
	}
	if n := gitOutT(t, f.repoPath, "rev-list", "--count", "main.."+wantBranch); n != "1" {
		t.Errorf("run branch %s carries %s commit(s) beyond main, want 1", wantBranch, n)
	}
	if log := gitOutT(t, f.repoPath, "log", "--format=%s", "main"); strings.Count(log, "\n") != 0 {
		t.Errorf("the operator's main branch gained commits:\n%s", log)
	}

	envs, err := f.st.ListEnvelopesForRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var delivered []store.EnvelopeRecord
	for _, e := range envs {
		if e.Type == "run.delivered" {
			delivered = append(delivered, e)
		}
	}
	if len(delivered) != 1 {
		t.Fatalf("run has %d run.delivered envelope(s), want 1", len(delivered))
	}
	var report DeliveryReport
	if err := json.Unmarshal(delivered[0].Data, &report); err != nil {
		t.Fatalf("run.delivered data: %v", err)
	}
	if report.Branch != wantBranch || report.Commits != 1 || report.Dirty {
		t.Errorf("delivery report branch/commits/dirty = %q/%d/%v, want %q/1/false",
			report.Branch, report.Commits, report.Dirty, wantBranch)
	}
}

// Scenario: sgt run applies gauntlet roles.
func TestRunProjectCLIRunsBuildAsAlphaWithTheProbedVersion(t *testing.T) {
	f := newCLIRunFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build, test]\n", oneStageDAG)

	status, _, err := RunProjectCLI(context.Background(), f.st, f.proj, f.probe.probe)
	if err != nil {
		t.Fatalf("RunProjectCLI error = %v", err)
	}
	if status != "passed" {
		t.Fatalf("status = %q, want passed", status)
	}
	if f.probe.count() != 1 {
		t.Errorf("probe invoked %d times, want 1", f.probe.count())
	}
	run := f.onlyRun(t)
	agent, role, version := provenanceOf(t, phaseRecord(t, f.st, run.ID, "build"))
	if agent != f.claude || role != "gauntlet:alpha" || version != "0.1.0" {
		t.Errorf("build payload agent/role/plugin_version = %v/%v/%v, want %s/gauntlet:alpha/0.1.0",
			agent, role, version, f.claude)
	}
}

// A nil probe means the server's default probe (in production, the real
// `claude plugin list --json`; in this package's tests, the TestMain stub).
func TestRunProjectCLIWithANilProbeUsesTheDefaultProbe(t *testing.T) {
	f := newCLIRunFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]\n", oneStageDAG)

	status, _, err := RunProjectCLI(context.Background(), f.st, f.proj, nil)
	if err != nil {
		t.Fatalf("RunProjectCLI error = %v", err)
	}
	if status != "passed" {
		t.Fatalf("status = %q, want passed", status)
	}
	_, role, version := provenanceOf(t, phaseRecord(t, f.st, f.onlyRun(t).ID, "build"))
	if role != "gauntlet:alpha" || version != stubGauntletVersion {
		t.Errorf("build role/plugin_version = %v/%v, want gauntlet:alpha/%s", role, version, stubGauntletVersion)
	}
}

// Scenario: No DAG stages is refused, not passed.
func TestRunProjectCLIRefusesAProjectWithNoDAGStagesAndWritesNothing(t *testing.T) {
	for name, dagYAML := range map[string]string{
		"no dag":           "",
		"dag with nothing": "dag:\n  name: empty\n  stages: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newCLIRunFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]\n", dagYAML)

			status, reason, err := RunProjectCLI(context.Background(), f.st, f.proj, f.probe.probe)
			want := "project " + cliRunProject + " has no dag stages; nothing to run (use sgt dispatch to run its repositories)"
			if err == nil || err.Error() != want {
				t.Fatalf("RunProjectCLI error = %v, want %q", err, want)
			}
			if status == "passed" || reason != "" {
				t.Errorf("a refusal returned status %q, reason %q", status, reason)
			}
			if f.probe.count() != 0 {
				t.Errorf("a project with nothing to run was probed %d time(s)", f.probe.count())
			}
			f.assertNoRunAndNoWork(t)
		})
	}
}

// Scenario: sgt run without the plugin creates no run.
func TestRunProjectCLIWithoutThePluginIsRefusedAndCreatesNoRun(t *testing.T) {
	f := newCLIRunFixture(t, "agent: $CLAUDE", "factory:\n  pipeline: [build]\n", oneStageDAG)
	f.probe.set(pluginListJSON(t), nil)

	status, _, err := RunProjectCLI(context.Background(), f.st, f.proj, f.probe.probe)
	want := fmt.Sprintf("gauntlet roles are on for project %s but claude reports no enabled gauntlet plugin "+
		"(install: /plugin install gauntlet@gauntlet), or set defaults.gauntlet_roles: false", cliRunProject)
	if err == nil || err.Error() != want {
		t.Fatalf("RunProjectCLI error = %v, want %q", err, want)
	}
	if status == "passed" {
		t.Errorf("a refusal returned status passed")
	}
	if f.probe.count() != 1 {
		t.Errorf("probe invoked %d times, want 1", f.probe.count())
	}
	f.assertNoRunAndNoWork(t)
}

// The reason map holds an entry only while a caller is waiting for it, so the
// long-lived dashboard server, which records every run's terminal status
// through the same function, does not accumulate one per run.
func TestTerminalReasonsAreKeptOnlyForAwaitedRuns(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	t.Setenv("SGT_WIKI_ROOT", t.TempDir())
	srv := NewServer(st, 0)
	for _, id := range []string{"r-awaited", "r-other"} {
		if err := st.CreateRun(&store.RunRecord{ID: id, Project: "p", TaskID: id, Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}

	srv.awaitTerminalReason("r-awaited")
	if err := srv.recordTerminalRunWithReason("r-awaited", "failed", "gate unit failed"); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordTerminalRunWithReason("r-other", "failed", "something else"); err != nil {
		t.Fatal(err)
	}

	if got, ok := srv.takeTerminalReason("r-awaited"); !ok || got != "gate unit failed" {
		t.Errorf("takeTerminalReason(r-awaited) = %q, %v; want the recorded reason", got, ok)
	}
	if _, ok := srv.takeTerminalReason("r-awaited"); ok {
		t.Errorf("a taken reason is still held")
	}
	if _, ok := srv.takeTerminalReason("r-other"); ok {
		t.Errorf("a reason was kept for a run nobody awaited")
	}
	srv.reasonsMu.Lock()
	n := len(srv.terminalReasons)
	srv.reasonsMu.Unlock()
	if n != 0 {
		t.Errorf("terminalReasons holds %d entries after every waiter took its reason", n)
	}
}

func gitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}
