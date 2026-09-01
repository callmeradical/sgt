package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitutilTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func gitutilNewGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitutilTestGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitutilTestGit(t, dir, "add", ".")
	gitutilTestGit(t, dir, "commit", "-q", "-m", "seed")
}

// defaultBase's guess chain (used only when a run predates recording its
// base branch) must prefer the local main/master branch over a stale
// origin/* remote-tracking ref, for the same reason
// internal/dag/engine.go's resolveDefaultBranch does: a remote-tracking ref
// only updates on an explicit fetch/push, while a commit to the operator's
// own local main is real, current work the instant it lands.
func TestDefaultBaseGuessPrefersLocalMainOverStaleOriginMain(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	gitutilNewGitRepo(t, remote)

	src := filepath.Join(t.TempDir(), "svc")
	gitutilTestGit(t, filepath.Dir(src), "clone", "-q", remote, src)

	gitutilTestGit(t, src, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(src, "local-fix.txt"), []byte("local fix\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitutilTestGit(t, src, "add", ".")
	gitutilTestGit(t, src, "commit", "-q", "-m", "local fix")

	got := defaultBase(src, "")
	if got != "main" {
		t.Errorf("defaultBase guess = %q, want %q (a local main exists; must not prefer stale origin/main)", got, "main")
	}
}
