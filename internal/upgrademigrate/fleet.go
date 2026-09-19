package upgrademigrate

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fleetItem names one migrated worktree by its <task>/<repo> pair, matching
// internal/dag.FleetDir's own layout (FleetRoot()/<runID>/<repoName>).
type fleetItem struct {
	Task string
	Repo string
}

func (f fleetItem) String() string { return f.Task + "/" + f.Repo }

type fleetMigrationResult struct {
	Migrated  []fleetItem
	Conflicts []string // "fleet:<task>/<repo>"
}

// Fleet migration itself lives in migrate.go's migrateFleetRetryAware,
// which folds in "is this destination already ours from an earlier attempt"
// handling for the failed→retry cycle (Decision 6). This file holds the
// shared pieces: the <task>/<repo> item type, the recursive cp -a-style
// copy, and the independent git-state check used both there and by this
// package's overall verification pass.
//
// A linked worktree's .git file names an absolute path back to its origin
// repository's own .git/worktrees/<name> directory, so a plain recursive
// file copy (never a git operation) keeps it working unchanged: that origin
// repository does not move just because the worktree's checkout files are
// copied elsewhere. Only oldFleetRoot (pre-rebrand v2's fleet) is ever read
// by this package — v1's ~/.local/share/sergeant/fleet has no field in
// paths and cannot be reached from here by construction.

// copyTree recursively copies src to dst, preserving file modes and
// symlinks exactly (equivalent to cp -a). dst (and any of its missing
// parents) is created as needed; dst itself must not already exist as a
// populated directory — callers check that before calling this.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	}

	return copyRegularFile(src, dst, info.Mode().Perm())
}

func copyRegularFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// gitStatePorcelain returns "git status --porcelain" and "git rev-parse
// HEAD" output for dir, used to independently prove a copied worktree
// matches its source: the same check both the fleet-migration step and the
// overall verification pass make, always against a fresh subprocess
// invocation rather than any state carried over from the copy itself.
func gitStatePorcelain(dir string) (status string, head string, err error) {
	statusOut, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git status in %s: %w: %s", dir, err, statusOut)
	}
	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse HEAD in %s: %w: %s", dir, err, headOut)
	}
	return strings.TrimSpace(string(statusOut)), strings.TrimSpace(string(headOut)), nil
}
