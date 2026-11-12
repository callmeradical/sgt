package dag

// Tests for slice beta-verdict of change claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 6): the review prompt beta receives,
// the brief it is judged against, and what of beta's output reaches a
// downstream repo's worktree.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// The JSON-findings instruction of the role-less review prompt.
const jsonFindingsInstruction = "Report findings as JSON"

// Scenario: The roles review prompt has one output contract.
func TestRolesReviewPromptHasOneOutputContract(t *testing.T) {
	const brief = "# Intent\n\nadd webhook retries\n"
	const diff = "diff --git a/webhook.go b/webhook.go\n+func retry() {}\n"
	stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}}

	prompt := reviewPrompt(diff, brief, stage, "svc", true)

	for _, want := range []string{brief, diff, "VERDICT: PASS", "VERDICT: HOLD: <gap>", "worktree"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("roles review prompt = %q, want it to contain %q", prompt, want)
		}
	}
	for _, banned := range []string{jsonFindingsInstruction, `{"findings"`, "nothing else"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("roles review prompt contains %q:\n%s", banned, prompt)
		}
	}
	if strings.Index(prompt, brief) > strings.Index(prompt, diff) {
		t.Error("roles review prompt gives the diff before the bar")
	}
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "VERDICT:") {
		t.Errorf("roles review prompt ends %q, want the VERDICT: contract last", last)
	}
	if strings.Count(prompt, "End your output with") != 1 {
		t.Errorf("roles review prompt states the output contract %d times, want once", strings.Count(prompt, "End your output with"))
	}
}

// With roles off the review prompt is exactly today's, whatever brief it is
// handed.
func TestRoleLessReviewPromptIsUnchanged(t *testing.T) {
	const diff = "diff --git a/a.go b/a.go\n+x\n"
	stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}}
	want := "# Phase objective: review\n\n" +
		"You are executing the \"review\" phase: judge the diff below, nothing else. " +
		"Do not modify files, do not commit, do not push, and do not open a pull request " +
		"— report findings only.\n\n" +
		"Review this diff for repo svc against its intent and OpenSpec change, if one is referenced. " +
		"Judge only what is in the diff and the referenced spec — you have not seen and must not assume " +
		"the implementing agent's own reasoning. Report findings as JSON: " +
		"{\"findings\":[{\"axis\":...,\"severity\":\"error\"|\"warning\"|\"info\",\"summary\":...,\"disposition\":...}]}.\n\nDiff:\n" + diff

	if got := reviewPrompt(diff, "BRIEF-NOT-USED", stage, "svc", false); got != want {
		t.Errorf("role-less review prompt =\n%s\nwant exactly\n%s", got, want)
	}
}

// promptCapturingClaude writes a stub named claude that saves the prompt it
// received (its last argument, or stdin when it has none) to
// $PROMPT_LOG/<role>.txt, where <role> is "alpha", "beta" or "none". As beta
// it prints BETA-REASONING and then reviewOutput's verdict; otherwise it
// prints BUILD-OUTPUT, appends to README.md, and writes an envelope whose
// summary is BUILD-ENVELOPE.
func promptCapturingClaude(t *testing.T, dir, logDir string) string {
	t.Helper()
	t.Setenv("PROMPT_LOG", logDir)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
role=none
prev=
last=
for a in "$@"; do
  if [ "$prev" = "--agent" ]; then role="${a#gauntlet:}"; fi
  prev="$a"
  last="$a"
done
case "$last" in
  --*) cat > "$PROMPT_LOG/$role.txt" ;;
  *) printf '%s' "$last" > "$PROMPT_LOG/$role.txt" ;;
esac
if [ "$role" = beta ]; then
  echo 'BETA-REASONING: the change looks complete'
  echo 'VERDICT: PASS'
  exit 0
fi
echo 'BUILD-OUTPUT'
echo 'built change' >> README.md
mkdir -p .sgt
printf '{"repo":"%s","stage":"build","summary":"BUILD-ENVELOPE","payload":{"notes":"BUILD-ENVELOPE"}}' "$(basename "$PWD")" > .sgt/envelope.json
exit 0
`
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func capturedPrompt(t *testing.T, logDir, role string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, role+".txt"))
	if err != nil {
		t.Fatalf("no prompt captured for %s: %v", role, err)
	}
	return string(data)
}

// Scenario: The review prompt carries the intent brief.
func TestRolesReviewPromptCarriesTheIntentBriefAndNotTheBuildsOutput(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)
	logDir := filepath.Join(base, "prompts")

	eng := newEngine(t, rolesProject(repo, promptCapturingClaude(t, base, logDir), &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles = true
	eng.GauntletVersion = "0.1.0"

	const intentID = "intent-review-brief"
	if err := eng.Store.CreateIntent(&store.IntentRecord{
		ID: intentID, Project: "p", Statement: "add webhook retries", Status: "in_progress",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Store.CreateBullet(&store.BulletRecord{
		ID: intentID + "-b1", IntentID: intentID, Repo: "svc", Position: 1, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	const runID = "run-review-brief"
	if err := eng.Store.CreateRun(&store.RunRecord{
		ID: runID, Project: "p", TaskID: runID, Status: "running",
		Type: testWorkType, ChangeID: runID, IntentID: intentID,
	}); err != nil {
		t.Fatal(err)
	}

	stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}, Brief: "raw operator text"}
	if err := eng.RunStage(context.Background(), runID, stage); err != nil {
		t.Fatalf("RunStage: %v", err)
	}

	brief, err := eng.Store.RenderIntentBrief(intentID, "svc", nil)
	if err != nil {
		t.Fatal(err)
	}
	review := capturedPrompt(t, logDir, "beta")
	for _, want := range []string{brief, "+built change", "VERDICT:"} {
		if !strings.Contains(review, want) {
			t.Errorf("review prompt does not contain %q:\n%s", want, review)
		}
	}
	for _, leaked := range []string{"BUILD-OUTPUT", "BUILD-ENVELOPE", "raw operator text"} {
		if strings.Contains(review, leaked) {
			t.Errorf("review prompt contains %q:\n%s", leaked, review)
		}
	}
}

// With no intent (sgt run), beta judges against the same source the build
// used: the stage's brief, else the generic build prompt.
func TestRolesReviewPromptWithNoIntentUsesTheBuildsBrief(t *testing.T) {
	for _, c := range []struct{ name, stageBrief, want string }{
		{"stage brief", "do the work exactly as typed", "do the work exactly as typed"},
		{"generic prompt", "", "Execute build phase for stage s on svc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SGT_FLEET_DIR", t.TempDir())
			base := t.TempDir()
			repo := filepath.Join(base, "svc")
			newGitRepo(t, repo)
			logDir := filepath.Join(base, "prompts")

			eng := newEngine(t, rolesProject(repo, promptCapturingClaude(t, base, logDir), &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
			eng.GauntletRoles = true
			eng.GauntletVersion = "0.1.0"
			createTestRun(t, eng, "p", "run-no-intent", "running")

			stage := &config.DAGStage{Name: "s", Repos: []string{"svc"}, Brief: c.stageBrief}
			if err := eng.RunStage(context.Background(), "run-no-intent", stage); err != nil {
				t.Fatalf("RunStage: %v", err)
			}
			if build := capturedPrompt(t, logDir, "alpha"); !strings.Contains(build, c.want) {
				t.Fatalf("fixture precondition: build prompt does not contain %q", c.want)
			}
			if review := capturedPrompt(t, logDir, "beta"); !strings.Contains(review, c.want) {
				t.Errorf("review prompt does not contain the build's brief %q:\n%s", c.want, review)
			}
		})
	}
}

// Scenario: The build prompt carries no review contract.
func TestBuildPromptCarriesNoReviewContract(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)
	logDir := filepath.Join(base, "prompts")

	eng := newEngine(t, rolesProject(repo, promptCapturingClaude(t, base, logDir), &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles = true
	eng.GauntletVersion = "0.1.0"
	createTestRun(t, eng, "p", "run-build-prompt", "running")

	if err := eng.RunStage(context.Background(), "run-build-prompt", &config.DAGStage{Name: "s", Repos: []string{"svc"}, Brief: "add retries"}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	if build := capturedPrompt(t, logDir, "alpha"); strings.Contains(build, "VERDICT:") {
		t.Errorf("build prompt contains the review contract:\n%s", build)
	}
}

// Scenario: Beta's output does not reach a downstream worktree.
func TestBetasOutputDoesNotReachADownstreamWorktree(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repoA := filepath.Join(base, "a")
	repoB := filepath.Join(base, "b")
	newGitRepo(t, repoA)
	newGitRepo(t, repoB)
	agent := promptCapturingClaude(t, base, filepath.Join(base, "prompts"))

	proj := &config.Project{
		Name:     "p",
		Defaults: config.ProjectDefaults{Agent: agent},
		Repos: map[string]config.Repo{
			"a": {Path: repoA, Factory: &config.FactoryConfig{Pipeline: []string{"build", "review"}}},
			"b": {Path: repoB, Factory: &config.FactoryConfig{Pipeline: []string{"build"}}},
		},
	}
	eng := newEngine(t, proj)
	eng.GauntletRoles = true
	eng.GauntletVersion = "0.1.0"
	const runID = "run-downstream"
	createTestRun(t, eng, "p", runID, "running")

	if err := eng.RunStage(context.Background(), runID, &config.DAGStage{Name: "first", Repos: []string{"a"}}); err != nil {
		t.Fatalf("stage a: %v", err)
	}
	if err := eng.RunStage(context.Background(), runID, &config.DAGStage{Name: "second", Repos: []string{"b"}, After: []string{"a"}}); err != nil {
		t.Fatalf("stage b: %v", err)
	}

	handoffDir := filepath.Join(FleetDir(runID, "b"), ".sgt", "handoff")
	var files int
	err := filepath.Walk(handoffDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "BETA-REASONING") {
			t.Errorf("%s carries beta's output", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("fixture precondition: a's handoff never reached b's worktree")
	}
}
