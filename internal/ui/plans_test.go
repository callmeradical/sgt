package ui

import (
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
