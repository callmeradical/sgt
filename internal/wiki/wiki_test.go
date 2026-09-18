package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/callmeradical/sgt/internal/store"
)

// frontmatter splits raw markdown into its YAML frontmatter block (if any)
// and body, parsing the frontmatter into m. It fails the test if the file
// has no frontmatter block at all, so callers that need to assert absence
// of frontmatter use hasFrontmatter instead.
func frontmatter(t *testing.T, raw []byte, m interface{}) {
	t.Helper()
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("no frontmatter block at start of file: %q", s)
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		t.Fatalf("no closing frontmatter delimiter: %q", s)
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), m); err != nil {
		t.Fatalf("parsing frontmatter: %v", err)
	}
}

func hasFrontmatter(raw []byte) bool {
	return strings.HasPrefix(string(raw), "---\n")
}

func sampleRun(id, project, status string, updatedAt time.Time) store.RunRecord {
	return store.RunRecord{
		ID:        id,
		Project:   project,
		Brief:     "add stripe webhooks",
		Type:      "feat",
		Status:    status,
		Slug:      id + "-slug",
		UpdatedAt: updatedAt,
	}
}

// Scenario: "A terminal run gets its own concept page" — a concept page
// exists under the project's wiki root, dated by the day it completed,
// carrying at minimum the type frontmatter field and the run's brief,
// bullets, and outcome.
func TestRecordRunProducesARealConceptPage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	run := sampleRun("sgt-run-1", "o3", "passed", updatedAt)
	bullets := []store.BulletRecord{
		{ID: "b1", Repo: "api", Status: "green", PRURL: "https://github.com/acme/api/pull/1"},
		{ID: "b2", Repo: "web", Status: "green"},
	}

	RecordRun(Entry{Run: run, Bullets: bullets})

	pagePath := filepath.Join(ProjectRoot("o3"), "2026-08-29", "sgt-run-1.md")
	raw, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("reading concept page: %v", err)
	}

	var fm struct {
		Type string `yaml:"type"`
	}
	frontmatter(t, raw, &fm)
	if fm.Type == "" {
		t.Errorf("concept page frontmatter has no type field")
	}

	body := string(raw)
	if !strings.Contains(body, "add stripe webhooks") {
		t.Errorf("concept page missing the run's brief: %s", body)
	}
	if !strings.Contains(body, "api") || !strings.Contains(body, "web") {
		t.Errorf("concept page missing bullet repos: %s", body)
	}
	if !strings.Contains(body, "https://github.com/acme/api/pull/1") {
		t.Errorf("concept page missing a bullet's PR link: %s", body)
	}
	if !strings.Contains(body, "passed") {
		t.Errorf("concept page missing the run's outcome: %s", body)
	}
}

// Scenario: "The wiki never writes to the operator's personal vault" — the
// wiki root ends up under SGT_WIKI_ROOT/ProjectRoot, never resembling
// ~/wiki's path.
func TestProjectRootIsUnderSGTWikiRootNeverThePersonalVault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", tmp)

	got := ProjectRoot("o3")
	want := filepath.Join(tmp, "o3")
	if got != want {
		t.Fatalf("ProjectRoot(%q) = %q, want %q", "o3", got, want)
	}
	if strings.Contains(got, string(filepath.Separator)+"wiki"+string(filepath.Separator)) && !strings.HasPrefix(got, tmp) {
		t.Fatalf("ProjectRoot resembles the operator's personal ~/wiki vault: %q", got)
	}
}

func TestProjectRootDefaultNeverResemblesThePersonalVault(t *testing.T) {
	t.Setenv("SGT_WIKI_ROOT", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got := ProjectRoot("o3")
	personalWiki := filepath.Join(home, "wiki")
	if got == personalWiki || strings.HasPrefix(got, personalWiki+string(filepath.Separator)) {
		t.Fatalf("default ProjectRoot %q resembles the operator's personal ~/wiki vault %q", got, personalWiki)
	}
	want := filepath.Join(home, ".local", "share", "sgt", "wiki", "o3")
	if got != want {
		t.Fatalf("ProjectRoot() = %q, want %q", got, want)
	}
}

// Scenario: "Rendering never fails the run" — a write failure is logged
// (not asserted here directly; recorded via RecordRun's no-error contract)
// and RecordRun itself never panics even when its destination is
// unwritable.
func TestRecordRunNeverPanicsOnAnUnwritableRoot(t *testing.T) {
	// A regular file where a directory needs to exist makes MkdirAll fail
	// deterministically and portably, without relying on chmod semantics.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_WIKI_ROOT", blocker)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	run := sampleRun("sgt-run-2", "o3", "passed", updatedAt)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecordRun panicked on an unwritable root: %v", r)
		}
	}()
	RecordRun(Entry{Run: run})
}

// Scenario: "The wiki root index carries okf_version" — asserted by
// parsing the written file's frontmatter directly.
func TestRootIndexCarriesOKFVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	RecordRun(Entry{Run: sampleRun("sgt-run-3", "o3", "passed", updatedAt)})

	raw, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "index.md"))
	if err != nil {
		t.Fatalf("reading root index.md: %v", err)
	}
	var fm struct {
		OKFVersion string `yaml:"okf_version"`
	}
	frontmatter(t, raw, &fm)
	if fm.OKFVersion != "0.2" {
		t.Fatalf("root index.md okf_version = %q, want 0.2", fm.OKFVersion)
	}
}

// Scenario: "A dated index carries no frontmatter" — asserted by parsing
// (attempting to parse) the written file directly, per OKF v0.2 §8: only a
// bundle-root index may carry frontmatter.
func TestDatedIndexCarriesNoFrontmatter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	RecordRun(Entry{Run: sampleRun("sgt-run-4", "o3", "passed", updatedAt)})

	raw, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "2026-08-29", "index.md"))
	if err != nil {
		t.Fatalf("reading dated index.md: %v", err)
	}
	if hasFrontmatter(raw) {
		t.Fatalf("dated index.md carries frontmatter, want none: %s", raw)
	}
}

// Scenario: "A concept page always names its type."
func TestConceptPageAlwaysNamesItsType(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	RecordRun(Entry{Run: sampleRun("sgt-run-5", "o3", "passed", updatedAt)})

	raw, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "2026-08-29", "sgt-run-5.md"))
	if err != nil {
		t.Fatalf("reading concept page: %v", err)
	}
	var fm struct {
		Type string `yaml:"type"`
	}
	frontmatter(t, raw, &fm)
	if fm.Type != "run" {
		t.Fatalf("concept page type = %q, want non-empty (run)", fm.Type)
	}
}

// Scenario: "Links are bundle-relative" — every cross-link the wiki writes
// uses the bundle-relative absolute form (a leading /), not a relative
// path or a path outside the wiki.
func TestLinksAreBundleRelativeAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	RecordRun(Entry{Run: sampleRun("sgt-run-6", "o3", "passed", updatedAt)})

	projectRoot := ProjectRoot("o3")
	files := []string{
		filepath.Join(projectRoot, "index.md"),
		filepath.Join(projectRoot, "2026-08-29", "index.md"),
		filepath.Join(projectRoot, "2026-08-29", "log.md"),
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		if !strings.Contains(string(raw), "](/") {
			t.Errorf("%s has no bundle-relative absolute link: %s", f, raw)
		}
		if strings.Contains(string(raw), "](./") || strings.Contains(string(raw), "](../") {
			t.Errorf("%s uses a relative link instead of bundle-relative absolute: %s", f, raw)
		}
	}
}

// Scenario: "No model call is made to produce wiki content" — content is
// a pure, deterministic function of Entry: calling RecordRun twice with
// identical input (against two independent roots) produces byte-identical
// concept pages, proving nothing beyond the given fields (no hidden
// network response, no randomness) influences the render.
func TestRenderingIsDeterministicNotSynthesized(t *testing.T) {
	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	run := sampleRun("sgt-run-7", "o3", "passed", updatedAt)
	bullets := []store.BulletRecord{{ID: "b1", Repo: "api", Status: "green"}}

	rootA := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", rootA)
	RecordRun(Entry{Run: run, Bullets: bullets})
	pageA, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "2026-08-29", "sgt-run-7.md"))
	if err != nil {
		t.Fatal(err)
	}

	rootB := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", rootB)
	RecordRun(Entry{Run: run, Bullets: bullets})
	pageB, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "2026-08-29", "sgt-run-7.md"))
	if err != nil {
		t.Fatal(err)
	}

	if string(pageA) != string(pageB) {
		t.Fatalf("rendering the same Entry twice produced different content:\nA:\n%s\nB:\n%s", pageA, pageB)
	}
}

// Scenario: "A blocked run's page names the real reason" — the exact
// reason string, asserted by equality, not just non-emptiness.
func TestBlockedRunPageNamesTheExactReason(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	const reason = "gates did not pass; no further automatic attempt available"
	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	RecordRun(Entry{
		Run:           sampleRun("sgt-run-8", "o3", "failed", updatedAt),
		BlockedReason: reason,
	})

	raw, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "2026-08-29", "sgt-run-8.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	idx := strings.Index(body, "## Blocked")
	if idx == -1 {
		t.Fatalf("concept page has no Blocked section: %s", body)
	}
	section := strings.TrimSpace(body[idx+len("## Blocked"):])
	if section != reason {
		t.Fatalf("blocked section = %q, want exactly %q", section, reason)
	}
}

// Scenario: "Two runs completing near-simultaneously both appear" — real
// goroutines racing calls to RecordRun for two different runs of the same
// project must both end up represented in the shared date's
// index.md/log.md and the root index.md. Run with -race to prove there is
// no data race, not just that both entries happen to appear.
func TestConcurrentRecordRunNeverLosesAnUpdate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	runA := sampleRun("sgt-run-a", "o3", "passed", updatedAt)
	runB := sampleRun("sgt-run-b", "o3", "passed", updatedAt)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		RecordRun(Entry{Run: runA})
	}()
	go func() {
		defer wg.Done()
		RecordRun(Entry{Run: runB})
	}()
	wg.Wait()

	dateDir := filepath.Join(ProjectRoot("o3"), "2026-08-29")
	for _, name := range []string{"index.md", "log.md"} {
		raw, err := os.ReadFile(filepath.Join(dateDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		body := string(raw)
		if !strings.Contains(body, "sgt-run-a.md") {
			t.Errorf("%s is missing run A's entry: %s", name, body)
		}
		if !strings.Contains(body, "sgt-run-b.md") {
			t.Errorf("%s is missing run B's entry: %s", name, body)
		}
	}

	rootIndex, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rootIndex), "/2026-08-29/index.md") {
		t.Errorf("root index.md is missing the shared date folder: %s", rootIndex)
	}

	for _, id := range []string{"sgt-run-a", "sgt-run-b"} {
		if _, err := os.Stat(filepath.Join(dateDir, id+".md")); err != nil {
			t.Errorf("missing concept page for %s: %v", id, err)
		}
	}
}

// RecordRun is safe to call twice for the same run: the second call must
// not duplicate the run's index/log line.
func TestRecordRunTwiceDoesNotDuplicateIndexOrLogLines(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SGT_WIKI_ROOT", root)

	updatedAt := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	run := sampleRun("sgt-run-9", "o3", "passed", updatedAt)

	RecordRun(Entry{Run: run})
	RecordRun(Entry{Run: run})

	dateDir := filepath.Join(ProjectRoot("o3"), "2026-08-29")
	for _, name := range []string{"index.md", "log.md"} {
		raw, err := os.ReadFile(filepath.Join(dateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		count := strings.Count(string(raw), "sgt-run-9.md")
		if count != 1 {
			t.Errorf("%s mentions sgt-run-9.md %d times, want exactly 1", name, count)
		}
	}

	rootIndex, err := os.ReadFile(filepath.Join(ProjectRoot("o3"), "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(rootIndex), "/2026-08-29/index.md")
	if count != 1 {
		t.Errorf("root index.md mentions the date folder %d times, want exactly 1", count)
	}
}
