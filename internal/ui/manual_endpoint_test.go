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
