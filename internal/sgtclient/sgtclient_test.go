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

// Runs("", "") must omit the project query param entirely, matching
// handleRuns' own convention (internal/ui/server.go): an empty project value
// falls through to ListRecentRuns, the "combine every project" behavior —
// there is no query string at all for the empty case, not "project=".
func TestRunsOmitsProjectQueryParamWhenEmpty(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer srv.Close()

	out, err := Runs(srv.URL, "")
	if err != nil {
		t.Fatalf("Runs returned an error: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("request path = %q, want /api/runs with no query string", gotPath)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, want empty", out)
	}
}

// Runs(addr, "myproject") must scope to that project via ?project=, and the
// response must decode every embedded store.RunRecord field plus Resumable.
func TestRunsIncludesProjectQueryParamWhenSet(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"id":        "run-1",
				"project":   "myproject",
				"task_id":   "run-1",
				"status":    "passed",
				"resumable": false,
				"intent_id": "intent-1",
			},
		})
	}))
	defer srv.Close()

	out, err := Runs(srv.URL, "myproject")
	if err != nil {
		t.Fatalf("Runs returned an error: %v", err)
	}
	if gotPath != "/api/runs?project=myproject" {
		t.Errorf("request path = %q, want /api/runs?project=myproject", gotPath)
	}
	if len(out) != 1 {
		t.Fatalf("out = %v, want exactly one run", out)
	}
	if out[0].ID != "run-1" || out[0].Project != "myproject" || out[0].Status != "passed" {
		t.Errorf("out[0] = %+v, want id/project/status decoded from the response", out[0])
	}
	if out[0].IntentID != "intent-1" {
		t.Errorf("out[0].IntentID = %q, want intent-1", out[0].IntentID)
	}
	if out[0].Resumable {
		t.Errorf("out[0].Resumable = true, want false")
	}
}

// A project value of "all" must be sent through to the server untouched —
// Runs does no local re-interpretation of handleRuns' own "" vs "all"
// convention; the server decides what "all" means.
func TestRunsPassesThroughTheLiteralAllValue(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer srv.Close()

	if _, err := Runs(srv.URL, "all"); err != nil {
		t.Fatalf("Runs returned an error: %v", err)
	}
	if gotPath != "/api/runs?project=all" {
		t.Errorf("request path = %q, want /api/runs?project=all", gotPath)
	}
}

// RunDetails must decode phases, envelopes, and resume_skips exactly as
// handleRunDetails returns them.
func TestRunDetailsDecodesPhasesEnvelopesAndResumeSkips(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"run_id": "run-1",
			"phases": []map[string]interface{}{
				{"id": "phase-1", "run_id": "run-1", "repo": "svc", "name": "build", "kind": "agent", "status": "passed"},
			},
			"envelopes": []map[string]interface{}{
				{"id": "env-1", "run_id": "run-1", "repo": "svc", "stage": "build", "summary": "did the thing"},
			},
			"resume_skips": []string{"build"},
		})
	}))
	defer srv.Close()

	out, err := RunDetails(srv.URL, "run-1")
	if err != nil {
		t.Fatalf("RunDetails returned an error: %v", err)
	}
	if gotPath != "/api/run-details?id=run-1" {
		t.Errorf("request path = %q, want /api/run-details?id=run-1", gotPath)
	}
	if out.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", out.RunID)
	}
	if len(out.Phases) != 1 || out.Phases[0].Name != "build" || out.Phases[0].Status != "passed" {
		t.Errorf("Phases = %+v, want one phase named build/passed", out.Phases)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Summary != "did the thing" {
		t.Errorf("Envelopes = %+v, want one envelope with the summary", out.Envelopes)
	}
	if len(out.ResumeSkips) != 1 || out.ResumeSkips[0] != "build" {
		t.Errorf("ResumeSkips = %v, want [build]", out.ResumeSkips)
	}
}

// With no server listening at all, Runs/RunDetails must produce the same
// actionable "not reachable... start it with sgt ui" error Dispatch/CreatePR
// already do — they share doJSON, so this is really confirming that sharing.
func TestRunsAgainstAnUnreachableAddrNamesTheAddrAndSaysStartSgtUI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	_, err := Runs(addr, "")
	if err == nil {
		t.Fatal("expected an error listing runs against an unreachable address, got nil")
	}
	if !strings.Contains(err.Error(), addr) || !strings.Contains(err.Error(), "sgt ui") {
		t.Errorf("error %q does not name the address and mention starting sgt ui", err.Error())
	}
}
