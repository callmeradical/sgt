package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaxClaudeArgPrompt is the largest prompt passed to claude as a command-line
// argument. A longer prompt goes on stdin instead (claude --print reads stdin
// when it is given no prompt argument), so Linux's 128 KiB single-argument
// limit can never stop the process from starting (design decision 6).
const MaxClaudeArgPrompt = 64 << 10

// reviewPathspec limits a review diff to the repository, minus sgt's own
// .sgt/ state directory (prompts, envelopes, plan, artifacts).
var reviewPathspec = []string{"--", ".", ":(exclude).sgt"}

// DiffAgainstBase returns the run's whole change, for a review phase's
// prompt: everything between the merge base of BaseBranch and HEAD and the
// working tree. That is committed work, uncommitted changes to tracked files
// and untracked files alike, with .sgt/ excluded.
//
// Untracked files are included through a throwaway copy of the worktree's
// index (GIT_INDEX_FILE) marked with `git add --intent-to-add --all`, so the
// worktree's real index is never touched.
//
// With an empty BaseBranch (runs recorded before it existed) the base is
// HEAD, so the diff covers only uncommitted and untracked work and may miss
// commits the run already made. A BaseBranch git cannot resolve is an error,
// never a silently narrower diff.
func (pr *PhaseRunner) DiffAgainstBase(ctx context.Context) (string, error) {
	base := "HEAD"
	if pr.BaseBranch != "" {
		out, err := exec.CommandContext(ctx, "git", "-C", pr.Worktree, "merge-base", pr.BaseBranch, "HEAD").Output()
		if err != nil {
			return "", fmt.Errorf("git merge-base %s HEAD in %s: %w", pr.BaseBranch, pr.Worktree, err)
		}
		base = strings.TrimSpace(string(out))
	}

	index, cleanup, err := scratchIndex(ctx, pr.Worktree)
	if err != nil {
		return "", err
	}
	defer cleanup()

	git := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", pr.Worktree}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index, "GIT_OPTIONAL_LOCKS=0")
		return cmd.Output()
	}
	if _, err := git(append([]string{"add", "--intent-to-add", "--all"}, reviewPathspec...)...); err != nil {
		return "", fmt.Errorf("marking untracked files in %s: %w", pr.Worktree, err)
	}
	out, err := git(append([]string{"diff", base}, reviewPathspec...)...)
	if err != nil {
		return "", fmt.Errorf("git diff %s in %s: %w", base, pr.Worktree, err)
	}
	return string(out), nil
}

// scratchIndex copies the worktree's index to a temporary file and returns
// its path and a function that removes it. A worktree with no index yet gets
// an empty one, which git creates on first use.
func scratchIndex(ctx context.Context, worktree string) (string, func(), error) {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-path", "index").Output()
	if err != nil {
		return "", nil, fmt.Errorf("locating the index of %s: %w", worktree, err)
	}
	dir, err := os.MkdirTemp("", "sgt-review-index-")
	if err != nil {
		return "", nil, fmt.Errorf("creating a scratch index: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	scratch := filepath.Join(dir, "index")
	if data, err := os.ReadFile(strings.TrimSpace(string(out))); err == nil {
		if err := os.WriteFile(scratch, data, 0o600); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("copying the index of %s: %w", worktree, err)
		}
	} else if !os.IsNotExist(err) {
		cleanup()
		return "", nil, fmt.Errorf("reading the index of %s: %w", worktree, err)
	}
	return scratch, cleanup, nil
}

// PromptSuffix is the text RunAgentPhase appends to every prompt it runs in
// worktree: the plan instructions when a plan file was seeded into
// worktree's .sgt/, else "". A caller that budgets a prompt's size (the
// review diff cap) counts it, so the budget covers the prompt the agent
// actually receives.
func PromptSuffix(worktree string) string {
	if _, err := os.Stat(filepath.Join(worktree, ".sgt", "plan.json")); err == nil {
		return planPromptExtension
	}
	return ""
}

// rolePromptPath is where a gauntlet-role phase's prompt for one attempt is
// written: <run dir>/.prompts/<repo>/prompt_<phase>_<attempt>.txt, beside the
// run's worktrees rather than inside one, so CommitRunOutput never commits a
// critic's bar or a builder's fix brief onto the run branch. The run
// directory is the worktree's parent (FleetDir is <fleet root>/<run>/<repo>);
// runner cannot import dag to ask it.
func (pr *PhaseRunner) rolePromptPath(phaseName string, attempt int) string {
	return filepath.Join(filepath.Dir(pr.Worktree), ".prompts", pr.RepoName,
		fmt.Sprintf("prompt_%s_%d.txt", phaseName, attempt))
}
