package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/callmeradical/sgt/internal/manual"
)

type manualResponse struct {
	Sections []manual.Section `json:"sections"`
}

// Scenario: GET /api/manual returns the same sections sgt help answers from.
func TestHandleManualReturnsSameSectionsAsManualPackage(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/manual", nil)
	w := httptest.NewRecorder()
	srv.handleManual(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got manualResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v\nbody: %s", err, w.Body.String())
	}

	want := manual.Sections()
	if len(got.Sections) != len(want) {
		t.Fatalf("got %d sections, want %d", len(got.Sections), len(want))
	}
	for i := range want {
		if got.Sections[i] != want[i] {
			t.Errorf("section %d = %+v, want %+v", i, got.Sections[i], want[i])
		}
	}
}

// The frontend's manualHTML (internal/ui/static/index.html) reads
// lowercase s.title/s.body — JS object property access is exact-case, unlike
// Go's json.Unmarshal into a tagged/untagged struct, which matches keys
// case-insensitively and would silently accept "Title"/"Body" on the wire
// without ever exposing the mismatch. Decoding into a plain map (not
// manual.Section) is what actually catches a capitalization drift here.
func TestHandleManualJSONKeysAreLowercaseForTheFrontend(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/manual", nil)
	w := httptest.NewRecorder()
	srv.handleManual(w, req)

	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v\nbody: %s", err, w.Body.String())
	}

	sections, ok := got["sections"].([]interface{})
	if !ok || len(sections) == 0 {
		t.Fatalf("response has no usable \"sections\" array: %s", w.Body.String())
	}
	first, ok := sections[0].(map[string]interface{})
	if !ok {
		t.Fatalf("section 0 is not an object: %#v", sections[0])
	}
	if _, ok := first["title"]; !ok {
		t.Errorf("section 0 has no lowercase \"title\" key (got keys %v) — the frontend reads s.title and would render undefined", keysOf(first))
	}
	if _, ok := first["body"]; !ok {
		t.Errorf("section 0 has no lowercase \"body\" key (got keys %v) — the frontend reads s.body and would render undefined", keysOf(first))
	}
}

// Scenario: GET /api/manual is a pure read — it performs no write. Server{}
// is constructed with a nil Store, unlike every other handler in this
// package: if handleManual touched srv.Store at all (read or write), this
// would panic on the nil pointer instead of succeeding.
func TestHandleManualPerformsNoWrite(t *testing.T) {
	srv := &Server{Store: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/manual", nil)
	w := httptest.NewRecorder()
	srv.handleManual(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}
