package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
