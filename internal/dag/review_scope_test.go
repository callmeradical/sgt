package dag

// Tests for the review-scope slice of claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 6), at the Engine.RunStage review case
// and CommitRunOutput: what diff reaches the reviewer, how a long one is cut
// and delivered, and what a role phase leaves on the run branch.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/config"
)

// roleStub writes a stub claude that runs alphaBody when invoked as
// gauntlet:alpha and betaBody when invoked as gauntlet:beta.
func roleStub(t *testing.T, dir, alphaBody, betaBody string) string {
	t.Helper()
	p := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"role=''\nprev=''\nfor a in \"$@\"; do [ \"$prev\" = --agent ] && role=\"$a\"; prev=\"$a\"; done\n" +
		"case \"$role\" in\n" +
		"gauntlet:alpha)\n" + alphaBody + "\n;;\n" +
		"gauntlet:beta)\n" + betaBody + "\n;;\n" +
		"esac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func gitIdentity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
}

// rolePrompt reads the prompt a role phase was given, from the run's
// .prompts directory beside its worktrees.
func rolePrompt(t *testing.T, runID, repo, phase string, attempt int) string {
	t.Helper()
	path := filepath.Join(FleetRoot(), runID, ".prompts", repo, fmt.Sprintf("prompt_%s_%d.txt", phase, attempt))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s prompt: %v", phase, err)
	}
	return string(data)
}

func rawOutput(t *testing.T, eng *Engine, runID, phase string) string {
	t.Helper()
	p := latestPhaseByName(t, eng, runID, phase)
	if p == nil {
		t.Fatalf("no %s phase recorded", phase)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(p.Payload, &m)
	s, _ := m["raw_output"].(string)
	return s
}

// bigFileScript writes name with roughly kib KiB of distinct lines.
func bigFileScript(name string, kib int) string {
	return fmt.Sprintf(`awk 'BEGIN{for(i=0;i<%d;i++) printf "line %%06d of the generated fixture, padded out to sixty-four bytes\n", i}' > %s`, kib*16, name)
}

const truncationMarkerFormat = "[diff truncated at %s; read the remaining files in the worktree]"

// Scenario: A long diff is capped and still delivered. A claude review of a
// 300 KiB diff gets the 256 KiB cap and its marker, names the file that was
// cut, and the process starts and reads the whole prompt from stdin.
func TestLongClaudeReviewDiffIsCappedAndDeliveredOnStdin(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	stub := roleStub(t, base,
		"echo 'package small' > small.go\n"+bigFileScript("zz_big.txt", 300),
		`echo "STDIN_BYTES=$(wc -c | tr -d ' ')"; echo "ARGC=$#"; echo 'VERDICT: PASS'`)
	eng := newEngine(t, rolesProject(repo, stub, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles, eng.GauntletVersion = true, "0.1.0"
	createTestRun(t, eng, "p", "run-long", "running")

	if err := eng.RunStage(context.Background(), "run-long", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	if p := latestPhaseByName(t, eng, "run-long", "review"); p == nil || p.Status != "passed" {
		t.Fatalf("review phase = %+v, want passed", p)
	}

	prompt := rolePrompt(t, "run-long", "svc", "review", 1)
	if marker := fmt.Sprintf(truncationMarkerFormat, "256 KiB"); !strings.Contains(prompt, marker) {
		t.Errorf("review prompt lacks %q", marker)
	}
	if !strings.Contains(prompt, "small.go") {
		t.Error("review prompt lost small.go, which fits under the cap")
	}
	if !regexp.MustCompile(`(?m)^Files cut from the diff:.*zz_big\.txt`).MatchString(prompt) {
		t.Errorf("review prompt does not name zz_big.txt as cut; tail:\n%s", tail(prompt, 600))
	}
	if strings.Contains(prompt, fmt.Sprintf("line %06d", 300*16-1)) {
		t.Error("review prompt carries the diff's last line; it was not cut")
	}

	out := rawOutput(t, eng, "run-long", "review")
	if want := fmt.Sprintf("STDIN_BYTES=%d", len(prompt)); !strings.Contains(out, want) {
		t.Errorf("review agent output %q, want %s (the whole prompt on stdin)", out, want)
	}
	if !strings.Contains(out, "ARGC=5") {
		t.Errorf("review agent output %q, want ARGC=5 (no prompt argument)", out)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Every other harness takes the prompt as one argument, so the whole review
// prompt stays under 120 KiB, with the diff given at most 96 KiB.
func TestLongReviewDiffForAnArgumentHarnessKeepsThePromptUnder120KiB(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	agent := filepath.Join(base, "goose")
	// The build also leaves a plan file, so RunAgentPhase appends its plan
	// instructions to the review prompt: the budget covers the final prompt.
	script := "#!/bin/sh\ncase \"$*\" in\n*'Phase objective: build'*) mkdir -p .sgt; echo '{}' > .sgt/plan.json; " + bigFileScript("zz_big.txt", 200) + " ;;\nesac\nexit 0\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, rolesProject(repo, agent, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	createTestRun(t, eng, "p", "run-arg", "running")

	if err := eng.RunStage(context.Background(), "run-arg", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	prompt := readPromptFile(t, "run-arg", "svc", "review")
	if !strings.Contains(prompt, ".sgt/plan.json") {
		t.Fatal("fixture precondition: the review prompt carries no plan instructions")
	}
	if len(prompt) >= 120<<10 {
		t.Errorf("review prompt is %d bytes, want under %d", len(prompt), 120<<10)
	}
	m := regexp.MustCompile(`\[diff truncated at (\d+) (KiB|bytes); read the remaining files in the worktree\]`).FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("review prompt lacks the truncation marker; tail:\n%s", tail(prompt, 600))
	}
	var capBytes int
	fmt.Sscanf(m[1], "%d", &capBytes)
	if m[2] == "KiB" {
		capBytes <<= 10
	}
	if capBytes > 96<<10 || capBytes < 64<<10 {
		t.Errorf("diff cap = %d bytes, want at most 96 KiB and the room the rest of the prompt leaves", capBytes)
	}
	if !regexp.MustCompile(`(?m)^Files cut from the diff:.*zz_big\.txt`).MatchString(prompt) {
		t.Errorf("review prompt does not name zz_big.txt as cut; tail:\n%s", tail(prompt, 600))
	}
}

// A diff that fits is passed whole, with no marker.
func TestShortReviewDiffIsNotMarkedTruncated(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	stub := roleStub(t, base, "echo 'package small' > small.go", "echo 'VERDICT: PASS'")
	eng := newEngine(t, rolesProject(repo, stub, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles, eng.GauntletVersion = true, "0.1.0"
	createTestRun(t, eng, "p", "run-short", "running")

	if err := eng.RunStage(context.Background(), "run-short", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	prompt := rolePrompt(t, "run-short", "svc", "review", 1)
	if !strings.Contains(prompt, "+package small") {
		t.Errorf("review prompt lacks the diff:\n%s", prompt)
	}
	for _, unwanted := range []string{"diff truncated", "Files cut from the diff", "may be incomplete"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("review prompt of a short diff with a recorded base says %q", unwanted)
		}
	}
}

// Scenario: Committed and new files reach the reviewer. The build commits a
// change to a.go, leaves b.go modified and creates an untracked c_test.go;
// the review prompt's diff has all three and nothing under .sgt/.
func TestCommittedModifiedAndNewFilesReachTheReviewer(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	gitIdentity(t)
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)
	for name, body := range map[string]string{"a.go": "package a\n", "b.go": "package b\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "a and b")

	stub := roleStub(t, base,
		"echo 'func A() {}' >> a.go && git commit -q -am 'build: a'\n"+
			"echo 'func B() {}' >> b.go\n"+
			"echo 'func TestC() {}' > c_test.go\n"+
			"mkdir -p .sgt && echo 'SGT-STATE-NOTE' > .sgt/notes.txt",
		"echo 'VERDICT: PASS'")
	eng := newEngine(t, rolesProject(repo, stub, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles, eng.GauntletVersion = true, "0.1.0"
	createTestRun(t, eng, "p", "run-scope", "running")

	if err := eng.RunStage(context.Background(), "run-scope", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	prompt := rolePrompt(t, "run-scope", "svc", "review", 1)
	for _, want := range []string{"diff --git a/a.go b/a.go", "+func A() {}", "diff --git a/b.go b/b.go", "+func B() {}", "diff --git a/c_test.go b/c_test.go", "+func TestC() {}"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, ".sgt/") || strings.Contains(prompt, "SGT-STATE-NOTE") {
		t.Error("review prompt's diff includes .sgt/ content")
	}
}

// A run recorded before BaseBranch existed has none, so its diff covers only
// the change since HEAD; the review prompt says so.
func TestReviewWithoutABaseBranchSaysTheDiffMayBeIncomplete(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	stub := roleStub(t, base, "echo 'package small' > small.go", "echo 'VERDICT: PASS'")
	eng := newEngine(t, rolesProject(repo, stub, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles, eng.GauntletVersion = true, "0.1.0"
	createTestRun(t, eng, "p", "run-legacy", "running")
	// An existing worktree makes prepareWorktree return before it records a
	// base branch, as for a run whose worktree predates the field.
	if err := os.MkdirAll(filepath.Join(FleetRoot(), "run-legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "worktree", "add", "-q", "-b", "legacy", FleetDir("run-legacy", "svc"))

	if err := eng.RunStage(context.Background(), "run-legacy", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	if run, _ := eng.Store.GetRun("run-legacy"); run.BaseBranch != "" {
		t.Fatalf("fixture precondition: run has base branch %q, want none", run.BaseBranch)
	}
	prompt := rolePrompt(t, "run-legacy", "svc", "review", 1)
	if !strings.Contains(prompt, "may be incomplete") {
		t.Errorf("review prompt does not say the diff may be incomplete:\n%s", prompt)
	}
	if !strings.Contains(prompt, "+package small") {
		t.Error("review prompt lacks the uncommitted change")
	}
}

// Scenario: A beta-written envelope is not committed. A beta that writes
// .sgt/envelope.json leaves none on the run branch once the run's output is
// committed.
func TestBetaWrittenEnvelopeIsNotCommitted(t *testing.T) {
	t.Setenv("SGT_FLEET_DIR", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "svc")
	newGitRepo(t, repo)

	stub := roleStub(t, base,
		"echo 'package small' > small.go",
		`mkdir -p .sgt; printf '{"summary":"BETA-ENVELOPE","payload":{"gauntlet_verdict":"pass"}}' > .sgt/envelope.json; echo 'VERDICT: PASS'`)
	eng := newEngine(t, rolesProject(repo, stub, &config.FactoryConfig{Pipeline: []string{"build", "review"}}))
	eng.GauntletRoles, eng.GauntletVersion = true, "0.1.0"
	createTestRun(t, eng, "p", "run-env", "running")

	if err := eng.RunStage(context.Background(), "run-env", &config.DAGStage{Name: "s", Repos: []string{"svc"}}); err != nil {
		t.Fatalf("RunStage: %v", err)
	}
	committed, _, err := CommitRunOutput(context.Background(), "run-env", "svc", "run output")
	if err != nil || !committed {
		t.Fatalf("CommitRunOutput = %v, %v; want a commit of small.go", committed, err)
	}

	wt := FleetDir("run-env", "svc")
	out, err := exec.Command("git", "-C", wt, "ls-tree", "-r", "--name-only", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	files := string(out)
	if !strings.Contains(files, "small.go") {
		t.Fatalf("run branch lacks small.go:\n%s", files)
	}
	if strings.Contains(files, ".sgt/envelope.json") {
		t.Errorf("run branch carries .sgt/envelope.json:\n%s", files)
	}
	for _, f := range strings.Fields(files) {
		if strings.HasPrefix(f, ".sgt/prompt_") {
			t.Errorf("run branch carries role prompt %s", f)
		}
	}
}

// The 120 KiB budget covers the prompt the agent actually receives, including
// what RunAgentPhase appends after the engine builds it (the plan
// instructions), when the rest of the prompt leaves the diff less than its
// 96 KiB ceiling.
func TestArgumentHarnessBudgetCoversTextAppendedAfterTheReviewPrompt(t *testing.T) {
	rest := strings.Repeat("r", 30<<10)
	appended := strings.Repeat("a", 3<<10)
	diff := "diff --git a/zz_big.txt b/zz_big.txt\n" + strings.Repeat("+line of the generated fixture\n", 200<<10/31)

	prompt := boundedReviewPrompt(diff, "goose", true, appended, func(d string) string { return rest + d })
	if total := len(prompt) + len(appended); total >= 120<<10 {
		t.Errorf("final prompt is %d bytes, want under %d", total, 120<<10)
	}
	if !strings.Contains(prompt, "[diff truncated at ") {
		t.Error("prompt lacks the truncation marker")
	}
}
