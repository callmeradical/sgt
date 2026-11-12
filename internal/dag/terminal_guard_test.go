package dag

// Tests for specs/verification-freshness/spec.md, requirements "A run never
// ends passed without a passing result for every required phase" and "The
// test entry is satisfied by gate records" (claude-is-the-default-harness-
// and-runs-gauntlet-roles, design decision 8's terminal guard), at the seam
// dag.RunVerified over seeded records.

import (
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
)

// guardProject builds a project whose repos each have the given pipeline
// (nil means none configured) and gates.
func guardProject(repos map[string]config.Repo, stages ...config.DAGStage) *config.Project {
	p := &config.Project{Name: "guard", Repos: repos}
	if len(stages) > 0 {
		p.DAG = &config.DAGConfig{Name: "guard-dag", Stages: stages}
	}
	return p
}

func factoryRepo(pipeline []string, gates ...string) config.Repo {
	f := &config.FactoryConfig{Pipeline: pipeline, Gates: map[string]string{}}
	for _, g := range gates {
		f.Gates[g] = "true"
	}
	return config.Repo{Factory: f}
}

func TestRunVerifiedPassesWhenEveryRequiredPhasePasses(t *testing.T) {
	cases := []struct {
		name  string
		proj  *config.Project
		recs  *seq
		repos []string
		roles bool
	}{
		{
			name:  "a default pipeline with roles on and every phase passing",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo(nil, "unit")}),
			recs:  newSeq().add("svc", "plan", "agent", "passed").add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").add("svc", "review", "agent", "passed"),
			repos: []string{"svc"},
			roles: true,
		},
		{
			name:  "configured gates satisfy the test entry with no record named test",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"}, "lint", "unit")}),
			recs:  newSeq().add("svc", "build", "agent", "passed").add("svc", "lint", "code", "passed").add("svc", "unit", "code", "passed"),
			repos: []string{"svc"},
		},
		{
			name:  "the placeholder test gate record satisfies a repo with no gates",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"})}),
			recs:  newSeq().add("svc", "build", "agent", "passed").add("svc", "test", "code", "passed"),
			repos: []string{"svc"},
		},
		{
			name:  "gates are not required when the pipeline has no test",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"plan", "build"}, "unit")}),
			recs:  newSeq().add("svc", "plan", "agent", "passed").add("svc", "build", "agent", "passed"),
			repos: []string{"svc"},
		},
		{
			name:  "a review before test does not make gates stale",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "review", "test"}, "unit")}),
			recs:  newSeq().add("svc", "build", "agent", "passed").add("svc", "review", "agent", "passed").add("svc", "unit", "code", "passed"),
			repos: []string{"svc"},
		},
		{
			name:  "an agent phase after test is trusted",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test", "docs"}, "unit")}),
			recs:  newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").add("svc", "docs", "agent", "passed"),
			repos: []string{"svc"},
		},
		{
			name: "a failed fix later made good does not fail the run",
			proj: guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test", "review"}, "unit")}),
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "failed").
				add("svc", "fix", "agent", "failed").
				add("svc", "unit", "code", "passed").add("svc", "review", "agent", "passed"),
			repos: []string{"svc"},
		},
		{
			name:  "fix is never required, even when a pipeline lists it",
			proj:  guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "fix", "test"}, "unit")}),
			recs:  newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed"),
			repos: []string{"svc"},
		},
		{
			name: "every accountable repo passes",
			proj: guardProject(map[string]config.Repo{
				"a": factoryRepo([]string{"build", "test"}, "unit"),
				"b": factoryRepo([]string{"build", "test"}, "unit"),
			}),
			recs: newSeq().add("a", "build", "agent", "passed").add("a", "unit", "code", "passed").
				add("b", "build", "agent", "passed").add("b", "unit", "code", "passed"),
			repos: []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := RunVerified(tc.recs.recs, tc.proj, tc.repos, tc.roles)
			if !ok {
				t.Fatalf("RunVerified = false (%q), want true", reason)
			}
			if reason != "" {
				t.Errorf("reason = %q, want empty when the run is verified", reason)
			}
		})
	}
}

func TestRunVerifiedNamesThePhaseAndRepoThatFail(t *testing.T) {
	cases := []struct {
		name   string
		proj   *config.Project
		recs   *seq
		repos  []string
		roles  bool
		reason string
	}{
		{
			name:   "a gate with no passed record newer than the repo's newest build",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"}, "unit")}),
			recs:   newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").add("svc", "build", "agent", "passed"),
			repos:  []string{"svc"},
			reason: "phase unit on svc has no passing result since the last code change",
		},
		{
			name:   "a gate with no passed record newer than the repo's newest fix",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"}, "unit")}),
			recs:   newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").add("svc", "fix", "agent", "passed"),
			repos:  []string{"svc"},
			reason: "phase unit on svc has no passing result since the last code change",
		},
		{
			name: "a stale gate after a failed fix says to start a corrective cycle",
			proj: guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"}, "unit")}),
			recs: newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").
				add("svc", "fix", "agent", "failed"),
			repos:  []string{"svc"},
			reason: "phase unit on svc has no passing result since the last code change (start a corrective cycle with POST /api/run-fix)",
		},
		{
			name:   "a work phase whose newest record failed",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"plan", "build"})}),
			recs:   newSeq().add("svc", "plan", "agent", "passed").add("svc", "build", "agent", "passed").add("svc", "build", "agent", "failed"),
			repos:  []string{"svc"},
			reason: "phase build on svc did not pass",
		},
		{
			name:   "a work phase with no record at all",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"plan", "build"})}),
			recs:   newSeq().add("svc", "plan", "agent", "passed"),
			repos:  []string{"svc"},
			reason: "phase build on svc did not pass",
		},
		{
			name:   "a gate named like a phase is not confused with it",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"}, "build")}),
			recs:   newSeq().add("svc", "build", "agent", "failed").add("svc", "build", "code", "passed"),
			repos:  []string{"svc"},
			reason: "phase build on svc did not pass",
		},
		{
			name:   "an agent phase before test makes an earlier gate pass stale",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "docs", "test"}, "unit")}),
			recs:   newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed").add("svc", "docs", "agent", "passed"),
			repos:  []string{"svc"},
			reason: "phase unit on svc has no passing result since the last code change",
		},
		{
			name:   "the placeholder test gate is required when no gates are configured",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo([]string{"build", "test"})}),
			recs:   newSeq().add("svc", "build", "agent", "passed"),
			repos:  []string{"svc"},
			reason: "phase test on svc has no passing result since the last code change",
		},
		{
			name:   "roles on adds a required review to the default pipeline",
			proj:   guardProject(map[string]config.Repo{"svc": factoryRepo(nil, "unit")}),
			recs:   newSeq().add("svc", "plan", "agent", "passed").add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed"),
			repos:  []string{"svc"},
			roles:  true,
			reason: "phase review on svc has no passing result since the last code change",
		},
		{
			name: "a repo with no records has not run",
			proj: guardProject(map[string]config.Repo{
				"a": factoryRepo([]string{"build", "test"}, "unit"),
				"b": factoryRepo([]string{"build", "test"}, "unit"),
			}),
			recs:   newSeq().add("a", "build", "agent", "passed").add("a", "unit", "code", "passed"),
			repos:  []string{"a", "b"},
			reason: "repo b has not run",
		},
		{
			name: "the first failing repo in accountable order is named",
			proj: guardProject(map[string]config.Repo{
				"a": factoryRepo([]string{"build"}),
				"b": factoryRepo([]string{"build"}),
			}),
			recs:   newSeq().add("a", "build", "agent", "failed"),
			repos:  []string{"b", "a"},
			reason: "repo b has not run",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := RunVerified(tc.recs.recs, tc.proj, tc.repos, tc.roles)
			if ok {
				t.Fatal("RunVerified = true, want false")
			}
			if reason != tc.reason {
				t.Errorf("reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestRunVerifiedReportsABulletRepoOutsideTheDAGWithoutSayingToResume(t *testing.T) {
	proj := guardProject(
		map[string]config.Repo{
			"svc": factoryRepo([]string{"build", "test"}, "unit"),
			"x":   factoryRepo([]string{"build", "test"}, "unit"),
		},
		config.DAGStage{Name: "only-svc", Repos: []string{"svc"}},
	)
	recs := newSeq().add("svc", "build", "agent", "passed").add("svc", "unit", "code", "passed")

	ok, reason := RunVerified(recs.recs, proj, []string{"svc", "x"}, false)
	if ok {
		t.Fatal("RunVerified = true, want false: x has a bullet but no stage can run it")
	}
	want := "repo x has a bullet but is in no stage of this project's DAG; it cannot run"
	if reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
	if strings.Contains(strings.ToLower(reason), "resume") {
		t.Errorf("reason %q says to resume, but no resume can ever run x", reason)
	}
}

// A repo outside the DAG is a structural problem no resume repairs, so it is
// reported even when an earlier accountable repo merely has not run yet.
func TestRunVerifiedReportsARepoOutsideTheDAGBeforeAnUnrunRepo(t *testing.T) {
	proj := guardProject(
		map[string]config.Repo{
			"a": factoryRepo([]string{"build"}),
			"b": factoryRepo([]string{"build"}),
			"x": factoryRepo([]string{"build"}),
		},
		config.DAGStage{Name: "s-a", Repos: []string{"a"}},
		config.DAGStage{Name: "s-b", Repos: []string{"b"}, After: []string{"s-a"}},
	)
	recs := newSeq().add("a", "build", "agent", "passed")

	ok, reason := RunVerified(recs.recs, proj, []string{"a", "b", "x"}, false)
	if ok {
		t.Fatal("RunVerified = true, want false")
	}
	if want := "repo x has a bullet but is in no stage of this project's DAG; it cannot run"; reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
}
