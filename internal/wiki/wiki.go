// Package wiki renders a project's completed runs into a plain-markdown,
// browsable trail conformant with the Open Knowledge Format (OKF) v0.2
// (https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md):
// a root index.md, one dated folder per day carrying its own index.md and
// log.md, and one concept page per run. This is Sgt's own operational
// wiki, distinct from — and never written into — the operator's personal
// wiki vault.
//
// Content is a deterministic rendering of facts already durable in
// store.RunRecord/store.BulletRecord by the time RecordRun is called. No
// LLM or external API call is made.
package wiki

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/callmeradical/sgt/internal/store"
)

// ProjectRoot resolves a project's wiki root. SGT_WIKI_ROOT overrides the
// parent directory (mirrors dag.FleetRoot's SGT_FLEET_DIR and
// artifactsRoot's SGT_ARTIFACTS_ROOT), so tests never write into an
// operator's real home directory. The default,
// ~/.local/share/sgt/wiki/<project>/, sits alongside
// ~/.local/share/sgt/artifacts/ — a sibling durable-output root — and is
// never the operator's personal ~/wiki vault.
func ProjectRoot(project string) string {
	base := os.Getenv("SGT_WIKI_ROOT")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share", "sgt", "wiki")
	}
	return filepath.Join(base, project)
}

// Entry is the rendering input for one run: the already-durable facts
// recordTerminalRun has in hand by the time it calls RecordRun.
type Entry struct {
	Run     store.RunRecord
	Bullets []store.BulletRecord
	// BlockedReason is "" unless the run's bullets moved to blocked.
	BlockedReason string
}

// mu serializes every RecordRun call. recordTerminalRun can fire for two
// different runs of the same project at nearly the same time; a naive
// read-modify-write on a shared index.md/log.md could lose an update. File
// I/O for a handful of short markdown files is cheap enough that one
// global lock costs nothing measurable.
var mu sync.Mutex

// RecordRun renders entry into ProjectRoot(entry.Run.Project)'s wiki: the
// run's own dated concept page, that date's index.md/log.md, and the
// wiki's root index.md. Idempotent: safe to call more than once for the
// same run — overwrites the run's own page, never duplicates an
// index/log line already present for it.
//
// RecordRun never returns an error and never panics: an I/O failure is
// logged and RecordRun returns, the same posture
// internal/runner/artifacts.go's captureArtifacts already established for
// a structurally identical concern — a derived side effect of a terminal
// event that must never fail the run it describes.
func RecordRun(entry Entry) {
	mu.Lock()
	defer mu.Unlock()

	root := ProjectRoot(entry.Run.Project)
	date := entry.Run.UpdatedAt.UTC().Format("2006-01-02")
	dateDir := filepath.Join(root, date)

	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		log.Printf("sgt: wiki: creating dated dir %s for run %s: %v", dateDir, entry.Run.ID, err)
		return
	}
	if err := writeConceptPage(dateDir, date, entry); err != nil {
		log.Printf("sgt: wiki: writing concept page for run %s: %v", entry.Run.ID, err)
		return
	}
	if err := updateDateIndex(dateDir, date, entry); err != nil {
		log.Printf("sgt: wiki: updating dated index for run %s: %v", entry.Run.ID, err)
	}
	if err := updateDateLog(dateDir, date, entry); err != nil {
		log.Printf("sgt: wiki: updating dated log for run %s: %v", entry.Run.ID, err)
	}
	if err := updateRootIndex(root, date); err != nil {
		log.Printf("sgt: wiki: updating root index for run %s: %v", entry.Run.ID, err)
	}
}

// conceptFrontmatter is a run's concept-page frontmatter. Per OKF v0.2 §4.1
// only `type` is required; okf_version is not a recognized concept-page
// field (design.md's "rejected alternatives" — it belongs on the
// bundle-root index only, §12).
type conceptFrontmatter struct {
	Type        string        `yaml:"type"`
	Title       string        `yaml:"title"`
	Description string        `yaml:"description"`
	Tags        []string      `yaml:"tags,omitempty"`
	Generated   generatedInfo `yaml:"generated"`
}

// generatedInfo follows OKF v0.2 §5.2: `generated.at` supersedes v0.1's
// bare `timestamp` field, and `generated.by` uses the §7 actor convention
// (process:<id> for an automated process).
type generatedInfo struct {
	By string `yaml:"by"`
	At string `yaml:"at"`
}

// rootFrontmatter is the bundle-root index's frontmatter — the one place
// OKF v0.2 permits frontmatter on an index.md, and the one place
// okf_version may be declared (§8, §12).
type rootFrontmatter struct {
	OKFVersion string `yaml:"okf_version"`
}

func writeConceptPage(dateDir, date string, entry Entry) error {
	fm := conceptFrontmatter{
		Type:        "run",
		Title:       runTitle(entry.Run),
		Description: runDescription(entry.Run),
		Tags:        runTags(entry.Run),
		Generated: generatedInfo{
			By: "process:sgt",
			At: entry.Run.UpdatedAt.UTC().Format(time.RFC3339),
		},
	}
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return err
	}

	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", fm.Title)
	if entry.Run.Brief != "" {
		fmt.Fprintf(&body, "%s\n\n", entry.Run.Brief)
	}
	body.WriteString("## Bullets\n\n")
	body.WriteString("| Repo | Status | PR |\n|---|---|---|\n")
	for _, b := range entry.Bullets {
		pr := "—"
		if b.PRURL != "" {
			pr = fmt.Sprintf("[%s](%s)", b.PRURL, b.PRURL)
		}
		fmt.Fprintf(&body, "| %s | %s | %s |\n", b.Repo, b.Status, pr)
	}
	if entry.BlockedReason != "" {
		fmt.Fprintf(&body, "\n## Blocked\n\n%s\n", entry.BlockedReason)
	}

	content := "---\n" + string(fmBytes) + "---\n\n" + body.String()
	return os.WriteFile(conceptPagePath(dateDir, entry.Run), []byte(content), 0o644)
}

func conceptPagePath(dateDir string, run store.RunRecord) string {
	return filepath.Join(dateDir, run.ID+".md")
}

func conceptLink(date string, run store.RunRecord) string {
	return fmt.Sprintf("/%s/%s.md", date, run.ID)
}

// updateDateIndex appends one bullet for entry's run to that date's
// index.md — a reserved OKF filename (§3.1) carrying no frontmatter, per
// §8's rule that only a bundle-root index may include one.
func updateDateIndex(dateDir, date string, entry Entry) error {
	path := filepath.Join(dateDir, "index.md")
	link := conceptLink(date, entry.Run)
	marker := "(" + link + ")"
	line := fmt.Sprintf("* [%s](%s) - %s\n", runTitle(entry.Run), link, runDescription(entry.Run))
	header := fmt.Sprintf("# %s\n\n", date)
	return appendUniqueLine(path, header, marker, line)
}

// updateDateLog appends one prose bullet for entry's run under that date's
// own heading in log.md, following §9's date-headed, newest-first,
// bold-leading-word convention.
func updateDateLog(dateDir, date string, entry Entry) error {
	path := filepath.Join(dateDir, "log.md")
	link := conceptLink(date, entry.Run)
	marker := "(" + link + ")"
	line := fmt.Sprintf("* **%s**: [%s](%s) — %s\n", capitalize(entry.Run.Status), runTitle(entry.Run), link, runDescription(entry.Run))
	header := fmt.Sprintf("# Directory Update Log\n\n## %s\n\n", date)
	return appendUniqueLine(path, header, marker, line)
}

// appendUniqueLine appends line to the file at path, creating it with
// header first if it does not exist. It is idempotent: if marker already
// appears anywhere in the file, the file is left untouched — a second
// RecordRun for the same run never duplicates its index/log entry.
func appendUniqueLine(path, header, marker, line string) error {
	content := header
	data, err := os.ReadFile(path)
	if err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	if strings.Contains(content, marker) {
		return nil
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line
	return os.WriteFile(path, []byte(content), 0o644)
}

// updateRootIndex lists date under the wiki root's index.md if it is not
// already listed. It is the one file in the wiki that carries okf_version
// frontmatter (§12). New dates are inserted immediately after the heading
// rather than appended, so the list reads newest-first for the normal case
// of dates arriving in non-decreasing order — an implementation choice;
// OKF only mandates order for log.md, not index.md (§8, §9).
func updateRootIndex(root, date string) error {
	path := filepath.Join(root, "index.md")
	const heading = "# Dates\n"

	content, err := os.ReadFile(path)
	var body string
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		fmBytes, marshalErr := yaml.Marshal(rootFrontmatter{OKFVersion: "0.2"})
		if marshalErr != nil {
			return marshalErr
		}
		body = "---\n" + string(fmBytes) + "---\n\n" + heading + "\n"
	} else {
		body = string(content)
	}

	marker := fmt.Sprintf("(/%s/index.md)", date)
	if strings.Contains(body, marker) {
		return nil
	}

	line := fmt.Sprintf("* [%s](/%s/index.md)\n", date, date)
	idx := strings.Index(body, heading)
	if idx == -1 {
		body = strings.TrimRight(body, "\n") + "\n\n" + heading + "\n" + line
	} else {
		insertAt := idx + len(heading)
		for insertAt < len(body) && body[insertAt] == '\n' {
			insertAt++
		}
		body = body[:insertAt] + line + body[insertAt:]
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func runTitle(run store.RunRecord) string {
	if run.Slug != "" {
		return run.Slug
	}
	return firstLine(run.Brief)
}

func runDescription(run store.RunRecord) string {
	return fmt.Sprintf("%s — %s", run.Status, firstLine(run.Brief))
}

func runTags(run store.RunRecord) []string {
	var tags []string
	if run.Type != "" {
		tags = append(tags, run.Type)
	}
	if run.Status != "" {
		tags = append(tags, run.Status)
	}
	return tags
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 72 {
		s = strings.TrimSpace(s[:72])
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
