package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/store"
)

func TestHandlePlans(t *testing.T) {
	tempDir := t.TempDir()
	db, _ := store.Open(filepath.Join(tempDir, "test.db"))
	defer db.Close()
	srv := NewServer(db, 8080)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/plans", srv.handlePlans)

	t.Run("invalid method", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/plans", nil)
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})

	t.Run("empty plans list", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/plans", nil)
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}

		var plans []planEntry
		if err := json.Unmarshal(w.Body.Bytes(), &plans); err != nil {
			t.Fatal(err)
		}
		if len(plans) != 0 {
			t.Errorf("expected empty plans, got %d", len(plans))
		}
	})

	t.Run("lists proposed plans", func(t *testing.T) {
		intent := store.IntentRecord{
			ID:        "intent-1",
			Statement: "make things better",
			Status:    "proposed",
		}
		if err := db.CreateIntent(&intent); err != nil {
			t.Fatal(err)
		}

		b1 := store.BulletRecord{
			ID:       "b-1",
			IntentID: "intent-1",
			Repo:     "alpha",
			Status:   "proposed",
		}
		if err := db.CreateBullet(&b1); err != nil {
			t.Fatal(err)
		}

		// Insert an abandoned intent, which should be ignored
		intent2 := store.IntentRecord{
			ID:        "intent-2",
			Statement: "never mind",
			Status:    "abandoned",
		}
		if err := db.CreateIntent(&intent2); err != nil {
			t.Fatal(err)
		}

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/plans", nil)
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}

		var plans []planEntry
		if err := json.Unmarshal(w.Body.Bytes(), &plans); err != nil {
			t.Fatal(err)
		}

		if len(plans) != 1 {
			t.Fatalf("expected 1 plan, got %d", len(plans))
		}

		if plans[0].Intent.ID != "intent-1" {
			t.Errorf("expected intent-1, got %s", plans[0].Intent.ID)
		}

		if len(plans[0].Bullets) != 1 {
			t.Fatalf("expected 1 bullet, got %d", len(plans[0].Bullets))
		}

		if plans[0].Bullets[0].ID != "b-1" {
			t.Errorf("expected bullet b-1, got %s", plans[0].Bullets[0].ID)
		}
	})
}

func TestHandlePlansRequiresGet(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")

	w := doPlanAction(t, mux, http.MethodPost, "/api/plans")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handlePlans POST = %d, want 405", w.Code)
	}

	w = doPlanAction(t, mux, http.MethodPut, "/api/plans")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handlePlans PUT = %d, want 405", w.Code)
	}
}

func TestHandleApprovePlanRequiresPost(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")

	w := doPlanAction(t, mux, http.MethodGet, "/api/plans/fake-id/approve")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleApprovePlan GET = %d, want 405", w.Code)
	}
}

func TestHandleRejectPlanRequiresPost(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")

	w := doPlanAction(t, mux, http.MethodGet, "/api/plans/fake-id/reject")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleRejectPlan GET = %d, want 405", w.Code)
	}
}

func TestHandleValidateIntentRequiresPost(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")

	w := doPlanAction(t, mux, http.MethodGet, "/api/validate-intent")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleValidateIntent GET = %d, want 405", w.Code)
	}
}

func doValidateIntent(t *testing.T, mux http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/validate-intent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestHandleValidateIntent_InvalidBody(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")
	w := doValidateIntent(t, mux, `{invalid-json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid json, got %d", w.Code)
	}
}

func TestHandleValidateIntent_MissingBrief(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")
	w := doValidateIntent(t, mux, `{"project": "o3", "brief": "   "}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty brief, got %d", w.Code)
	}
}

func TestHandleValidateIntent_UnknownProject(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")
	w := doValidateIntent(t, mux, `{"project": "unknown-proj", "brief": "do something"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown project, got %d", w.Code)
	}
}

func TestHandleValidateIntent_ValidComprehensive(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")
	// o3 project from dispatchFixtureRepos has repos "alpha", "beta", "gamma", "delta".
	// By default they may not have explicit factory gates configured.

	// Create a comprehensive brief:
	// length >= 20, contains signal word "should", names repo "alpha"
	brief := `The alpha service should support adding new users securely.`
	reqBody := `{"project": "o3", "brief": "` + brief + `"}`

	w := doValidateIntent(t, mux, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var res IntentValidationResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	if !res.Valid {
		t.Errorf("expected intent to be valid, got false. Errors: %v", res.Errors)
	}
	if len(res.Errors) > 0 {
		t.Errorf("expected 0 errors, got %d", len(res.Errors))
	}
}

func TestHandleValidateIntent_Warnings(t *testing.T) {
	mux, _, _, _ := dispatchFixtureRepos(t, "alpha")

	// Brief < 20 chars, no signal words, no repo name named
	brief := `fix bug`
	reqBody := `{"project": "o3", "brief": "` + brief + `"}`

	w := doValidateIntent(t, mux, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var res IntentValidationResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	if res.Valid {
		t.Errorf("expected short vague intent to be invalid (score < 60), got valid=true")
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected warnings for short vague intent, got none")
	}
}
