package sgtclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Dispatch's request must round-trip every DispatchRequest field to
// /api/dispatch untouched, and its response must decode into DispatchResponse
// field for field — proven independently of internal/ui, against a fake
// handler that simply echoes what it received.
func TestDispatchRoundTripsEveryRequestField(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "dispatched",
			"task_id":        "task-1",
			"project":        "proj-1",
			"change_id":      "change-1",
			"change_dir":     "/tmp/change-1",
			"change_repo":    "svc",
			"change_created": true,
		})
	}))
	defer srv.Close()

	req := DispatchRequest{
		Project:   "proj-1",
		Brief:     "add stripe webhooks",
		Repos:     []string{"svc", "web"},
		Agent:     "claude",
		Type:      "feat",
		ChangeID:  "change-1",
		RequestID: "req-1",
	}
	resp, err := Dispatch(srv.URL, req)
	if err != nil {
		t.Fatalf("Dispatch returned an error: %v", err)
	}

	if gotPath != "/api/dispatch" {
		t.Errorf("request path = %q, want /api/dispatch", gotPath)
	}
	wantBody := map[string]interface{}{
		"project":    "proj-1",
		"brief":      "add stripe webhooks",
		"repos":      []interface{}{"svc", "web"},
		"agent":      "claude",
		"type":       "feat",
		"change_id":  "change-1",
		"request_id": "req-1",
	}
	for k, want := range wantBody {
		got := gotBody[k]
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("request field %q = %s, want %s", k, gotJSON, wantJSON)
		}
	}

	if resp.Status != "dispatched" {
		t.Errorf("Status = %q, want dispatched", resp.Status)
	}
	if resp.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want task-1", resp.TaskID)
	}
	if resp.Project != "proj-1" {
		t.Errorf("Project = %q, want proj-1", resp.Project)
	}
	if resp.ChangeID != "change-1" {
		t.Errorf("ChangeID = %q, want change-1", resp.ChangeID)
	}
	if resp.ChangeDir != "/tmp/change-1" {
		t.Errorf("ChangeDir = %q, want /tmp/change-1", resp.ChangeDir)
	}
	if resp.ChangeRepo != "svc" {
		t.Errorf("ChangeRepo = %q, want svc", resp.ChangeRepo)
	}
	if !resp.ChangeCreated {
		t.Errorf("ChangeCreated = false, want true")
	}
}

// The no-repos path answers with the "proposed" shape: Status is "proposed",
// IntentID/Repos are populated, and the dispatched-shape fields (TaskID,
// ChangeDir) are zero because the fake server never sent them.
func TestDispatchDecodesTheProposedShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "proposed",
			"intent_id": "intent-1",
			"repos":     []string{"svc", "web"},
			"bullets":   []map[string]interface{}{{"id": "b1"}, {"id": "b2"}},
		})
	}))
	defer srv.Close()

	resp, err := Dispatch(srv.URL, DispatchRequest{Project: "proj-1", Brief: "b", Type: "feat"})
	if err != nil {
		t.Fatalf("Dispatch returned an error: %v", err)
	}
	if resp.Status != "proposed" {
		t.Errorf("Status = %q, want proposed", resp.Status)
	}
	if resp.IntentID != "intent-1" {
		t.Errorf("IntentID = %q, want intent-1", resp.IntentID)
	}
	if len(resp.Repos) != 2 || resp.Repos[0] != "svc" || resp.Repos[1] != "web" {
		t.Errorf("Repos = %v, want [svc web]", resp.Repos)
	}
	if len(resp.Bullets) == 0 {
		t.Errorf("Bullets is empty, want the raw bullets JSON to be preserved")
	}
	if resp.TaskID != "" {
		t.Errorf("TaskID = %q, want empty for the proposed shape", resp.TaskID)
	}
	if resp.ChangeDir != "" {
		t.Errorf("ChangeDir = %q, want empty for the proposed shape", resp.ChangeDir)
	}
}

// A non-2xx response's plain-text body becomes the returned error's message,
// verbatim — this is what lets a caller see exactly the refusal text
// handleDispatch/handleCreatePR already produce, unwrapped and unmodified.
func TestDispatchNonSuccessResponseErrorIsTheBodyVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid or missing type \"\": must be one of chore, docs, feat, fix, refactor, test", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := Dispatch(srv.URL, DispatchRequest{Project: "proj-1", Brief: "b"})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response, got nil")
	}
	want := "invalid or missing type \"\": must be one of chore, docs, feat, fix, refactor, test"
	if err.Error() != want {
		t.Errorf("error = %q, want exactly %q", err.Error(), want)
	}
}

func TestCreatePRNonSuccessResponseErrorIsTheBodyVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bullet \"b1\" for repo \"svc\" is \"pending\", not green — refusing to seal", http.StatusConflict)
	}))
	defer srv.Close()

	_, err := CreatePR(srv.URL, CreatePRRequest{RunID: "run-1", Project: "proj-1", Repo: "svc"})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response, got nil")
	}
	want := "bullet \"b1\" for repo \"svc\" is \"pending\", not green — refusing to seal"
	if err.Error() != want {
		t.Errorf("error = %q, want exactly %q", err.Error(), want)
	}
}

// CreatePR's request/response fields must round-trip exactly like Dispatch's.
func TestCreatePRRoundTripsEveryRequestField(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "created",
			"run_id": "run-1",
			"pr_url": "https://github.com/example/repo/pull/1",
			"branch": "feat/change-1",
			"error":  "",
		})
	}))
	defer srv.Close()

	req := CreatePRRequest{RunID: "run-1", Project: "proj-1", Repo: "svc", Title: "t", Body: "b"}
	resp, err := CreatePR(srv.URL, req)
	if err != nil {
		t.Fatalf("CreatePR returned an error: %v", err)
	}
	if gotPath != "/api/create-pr" {
		t.Errorf("request path = %q, want /api/create-pr", gotPath)
	}
	wantBody := map[string]interface{}{
		"run_id":  "run-1",
		"project": "proj-1",
		"repo":    "svc",
		"title":   "t",
		"body":    "b",
	}
	for k, want := range wantBody {
		if got := gotBody[k]; got != want {
			t.Errorf("request field %q = %v, want %v", k, got, want)
		}
	}
	if resp.Status != "created" {
		t.Errorf("Status = %q, want created", resp.Status)
	}
	if resp.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", resp.RunID)
	}
	if resp.PRURL != "https://github.com/example/repo/pull/1" {
		t.Errorf("PRURL = %q, want the PR url", resp.PRURL)
	}
	if resp.Branch != "feat/change-1" {
		t.Errorf("Branch = %q, want feat/change-1", resp.Branch)
	}
}

// With no server listening at all, the returned error names the address and
// tells the caller to start `sgt ui` — the same actionable text
// cli-dispatch-subcommands is designed to read for the CLI.
func TestDispatchAgainstAnUnreachableAddrNamesTheAddrAndSaysStartSgtUI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing listens at addr any more

	_, err := Dispatch(addr, DispatchRequest{Project: "proj-1", Brief: "b"})
	if err == nil {
		t.Fatal("expected an error dispatching against an unreachable address, got nil")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("error %q does not name the address %q", err.Error(), addr)
	}
	if !strings.Contains(err.Error(), "sgt ui") {
		t.Errorf("error %q does not mention starting `sgt ui`", err.Error())
	}
}

func TestCreatePRAgainstAnUnreachableAddrNamesTheAddrAndSaysStartSgtUI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	_, err := CreatePR(addr, CreatePRRequest{RunID: "run-1", Project: "proj-1", Repo: "svc"})
	if err == nil {
		t.Fatal("expected an error creating a PR against an unreachable address, got nil")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("error %q does not name the address %q", err.Error(), addr)
	}
	if !strings.Contains(err.Error(), "sgt ui") {
		t.Errorf("error %q does not mention starting `sgt ui`", err.Error())
	}
}
