package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/store"
	"github.com/callmeradical/sgt/internal/wiki"
)

// Scenario: "Rendering never fails the run" — a simulated wiki-write
// failure (an unwritable wiki root) is logged and does not change the
// run's own recorded status: recordTerminalRun's status write happens
// before recordWikiEntry runs, so the store row must reflect the real
// terminal status regardless of what the wiki write does.
func TestAWikiWriteFailureDoesNotChangeTheRunsRecordedStatus(t *testing.T) {
	srv, st := terminalRunFixture(t, "pending", "pending")

	// A regular file where the wiki root's directory needs to exist makes
	// every write inside internal/wiki fail deterministically.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_WIKI_ROOT", blocker)

	srv.recordTerminalRun("sgt-run", "passed")

	if got := runStatus(t, st, "sgt-run"); got != "passed" {
		t.Fatalf("run status = %q after a wiki-write failure, want passed", got)
	}
	for i, got := range bulletStatuses(t, st, "sgt-run-intent") {
		if got != "green" {
			t.Errorf("bullet %d status = %q after a wiki-write failure, want green", i+1, got)
		}
	}
}

// Scenario: "A completed run gets its own concept page" for the two
// terminal statuses bulletStatusForRunOutcome does NOT advance bullets
// for (cancelled, interrupted) — the exact distinction recordTerminalRun's
// own doc comment calls out: a wiki entry is recorded "for every terminal
// status, not only the ones that advance bullets." Review 042's critic
// found this covered only by a throwaway, now-deleted test; this is the
// committed regression test for it.
func TestWikiEntryIsRecordedForNonAdvancingTerminalStatuses(t *testing.T) {
	for _, status := range []string{"cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			srv, st := terminalRunFixture(t, "pending")
			t.Setenv("SGT_WIKI_ROOT", t.TempDir())

			srv.recordTerminalRun("sgt-run", status)

			// bulletStatusForRunOutcome must genuinely not advance this
			// status's bullets - otherwise this test is not exercising the
			// branch it claims to.
			for i, got := range bulletStatuses(t, st, "sgt-run-intent") {
				if got != "pending" {
					t.Fatalf("bullet %d status = %q after a %q run, want pending (unadvanced) — this test no longer exercises the non-advancing branch", i+1, got, status)
				}
			}

			run, err := st.GetRun("sgt-run")
			if err != nil {
				t.Fatal(err)
			}
			date := run.UpdatedAt.UTC().Format("2006-01-02")
			pagePath := filepath.Join(wiki.ProjectRoot(run.Project), date, run.ID+".md")
			raw, err := os.ReadFile(pagePath)
			if err != nil {
				t.Fatalf("wiki concept page was not written for a %q run: %v", status, err)
			}
			if !strings.Contains(string(raw), status) {
				t.Errorf("concept page for a %q run does not mention its own status:\n%s", status, raw)
			}
		})
	}
}

// Scenario: "A blocked run's page names the real reason" — the exact
// string blockedReasonForRun produced for this run, not a generic or
// placeholder message, and asserted by equality.
func TestBlockedRunsWikiPageNamesTheExactReasonBlockedReasonForRunProduced(t *testing.T) {
	srv, st := terminalRunFixture(t, "pending")
	t.Setenv("SGT_WIKI_ROOT", t.TempDir())

	if err := st.RecordEnvelope(&store.EnvelopeRecord{
		ID:            "env-1",
		RunID:         "sgt-run",
		Repo:          "api",
		Stage:         "build",
		Type:          "phase.completed",
		SchemaVersion: "1",
		Producer:      "sgt/runner",
		CorrelationID: "sgt-run",
		Data:          []byte(`{"blocked_reason":"requirement is ambiguous; needs a human decision"}`),
	}); err != nil {
		t.Fatal(err)
	}

	srv.recordTerminalRun("sgt-run", "failed")

	wantReason := srv.blockedReasonForRun("sgt-run", "blocked")

	run, err := st.GetRun("sgt-run")
	if err != nil {
		t.Fatal(err)
	}
	date := run.UpdatedAt.UTC().Format("2006-01-02")
	pagePath := filepath.Join(wiki.ProjectRoot(run.Project), date, run.ID+".md")
	raw, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("reading concept page: %v", err)
	}
	body := string(raw)
	idx := strings.Index(body, "## Blocked")
	if idx == -1 {
		t.Fatalf("concept page has no Blocked section: %s", body)
	}
	section := strings.TrimSpace(body[idx+len("## Blocked"):])
	if section != wantReason {
		t.Fatalf("wiki page blocked reason = %q, want exactly %q (blockedReasonForRun's own output)", section, wantReason)
	}
}
