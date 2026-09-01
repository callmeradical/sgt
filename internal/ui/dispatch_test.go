package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDispatchBaseValidation(t *testing.T) {
	mux, _, _ := dispatchFixture(t)

	t.Run("Rejects GET requests", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/dispatch", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})

	t.Run("Rejects invalid JSON body", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader("{invalid-json"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Rejects missing project", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader(`{"brief": "some brief"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Rejects empty brief", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader(`{"project": "o3", "brief": "   "}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Rejects invalid work type", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader(`{"project": "o3", "brief": "some brief", "type": "invalid-type"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Rejects non-existent project", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader(`{"project": "nonexistent", "brief": "some brief", "type": "feat"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Rejects invalid agent", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/dispatch", strings.NewReader(`{"project": "o3", "brief": "some brief", "type": "feat", "agent": "invalid-agent"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected %d, got %d", http.StatusBadRequest, w.Code)
		}
	})
}
