package runner

// Tests for the review-scope slice of claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 6): the diff a reviewer sees is the
// run's whole change since its base, a large claude prompt travels on stdin,
// role-phase prompts live outside the worktree, and a role phase's own
// envelope file is consumed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffFiles lists the paths named by the diff's "diff --git" headers.
func diffFiles(diff string) []string {
	var files []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if i := strings.LastIndex(line, " b/"); i >= 0 {
				files = append(files, line[i+3:])
			}
		}
	}
	return files
}

// Scenario: Committed and new files reach the reviewer, at the
// DiffAgainstBase seam. A committed change to a.go, an uncommitted change to
// b.go and an untracked c_test.go are all in the diff; nothing under .sgt/
// is; and the worktree's real index is untouched.
func TestDiffAgainstBaseCoversCommittedModifiedAndUntrackedWork(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	runGit(t, dir, "checkout", "-q", "-b", "run")

	writeFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc A() {}\n")
	runGit(t, dir, "commit", "-q", "-am", "build: a")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n\nfunc B() {}\n")
	writeFile(t, filepath.Join(dir, "c_test.go"), "package c\n\nfunc TestC() {}\n")
	writeFile(t, filepath.Join(dir, ".sgt", "envelope.json"), `{"summary":"beta wrote this"}`)
	writeFile(t, filepath.Join(dir, ".sgt", "prompt_build.txt"), "the builder's brief")

	indexPath := filepath.Join(dir, ".git", "index")
	hashBefore := fileHash(t, indexPath)
	cachedBefore := gitOut(t, dir, "diff", "--cached")

	pr := &PhaseRunner{Worktree: dir, BaseBranch: "main"}
	diff, err := pr.DiffAgainstBase(context.Background())
	if err != nil {
		t.Fatalf("DiffAgainstBase: %v", err)
	}

	files := strings.Join(diffFiles(diff), ",")
	if files != "a.go,b.go,c_test.go" {
		t.Errorf("diff covers files %q, want a.go,b.go,c_test.go\n%s", files, diff)
	}
	for _, want := range []string{"func A() {}", "func B() {}", "func TestC() {}"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, ".sgt/") || strings.Contains(diff, "beta wrote this") {
		t.Errorf("diff includes .sgt/ content:\n%s", diff)
	}

	if got := fileHash(t, indexPath); got != hashBefore {
		t.Errorf("worktree index changed: hash %s before, %s after", hashBefore, got)
	}
	if got := gitOut(t, dir, "diff", "--cached"); got != cachedBefore {
		t.Errorf("git diff --cached changed: %q before, %q after", cachedBefore, got)
	}
	if status := gitOut(t, dir, "status", "--porcelain", "--", "c_test.go"); !strings.HasPrefix(status, "??") {
		t.Errorf("c_test.go status = %q, want it still untracked", status)
	}
}

// The same holds in a linked worktree, which is what every run uses: its
// index lives under the main repository's .git/worktrees/<name>/.
func TestDiffAgainstBaseLeavesALinkedWorktreesIndexAlone(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	runGit(t, base, "init", "-q", "-b", "main", repo)
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	wt := filepath.Join(base, "run-1", "svc")
	runGit(t, repo, "worktree", "add", "-q", "-b", "run-1", wt, "main")

	writeFile(t, filepath.Join(wt, "new_test.go"), "package a\n")
	indexPath := strings.TrimSpace(gitOut(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	hashBefore := fileHash(t, indexPath)

	diff, err := (&PhaseRunner{Worktree: wt, BaseBranch: "main"}).DiffAgainstBase(context.Background())
	if err != nil {
		t.Fatalf("DiffAgainstBase: %v", err)
	}
	if !strings.Contains(diff, "new_test.go") {
		t.Errorf("diff lacks the untracked new_test.go:\n%s", diff)
	}
	if got := fileHash(t, indexPath); got != hashBefore {
		t.Errorf("linked worktree index changed: hash %s before, %s after", hashBefore, got)
	}
}

// With no recorded base branch the diff falls back to the change since HEAD,
// which still includes untracked files.
func TestDiffAgainstBaseWithoutABaseBranchIncludesUntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "seed")
	writeFile(t, filepath.Join(dir, "fresh.go"), "package fresh\n")
	writeFile(t, filepath.Join(dir, ".sgt", "plan.json"), "{}")

	diff, err := (&PhaseRunner{Worktree: dir}).DiffAgainstBase(context.Background())
	if err != nil {
		t.Fatalf("DiffAgainstBase: %v", err)
	}
	if files := strings.Join(diffFiles(diff), ","); files != "fresh.go" {
		t.Errorf("diff covers files %q, want fresh.go\n%s", files, diff)
	}
}

// A base branch that git cannot resolve is an error, never a silently
// narrower diff.
func TestDiffAgainstBaseErrorsWhenTheBaseBranchIsUnknown(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "seed")

	if _, err := (&PhaseRunner{Worktree: dir, BaseBranch: "no-such-branch"}).DiffAgainstBase(context.Background()); err == nil {
		t.Fatal("expected an error for an unresolvable base branch, got nil")
	}
}

// A claude prompt over 64 KiB goes on stdin, not as an argument; one at the
// limit stays an argument; other harnesses always take it as an argument.
func TestBuildAgentCommandForRolePutsLargeClaudePromptsOnStdin(t *testing.T) {
	limit := 64 << 10
	large := strings.Repeat("x", limit+1)
	atLimit := strings.Repeat("y", limit)

	_, args, _, stdin := BuildAgentCommandForRole("claude", "", roleBeta, large)
	if stdin != large {
		t.Errorf("claude prompt of %d bytes: stdin has %d bytes, want the whole prompt", len(large), len(stdin))
	}
	for _, a := range args {
		if a == large {
			t.Error("claude prompt over 64 KiB was also passed as an argument")
		}
	}
	if v, ok := agentFlag(args); !ok || v != roleBeta {
		t.Errorf("args %v lost --agent %s", args, roleBeta)
	}

	_, args, _, stdin = BuildAgentCommandForRole("/opt/stub/claude", "", "", atLimit)
	if stdin != "" || args[len(args)-1] != atLimit {
		t.Errorf("claude prompt of exactly 64 KiB: stdin %d bytes, last arg %d bytes; want it as the last argument", len(stdin), len(args[len(args)-1]))
	}

	for _, agent := range []string{"opencode", "codex", "goose", "pi", "copilot", "aider"} {
		_, args, _, stdin := BuildAgentCommandForRole(agent, "", "", large)
		if stdin != "" {
			t.Errorf("%s: stdin = %d bytes, want the prompt as an argument", agent, len(stdin))
		}
		found := false
		for _, a := range args {
			if a == large {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the prompt is not among the arguments", agent)
		}
	}
}

// Risk R8: the three-value BuildAgentCommand has no stdin to return, so it
// must never drop a large claude prompt; it keeps the argument form.
func TestBuildAgentCommandNeverDropsALargeClaudePrompt(t *testing.T) {
	large := strings.Repeat("z", 300<<10)
	_, args, _ := BuildAgentCommand("claude", "", large)
	if len(args) == 0 || args[len(args)-1] != large {
		t.Fatalf("BuildAgentCommand(claude, 300 KiB prompt) args do not end with the prompt")
	}
}

// RunAgentPhase feeds the stdin form to the process: the stub reports the
// number of bytes it read from stdin and the number of arguments it got.
func TestRunAgentPhaseDeliversALargeClaudePromptOnStdin(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	binDir := filepath.Join(dir, "bin")
	exe, _ := recordingAgent(t, mkdir(t, binDir), "claude",
		`echo "STDIN_BYTES=$(wc -c | tr -d ' ')"; echo "ARGC=$#"; echo 'VERDICT: PASS'`)
	wt := mkdir(t, filepath.Join(dir, "run-1", "svc"))

	prompt := strings.Repeat("p", 100<<10)
	pr := &PhaseRunner{Store: st, Router: handoff.NewRouter(filepath.Join(dir, "handoff")),
		Worktree: wt, RepoName: "svc", RunID: "run-1", AgentCLI: exe}
	env, _, err := pr.RunAgentPhase(context.Background(), "plan", prompt, 0)
	if err != nil {
		t.Fatalf("RunAgentPhase: %v", err)
	}
	raw, _ := payloadMap(t, env.Payload)["raw_output"].(string)
	if !strings.Contains(raw, "STDIN_BYTES=102400") {
		t.Errorf("stub did not read the 100 KiB prompt from stdin; output:\n%s", raw)
	}
	if !strings.Contains(raw, "ARGC=3") {
		t.Errorf("stub got the prompt as an argument too; output:\n%s", raw)
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rolePhaseRunner builds a roles-on claude runner whose worktree is
// <base>/run-1/svc, the layout FleetDir gives every run.
func rolePhaseRunner(t *testing.T, base, body string) (*PhaseRunner, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exe, _ := recordingAgent(t, mkdir(t, filepath.Join(base, "bin")), "claude", body)
	wt := filepath.Join(base, "run-1", "svc")
	runGit(t, base, "init", "-q", "-b", "main", wt)
	runGit(t, wt, "commit", "--allow-empty", "-q", "-m", "seed")
	return &PhaseRunner{
		Store: st, Router: handoff.NewRouter(filepath.Join(base, "handoff")),
		Worktree: wt, RepoName: "svc", RunID: "run-1", AgentCLI: exe,
		GauntletRoles: true, GauntletVersion: "0.1.0",
	}, wt
}

// Role-phase prompts are written beside the worktrees, under
// <run dir>/.prompts/<repo>/prompt_<phase>_<attempt>.txt, never into the
// worktree's .sgt/, and the envelope's Artifacts names that path.
func TestRolePhasePromptsAreWrittenOutsideTheWorktree(t *testing.T) {
	base := t.TempDir()
	pr, wt := rolePhaseRunner(t, base, `exit 1`)

	_, _, err := pr.RunAgentPhase(context.Background(), "review", "BAR-TEXT VERDICT: contract", 1)
	if err == nil {
		t.Fatal("a failing stub should fail the phase")
	}

	for _, attempt := range []string{"1", "2"} {
		path := filepath.Join(base, "run-1", ".prompts", "svc", "prompt_review_"+attempt+".txt")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("attempt %s prompt not written to %s: %v", attempt, path, err)
		}
		if !strings.Contains(string(data), "BAR-TEXT") {
			t.Errorf("%s = %q, want the prompt", path, data)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, ".sgt", "prompt_review.txt")); err == nil {
		t.Error("a role phase left .sgt/prompt_review.txt in the worktree")
	}
	_ = filepath.Walk(wt, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			if data, _ := os.ReadFile(path); strings.Contains(string(data), "BAR-TEXT") {
				t.Errorf("worktree file %s carries the role prompt", path)
			}
		}
		return nil
	})

	envs, err := pr.Store.ListEnvelopesForRun("run-1")
	if err != nil || len(envs) == 0 {
		t.Fatalf("no envelopes recorded: %v", err)
	}
	for _, e := range envs {
		for _, a := range e.Artifacts {
			if !strings.HasPrefix(a, filepath.Join(base, "run-1", ".prompts", "svc")+string(filepath.Separator)) {
				t.Errorf("envelope artifact %q is not under the run's .prompts directory", a)
			}
		}
	}
}

// A role-less phase keeps .sgt/prompt_<phase>.txt, as before.
func TestRolelessPhasePromptStaysInTheWorktree(t *testing.T) {
	base := t.TempDir()
	pr, wt := rolePhaseRunner(t, base, `exit 0`)

	if _, _, err := pr.RunAgentPhase(context.Background(), "plan", "PLAN-TEXT", 0); err != nil {
		t.Fatalf("RunAgentPhase: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, ".sgt", "prompt_plan.txt"))
	if err != nil || !strings.Contains(string(data), "PLAN-TEXT") {
		t.Errorf(".sgt/prompt_plan.txt = %q, %v; want the plan prompt", data, err)
	}
	if _, err := os.Stat(filepath.Join(base, "run-1", ".prompts")); err == nil {
		t.Error("a role-less phase wrote under .prompts")
	}
}

// A role phase's own .sgt/envelope.json is deleted once read, so a later
// commit cannot carry it onto the run branch. Its content is still what the
// phase recorded.
func TestRolePhaseConsumesItsOwnEnvelopeFile(t *testing.T) {
	base := t.TempDir()
	pr, wt := rolePhaseRunner(t, base,
		`mkdir -p .sgt; printf '{"summary":"beta envelope","payload":{"note":"from beta"}}' > .sgt/envelope.json; echo 'VERDICT: PASS'`)

	env, _, err := pr.RunAgentPhase(context.Background(), "review", "review it", 0)
	if err != nil {
		t.Fatalf("RunAgentPhase: %v", err)
	}
	if env.Summary != "beta envelope" {
		t.Errorf("envelope summary = %q, want the one beta wrote", env.Summary)
	}
	if _, err := os.Stat(filepath.Join(wt, ".sgt", "envelope.json")); !os.IsNotExist(err) {
		t.Errorf(".sgt/envelope.json still present after a role phase read it (stat err %v)", err)
	}
}

// A role phase whose process failed never has its envelope read, and it is
// still not left behind.
func TestFailedRolePhaseDoesNotLeaveItsEnvelopeFile(t *testing.T) {
	base := t.TempDir()
	pr, wt := rolePhaseRunner(t, base,
		`mkdir -p .sgt; printf '{"summary":"half done"}' > .sgt/envelope.json; exit 3`)

	if _, _, err := pr.RunAgentPhase(context.Background(), "build", "build it", 0); err == nil {
		t.Fatal("a non-zero exit should fail the phase")
	}
	if _, err := os.Stat(filepath.Join(wt, ".sgt", "envelope.json")); !os.IsNotExist(err) {
		t.Errorf(".sgt/envelope.json left behind by a failed role phase (stat err %v)", err)
	}
}
