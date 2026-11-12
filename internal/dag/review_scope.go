package dag

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/callmeradical/sgt/internal/runner"
)

// Review diff budgets (design decision 6).
//
// claude's diff is capped at claudeReviewDiffCap; a claude prompt over
// runner.MaxClaudeArgPrompt travels on stdin, so its size is no argv concern.
// Every other harness takes its prompt as one command-line argument, so the
// whole prompt stays under argPromptCap, below Linux's 128 KiB
// single-argument limit, and the diff gets whatever room the rest of the
// prompt leaves, up to argReviewDiffCap.
const (
	claudeReviewDiffCap = 256 << 10
	argPromptCap        = 120 << 10
	argReviewDiffCap    = 96 << 10

	// maxCutFilesNamed bounds the list of cut files, so a change touching
	// thousands of files cannot by itself push the prompt over its budget.
	maxCutFilesNamed = 100
)

// incompleteDiffNote prefixes the diff of a run with no recorded base branch,
// whose diff covers only the change since the worktree's HEAD.
const incompleteDiffNote = "Note: this run has no recorded base branch, so the diff below covers only " +
	"the uncommitted and untracked changes since the worktree's HEAD and may be incomplete: " +
	"commits the run already made are not in it. Read the worktree for the full change.\n\n"

// boundedReviewPrompt renders a review prompt with build, fitting the diff to
// agent's budget. A diff that does not fit is cut at a line boundary and
// followed by the marker `[diff truncated at <cap>; read the remaining files
// in the worktree]` and the names of the files that were cut. baseKnown false
// prefixes the incomplete-diff note. appended is the text the runner will add
// after the returned prompt (runner.PromptSuffix); an argument harness's
// budget covers it too.
func boundedReviewPrompt(diff, agent string, baseKnown bool, appended string, build func(diff string) string) string {
	note := ""
	if !baseKnown {
		note = incompleteDiffNote
	}

	if isClaude(agent) {
		return build(note + capDiff(diff, claudeReviewDiffCap))
	}

	// The diff's room is what the rest of the prompt leaves. The cut-file list
	// grows as the cap shrinks, so shrink by the overshoot until it fits.
	budget := argPromptCap - len(appended)
	limit := budget - len(build(note)) - 1
	if limit > argReviewDiffCap {
		limit = argReviewDiffCap
	}
	for {
		if limit < 0 {
			limit = 0
		}
		prompt := build(note + capDiff(diff, limit))
		if len(prompt) < budget || limit == 0 {
			return prompt
		}
		limit -= len(prompt) - budget + 1
	}
}

func isClaude(agent string) bool {
	if agent == "" {
		agent = runner.DefaultAgent
	}
	return filepath.Base(agent) == "claude"
}

// capDiff returns diff whole when it is at most limit bytes. Otherwise the
// diff and everything appended to it (marker and cut-file list) fit in limit
// bytes: the diff is cut at the last line boundary that leaves room, then
// marked, and the files whose content was cut, wholly or partly, are named.
func capDiff(diff string, limit int) string {
	if len(diff) <= limit {
		return diff
	}
	files := diffSections(diff)
	cutAt := limit
	for {
		if cutAt < 0 {
			cutAt = 0
		}
		kept := cutAtLine(diff, cutAt)
		suffix := truncationSuffix(limit, cutFiles(files, len(kept)))
		if len(kept)+len(suffix) <= limit || cutAt == 0 {
			return kept + suffix
		}
		cutAt -= len(kept) + len(suffix) - limit
	}
}

// cutAtLine returns the longest prefix of diff, at most n bytes, ending at a
// line boundary, or at a rune boundary when no line ends within n bytes.
func cutAtLine(diff string, n int) string {
	if n >= len(diff) {
		return diff
	}
	if i := strings.LastIndexByte(diff[:n], '\n'); i >= 0 {
		return diff[:i+1]
	}
	for n > 0 && !utf8.RuneStart(diff[n]) {
		n--
	}
	return diff[:n]
}

func truncationSuffix(limit int, cut []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[diff truncated at %s; read the remaining files in the worktree]\n", formatSize(limit))
	if len(cut) > 0 {
		named := cut
		if len(named) > maxCutFilesNamed {
			named = named[:maxCutFilesNamed]
		}
		b.WriteString("Files cut from the diff: " + strings.Join(named, ", "))
		if more := len(cut) - len(named); more > 0 {
			fmt.Fprintf(&b, ", and %d more", more)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatSize(n int) string {
	if n > 0 && n%1024 == 0 {
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// diffSection is one file's part of a unified diff: its path and where its
// "diff --git" header starts and its content ends.
type diffSection struct {
	path       string
	start, end int
}

func diffSections(diff string) []diffSection {
	var out []diffSection
	pos := 0
	for pos < len(diff) {
		lineEnd := strings.IndexByte(diff[pos:], '\n')
		next := len(diff)
		if lineEnd >= 0 {
			next = pos + lineEnd + 1
		}
		line := strings.TrimRight(diff[pos:next], "\n")
		if strings.HasPrefix(line, "diff --git ") {
			if len(out) > 0 {
				out[len(out)-1].end = pos
			}
			out = append(out, diffSection{path: sectionPath(line), start: pos, end: len(diff)})
		}
		pos = next
	}
	return out
}

// sectionPath reads the post-image path from a "diff --git a/X b/Y" header.
func sectionPath(header string) string {
	if i := strings.LastIndex(header, " b/"); i >= 0 {
		return strings.Trim(header[i+3:], `"`)
	}
	return strings.TrimPrefix(header, "diff --git ")
}

// cutFiles names the sections not wholly within the first kept bytes.
func cutFiles(sections []diffSection, kept int) []string {
	var out []string
	for _, s := range sections {
		if s.end > kept {
			out = append(out, s.path)
		}
	}
	return out
}
