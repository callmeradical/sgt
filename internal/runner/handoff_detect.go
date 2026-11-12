package runner

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Rule 4 of design decision 1a: a gauntlet:alpha attempt that handed off is
// never recorded passed (design decision 13). In this change it is failed
// and not retried; a successor arrives with change
// held-reviews-self-correct-and-builder-handoffs-continue.

// handoffContract is the last line of every gauntlet:alpha prompt, so a
// builder that stops at its context threshold says where its handoff is.
const handoffContract = "If you hand off, end your output with exactly one line: HANDOFF: <absolute path>"

// handoffLinePattern matches the line handoffContract asks for.
var handoffLinePattern = regexp.MustCompile(`^HANDOFF:\s*(/\S+)$`)

// handoffMtimeSlack widens the start of an attempt's window when comparing a
// handoff file's modification time against it: filesystems that stamp times
// from a coarse clock can date a file written just after the attempt started
// slightly before it.
const handoffMtimeSlack = time.Second

func init() {
	roleJudges[roleAlpha] = judgeHandoff
}

// withHandoffContract ends prompt with handoffContract when the phase runs as
// gauntlet:alpha, and returns it unchanged otherwise.
func withHandoffContract(prompt, role string) string {
	if role != roleAlpha {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + handoffContract
}

// judgeHandoff is rule 4: an alpha attempt that exited cleanly has handed off
// when its output reports a handoff or a handoff file written during it names
// its worktree.
func judgeHandoff(a roleAttempt) roleJudgement {
	path := reportedHandoff(a.Output)
	if path == "" {
		path = unreportedHandoff(handoffsDir(), a.Worktree, a.Started.Add(-handoffMtimeSlack), a.Ended)
	}
	if path == "" {
		return roleJudgement{}
	}
	return roleJudgement{
		Failed: true,
		Reason: "gauntlet:alpha handed off at " + path +
			"; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue",
		Merge: map[string]interface{}{"handoff_path": path},
	}
}

// reportedHandoff returns the path named by a HANDOFF: line among output's
// last three non-empty lines, the latest such line winning, or "". The file
// it names need not exist: a reported handoff is never a pass either way.
func reportedHandoff(output string) string {
	for _, line := range lastNonEmptyLines(output, 3) {
		if m := handoffLinePattern.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// handoffsDir is the gauntlet handoff skill's directory, resolved from HOME at
// call time, or "" when there is no home directory.
func handoffsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "gauntlet", "handoffs")
}

// unreportedHandoff returns the most recently modified file under dir that was
// modified within [from, to] and names worktree as a whole path, or "".
func unreportedHandoff(dir, worktree string, from, to time.Time) string {
	if dir == "" || worktree == "" {
		return ""
	}
	names := worktreeNames(worktree)
	var best string
	var bestMod time.Time
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mod := info.ModTime()
		if mod.Before(from) || mod.After(to) || (best != "" && !mod.After(bestMod)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, n := range names {
			if namesWholePath(string(data), n) {
				best, bestMod = p, mod
				break
			}
		}
		return nil
	})
	return best
}

// worktreeNames is worktree's absolute path, plus its symlink-resolved form
// when that differs: a session reports its working directory resolved.
func worktreeNames(worktree string) []string {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		abs = worktree
	}
	names := []string{abs}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real != abs {
		names = append(names, real)
	}
	return names
}

// namesWholePath reports whether text contains path not followed by a
// path-name character, nor by a "." and then one — so /f/r1/svc matches
// `/f/r1/svc`, "/f/r1/svc." and /f/r1/svc/sub, but not /f/r1/svc-api or
// /f/r1/svc.bak.
func namesWholePath(text, path string) bool {
	for i := 0; ; {
		at := strings.Index(text[i:], path)
		if at < 0 {
			return false
		}
		end := i + at + len(path)
		next := text[end:]
		switch {
		case next == "":
			return true
		case isPathNameByte(next[0]):
		case next[0] == '.' && len(next) > 1 && isPathNameByte(next[1]):
		default:
			return true
		}
		i = i + at + 1
	}
}

func isPathNameByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}
