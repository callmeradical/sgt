package handoff

// Test for slice beta-verdict of change claude-is-the-default-harness-and-
// runs-gauntlet-roles (design decision 6, "Beta's output never reaches
// another builder"): a review envelope never becomes envelope_latest.json.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveEnvelopeLeavesLatestAloneForAReview(t *testing.T) {
	router := NewRouter(t.TempDir())
	if err := router.SaveEnvelope(&Envelope{TaskID: "t", Repo: "a", Stage: "build", Summary: "built"}); err != nil {
		t.Fatal(err)
	}
	if err := router.SaveEnvelope(&Envelope{TaskID: "t", Repo: "a", Stage: "review", Summary: "reviewed"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(router.BaseDir, "a", "envelope_review.json")); err != nil {
		t.Errorf("review envelope not saved under its own stage: %v", err)
	}
	latest, err := router.ReadLatestEnvelope("a")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Stage != "build" || latest.Summary != "built" {
		t.Errorf("envelope_latest.json = %+v, want the build envelope", latest)
	}
}
