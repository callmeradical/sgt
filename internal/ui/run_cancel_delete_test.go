package ui

// Characterization tests for server-remaining-groups-decomposition, Task 3:
// pin handleRunCancel's and handleRunDelete's response shapes and their
// effect on run status against the CURRENT code, before they move verbatim
// to run_lifecycle.go. handleRunResume's scenario is already covered by
// TestResumeRestartsAFailedRun and its neighbors in server_test.go and by
// reconcile_test.go, which this change re-runs unmodified after the move.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Scenario: "Cancelling a run has the same effect before and after." An
// active run's registered cancel func must actually run, and the run's
// stored status must become cancelled.
func TestCancellingAnActiveRunStopsItAndRecordsCancelled(t *testing.T) {
	srv, st := terminalRunFixture(t, "pending")
	mux := srv.Handler()

	stopped := false
	_, cancel := context.WithCancel(context.Background())
	srv.registerRun("sgt-run", func() {
		stopped = true
		cancel()
	})

	w := postJSON(t, mux, "/api/run-cancel", `{"id":"sgt-run"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Status     string `json:"status"`
		ID         string `json:"id"`
		WasRunning bool   `json:"was_running"`
		Note       string `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response %s: %v", w.Body.String(), err)
	}
	if resp.Status != "cancelled" || resp.ID != "sgt-run" {
		t.Errorf("response = %+v, want status=cancelled id=sgt-run", resp)
	}
	if !resp.WasRunning {
		t.Error("was_running = false for a run this process had registered as active")
	}
	if resp.Note == "" {
		t.Error("note is empty; the operator is told nothing about what cancel did")
	}

	if !stopped {
		t.Error("the registered cancel func was never invoked; cancel only relabelled the status column")
	}
	if got := runStatus(t, st, "sgt-run"); got != "cancelled" {
		t.Errorf("run status = %q, want cancelled", got)
	}
}

// A cancel request naming a run this process is not driving still records the
// status, and says so via was_running=false rather than claiming it stopped
// something.
func TestCancellingARunThisProcessIsNotDrivingStillRecordsCancelled(t *testing.T) {
	srv, st := terminalRunFixture(t, "pending")
	mux := srv.Handler()

	w := postJSON(t, mux, "/api/run-cancel", `{"id":"sgt-run"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		WasRunning bool `json:"was_running"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response %s: %v", w.Body.String(), err)
	}
	if resp.WasRunning {
		t.Error("was_running = true for a run this process never registered as active")
	}
	if got := runStatus(t, st, "sgt-run"); got != "cancelled" {
		t.Errorf("run status = %q, want cancelled", got)
	}
}

// Regression coverage for issue #20 ("reconcile phase and bullet state on
// resume and cancellation"): cancelling a run this process is not actively
// driving (no live goroutine to eventually notice ctx cancellation and run
// setTerminal itself) must still clear a stale "blocked" bullet — the HTTP
// handler cannot rely on a goroutine that does not exist to do that later.
func TestCancellingARunThisProcessIsNotDrivingStillClearsAStaleBlockedBullet(t *testing.T) {
	srv, st := terminalRunFixture(t, "blocked")
	mux := srv.Handler()

	bullets, err := st.ListBulletsForIntent("sgt-run-intent")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdvanceBulletStatus(bullets[0].ID, "blocked", "gates did not pass; no further automatic attempt available"); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, mux, "/api/run-cancel", `{"id":"sgt-run"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	got, err := st.GetBullet(bullets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" {
		t.Errorf("bullet status = %q after cancelling a not-actively-driven run, want pending", got.Status)
	}
	if got.BlockedReason != "" {
		t.Errorf("bullet blocked reason = %q, want empty", got.BlockedReason)
	}
}

// Scenario: "Deleting a run has the same effect before and after." The run
// record must be gone afterward, and the response must name the deleted id.
func TestDeletingARunRemovesItsRecord(t *testing.T) {
	srv, st := terminalRunFixture(t, "pending")
	mux := srv.Handler()

	w := postJSON(t, mux, "/api/run-delete", `{"id":"sgt-run"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response %s: %v", w.Body.String(), err)
	}
	if resp.Status != "deleted" || resp.ID != "sgt-run" {
		t.Errorf("response = %+v, want status=deleted id=sgt-run", resp)
	}

	if _, err := st.GetRun("sgt-run"); err == nil {
		t.Error("run sgt-run is still present after delete")
	}
}

// Deleting a run that does not exist must not error: Store.DeleteRun's own
// semantics govern here, and the handler must not add a not-found guard
// DeleteRun itself does not have.
func TestDeletingAnUnknownRunIsNotAnError(t *testing.T) {
	srv, _ := terminalRunFixture(t, "pending")
	mux := srv.Handler()

	w := postJSON(t, mux, "/api/run-delete", `{"id":"sgt-nope"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}
