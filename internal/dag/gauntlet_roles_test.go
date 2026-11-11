package dag

// Tests for specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles): the effective pipeline when roles apply, and the
// engine carrying the caller's preflight result and fix cycle into every
// phase it runs.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// Scenarios: Default pipeline with roles / without roles / Explicit pipeline
// is untouched.
func TestPipelineForAddsReviewToTheDefaultOnlyWhenRolesApply(t *testing.T) {
	noPipeline := config.Repo{Path: "/tmp/svc"}
	emptyFactory := config.Repo{Path: "/tmp/svc", Factory: &config.FactoryConfig{Gates: map[string]string{"unit": "true"}}}
	explicit := config.Repo{Path: "/tmp/svc", Factory: &config.FactoryConfig{Pipeline: []string{"build", "test"}}}

	cases := []struct {
		name  string
		repo  config.Repo
		roles bool
		want  []string
	}{
		{"default pipeline with roles", noPipeline, true, []string{"plan", "build", "test", "review"}},
		{"default pipeline without roles", noPipeline, false, []string{"plan", "build", "test"}},
		{"factory with gates but no pipeline, roles on", emptyFactory, true, []string{"plan", "build", "test", "review"}},
		{"explicit pipeline with roles", explicit, true, []string{"build", "test"}},
		{"explicit pipeline without roles", explicit, false, []string{"build", "test"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PipelineFor(tc.repo, tc.roles); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PipelineFor(roles=%v) = %v, want %v", tc.roles, got, tc.want)
			}
		})
	}
}

// claudeStub writes an always-succeeding stub named claude whose output ends
// with a PASS verdict, so it can stand in for any phase, beta included.
func claudeStub(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'VERDICT: PASS'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func rolesProject(repoPath, agent string, factory *config.FactoryConfig) *config.Project {
	return &config.Project{
		Name:     "p",
		Defaults: config.ProjectDefaults{Agent: agent},
		Repos:    map[string]config.Repo{"svc": {Path: repoPath, Factory: factory}},
	}
}

func payloadKeys(t *testing.T, p store.PhaseRecord) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(p.Payload, &m); err != nil {
		t.Fatalf("phase %s payload is not a JSON object: %v", p.Name, err)
	}
	return m
}

// The engine copies the caller's preflight result into the agent phases it
// runs: a build runs as alpha and records the probed version.
func TestEngineRunsPhasesWithTheRolesItsCallerSet(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	eng := newEngine(t, rolesProject(repo, claudeStub(t, base), &config.FactoryConfig{Pipeline: []string{"plan", "build", "review"}}))
	eng.GauntletRoles = true
	eng.GauntletVersion = "0.1.0"
	createTestRun(t, eng, "p", "run-roles", "running")

	if err := eng.RunStage(context.Background(), "run-roles", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}

	want := map[string][2]string{
		"plan":   {"", ""},
		"build":  {"gauntlet:alpha", "0.1.0"},
		"review": {"gauntlet:beta", "0.1.0"},
	}
	for name, w := range want {
		p := latestPhaseByName(t, eng, "run-roles", name)
		if p == nil {
			t.Fatalf("no %s phase recorded", name)
		}
		m := payloadKeys(t, *p)
		if m["role"] != w[0] || m["plugin_version"] != w[1] {
			t.Errorf("%s payload role/plugin_version = %v/%v, want %q/%q", name, m["role"], m["plugin_version"], w[0], w[1])
		}
	}
}

// Roles are set only by callers. A claude project whose roles apply still
// runs role-less when the caller left the engine's fields unset — the engine
// never derives roles from the project itself.
func TestEngineNeverDerivesRolesFromTheProject(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	proj := rolesProject(repo, claudeStub(t, base), &config.FactoryConfig{Pipeline: []string{"build"}})
	if !proj.GauntletRolesApply() {
		t.Fatal("fixture precondition: roles must apply to this project")
	}
	eng := newEngine(t, proj)
	createTestRun(t, eng, "p", "run-no-roles", "running")

	if err := eng.RunStage(context.Background(), "run-no-roles", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	m := payloadKeys(t, *latestPhaseByName(t, eng, "run-no-roles", "build"))
	if m["role"] != "" || m["plugin_version"] != "" {
		t.Errorf("build ran with role %v / version %v though no caller set roles", m["role"], m["plugin_version"])
	}
}

// Scenario: Default pipeline with roles, as executed: a repo listing no
// pipeline runs plan, build, test and then review.
func TestEngineRunsReviewAfterTheDefaultPipelineWhenRolesAreOn(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	eng := newEngine(t, rolesProject(repo, claudeStub(t, base), nil))
	eng.GauntletRoles = true
	eng.GauntletVersion = "0.1.0"
	createTestRun(t, eng, "p", "run-default", "running")

	if err := eng.RunStage(context.Background(), "run-default", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	phases, err := eng.Store.ListPhasesForRun("run-default")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range phases {
		if p.Status == "running" {
			continue
		}
		order = append(order, p.Name)
	}
	if want := []string{"plan", "build", "test", "review"}; !reflect.DeepEqual(order, want) {
		t.Errorf("phases run = %v, want %v", order, want)
	}
}

// Engine.FixCycle stamps every phase the engine records, agent and gate
// alike, including the red/green gate runner.
func TestEngineStampsItsFixCycleOnEveryPhase(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	eng := newEngine(t, rolesProject(repo, claudeStub(t, base), &config.FactoryConfig{
		Pipeline: []string{"build", "test"},
		Gates:    map[string]string{"unit": "true"},
	}))
	eng.FixCycle = 3
	createTestRun(t, eng, "p", "run-cycle", "running")

	if err := eng.RunStage(context.Background(), "run-cycle", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	if _, err := eng.RecordGreenState(context.Background(), "run-cycle", "svc"); err != nil {
		t.Fatalf("RecordGreenState: %v", err)
	}
	phases, err := eng.Store.ListPhasesForRun("run-cycle")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range phases {
		seen[p.Name] = true
		if p.FixCycle != 3 {
			t.Errorf("phase %s (%s) recorded fix cycle %d, want 3", p.Name, p.Kind, p.FixCycle)
		}
	}
	for _, name := range []string{"build", "unit", GatePhaseName(StageGreen, "unit")} {
		if !seen[name] {
			t.Errorf("no %s phase recorded; got %v", name, seen)
		}
	}
}
