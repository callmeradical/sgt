package ui

// Tests for Task 3 of openspec/changes/upgrade-migration-pre-rebrand-v2-state:
// GET /api/migration-status exposes upgrademigrate.LastResult() without
// ever triggering a migration attempt itself.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
	"github.com/callmeradical/sgt/internal/upgrademigrate"
)

// migrationStatusTestServer builds a real *Server (as every other handler
// test in this package does) and points SGT_CONFIG at a fresh temp dir, so
// upgrademigrate.LastResult() (which the handler under test calls) resolves
// its sentinel path into this test's own fixture rather than a real
// operator's ~/.config/sgt.
func migrationStatusTestServer(t *testing.T) (mux http.Handler, sentinelPath string) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("SGT_CONFIG", cfgDir)
	// sentinelFileName (internal/upgrademigrate/paths.go) is unexported;
	// its value is fixed by design.md/the PRD's Decision 6 and documented
	// on upgrademigrate.Sentinel, so it is safe to hard-code here.
	sentinelPath = filepath.Join(cfgDir, ".migration-from-sergeant.json")

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return NewServer(st, 0).Handler(), sentinelPath
}

func getMigrationStatus(t *testing.T, mux http.Handler) (status int, body []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/migration-status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// TestMigrationStatusEndpointNullWhenNoSentinel covers "returns nil/absent
// when no sentinel exists".
func TestMigrationStatusEndpointNullWhenNoSentinel(t *testing.T) {
	mux, _ := migrationStatusTestServer(t)

	status, body := getMigrationStatus(t, mux)
	if status != http.StatusOK {
		t.Fatalf("GET /api/migration-status status = %d, want 200; body=%s", status, body)
	}
	trimmed := string(bytes.TrimSpace(body))
	if trimmed != "null" {
		t.Errorf("GET /api/migration-status body = %q, want the JSON literal null", trimmed)
	}
}

// TestMigrationStatusEndpointReturnsRealSentinel covers "returns the
// sentinel's actual Status/Conflicts/Mismatches when one does" — proven
// against a real upgrademigrate.Sentinel value written to the fixture path
// this server is pointed at, not a hand-built fake response.
func TestMigrationStatusEndpointReturnsRealSentinel(t *testing.T) {
	mux, sentinelPath := migrationStatusTestServer(t)

	want := &upgrademigrate.Sentinel{
		Status:      upgrademigrate.StatusFailed,
		SourcePaths: []string{"/old/config", "/old/db", "/old/fleet"},
		Timestamp:   time.Now().UTC().Truncate(time.Second),
		Conflicts:   []string{"config:foo.yaml"},
		Mismatches:  []string{"store: run count mismatch: source=2 destination=1"},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinelPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	status, body := getMigrationStatus(t, mux)
	if status != http.StatusOK {
		t.Fatalf("GET /api/migration-status status = %d, want 200; body=%s", status, body)
	}
	var got upgrademigrate.Sentinel
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding response body %s: %v", body, err)
	}
	if got.Status != want.Status {
		t.Errorf("Status = %q, want %q", got.Status, want.Status)
	}
	if len(got.Conflicts) != 1 || got.Conflicts[0] != "config:foo.yaml" {
		t.Errorf("Conflicts = %v, want [config:foo.yaml]", got.Conflicts)
	}
	if len(got.Mismatches) != 1 || got.Mismatches[0] != want.Mismatches[0] {
		t.Errorf("Mismatches = %v, want %v", got.Mismatches, want.Mismatches)
	}
}

// TestMigrationStatusEndpointDoesNotWriteSentinel covers "a dashboard load
// does not itself write or modify the sentinel file": the endpoint must
// call upgrademigrate.LastResult(), never Run(), so a GET can never trigger
// a migration attempt. Proven by asserting the sentinel file's mtime is
// unchanged after the request — for the "no sentinel yet" case, that a GET
// does not create one at all.
func TestMigrationStatusEndpointDoesNotWriteSentinel(t *testing.T) {
	mux, sentinelPath := migrationStatusTestServer(t)

	// No sentinel exists yet. A migration-triggering handler would create
	// one (or at least touch the config dir); LastResult must not.
	if _, err := getMigrationStatusAndReturnErr(mux); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinelPath); err == nil {
		t.Fatalf("GET /api/migration-status created a sentinel file that did not exist before the request")
	}

	// Now with a real sentinel present: its mtime must not move across a
	// GET, proving the handler only reads it.
	want := &upgrademigrate.Sentinel{Status: upgrademigrate.StatusVerified, Timestamp: time.Now().UTC()}
	data, _ := json.Marshal(want)
	if err := os.WriteFile(sentinelPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMtime := before.ModTime()

	if _, err := getMigrationStatusAndReturnErr(mux); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(beforeMtime) {
		t.Errorf("sentinel mtime changed from %v to %v after a GET /api/migration-status", beforeMtime, after.ModTime())
	}
}

func getMigrationStatusAndReturnErr(mux http.Handler) (int, error) {
	req := httptest.NewRequest(http.MethodGet, "/api/migration-status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, nil
}
