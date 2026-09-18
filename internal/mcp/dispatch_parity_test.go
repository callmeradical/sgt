package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/callmeradical/sgt/internal/sgtclient"
	"github.com/callmeradical/sgt/internal/store"
)

// Cross-surface parity (docs/prd-mcp-dispatch-and-create-pr-tools.md, Quality
// bar #1): sgt_dispatch/sgt_create_pr must call the exact same code
// handleDispatch/handleCreatePR do, not a reimplementation. Every test in
// this file drives ONE real httptest.Server (started inside
// mcpDispatchFixture/mcpCreatePRFixture, wrapping the real
// ui.NewServer(...).Handler()) two ways — a raw HTTP POST issued directly
// against that server's address, and the sgt_dispatch/sgt_create_pr MCP tool
// (which itself reaches the same server via SGT_UI_ADDR) — and asserts the
// two produce identical store state, or identical error text for the same
// invalid input. Neither leg mocks the HTTP layer.
//
// This is the two-way half of the three-way parity test design.md describes
// (HTTP / CLI subprocess / MCP). The CLI leg cannot be written until
// cli-dispatch-subcommands lands its subcommands; see the TODO on each test
// below.

// postJSON issues a raw HTTP POST against a real server address — the same
// kind of request curl or cli-dispatch-subcommands' future CLI leg would
// make — and returns the decoded status code and body.
func postJSON(t *testing.T, addr, path string, body map[string]interface{}) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(addr+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, buf.Bytes()
}

// TODO(cli-dispatch-subcommands): add the CLI-subprocess leg of this parity
// test once that change lands.
//
// A raw HTTP POST /api/dispatch and a call to the sgt_dispatch MCP tool,
// against the SAME running server, for equivalent valid input, must produce
// equivalent store state: one run, one intent, and one bullet per target
// repo, in the same shape (status, repo, position) either way.
func TestDispatchViaHTTPAndViaSgtDispatchToolProduceIdenticalStoreState(t *testing.T) {
	s, st, repoPaths, addr := mcpDispatchFixture(t, "svc")
	const changeID = "add-stripe-webhooks"
	if err := os.MkdirAll(filepath.Join(repoPaths["svc"], "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	// Leg 1: raw HTTP POST.
	httpCode, httpBody := postJSON(t, addr, "/api/dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks",
		"repos": []string{"svc"}, "type": "feat", "change_id": changeID, "request_id": "http-leg",
	})
	if httpCode != http.StatusOK {
		t.Fatalf("HTTP leg status = %d, want 200; body=%s", httpCode, httpBody)
	}
	var httpResp sgtclient.DispatchResponse
	if err := json.Unmarshal(httpBody, &httpResp); err != nil {
		t.Fatalf("decoding HTTP leg response: %v; body=%s", err, httpBody)
	}

	// Leg 2: the MCP tool, against the identical server.
	mcpText, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks",
		"repos": []interface{}{"svc"}, "type": "feat", "change_id": changeID, "request_id": "mcp-leg",
	})
	if err != nil {
		t.Fatalf("sgt_dispatch returned an error: %v", err)
	}
	var mcpResp sgtclient.DispatchResponse
	if err := json.Unmarshal([]byte(mcpText), &mcpResp); err != nil {
		t.Fatalf("decoding MCP leg response: %v; text=%s", err, mcpText)
	}

	// Both legs must report the same shape, modulo the identifiers that are
	// necessarily distinct because these are two separate dispatches against
	// the same server (two different request_ids, by construction).
	if httpResp.Status != "dispatched" || mcpResp.Status != "dispatched" {
		t.Fatalf("Status = %q (HTTP) / %q (MCP), want dispatched/dispatched", httpResp.Status, mcpResp.Status)
	}
	if httpResp.Project != mcpResp.Project {
		t.Errorf("Project = %q (HTTP) vs %q (MCP), want equal", httpResp.Project, mcpResp.Project)
	}
	if httpResp.ChangeID != mcpResp.ChangeID {
		t.Errorf("ChangeID = %q (HTTP) vs %q (MCP), want equal", httpResp.ChangeID, mcpResp.ChangeID)
	}
	if httpResp.ChangeRepo != mcpResp.ChangeRepo {
		t.Errorf("ChangeRepo = %q (HTTP) vs %q (MCP), want equal", httpResp.ChangeRepo, mcpResp.ChangeRepo)
	}
	if httpResp.ChangeCreated != mcpResp.ChangeCreated {
		t.Errorf("ChangeCreated = %v (HTTP) vs %v (MCP), want equal", httpResp.ChangeCreated, mcpResp.ChangeCreated)
	}
	if httpResp.TaskID == "" || mcpResp.TaskID == "" {
		t.Fatal("both legs must report a non-empty task_id")
	}
	if httpResp.TaskID == mcpResp.TaskID {
		t.Fatal("the two legs dispatched with different request_ids and must have produced two distinct runs")
	}

	// Store state: each leg's run must resolve to its own intent with
	// exactly one bullet, of the same shape.
	for _, taskID := range []string{httpResp.TaskID, mcpResp.TaskID} {
		run, err := st.GetRun(taskID)
		if err != nil {
			t.Fatalf("reading run %s: %v", taskID, err)
		}
		bullets, err := st.ListBulletsForIntent(run.IntentID)
		if err != nil {
			t.Fatalf("listing bullets for intent %s: %v", run.IntentID, err)
		}
		if len(bullets) != 1 || bullets[0].Repo != "svc" || bullets[0].Position != 1 {
			t.Errorf("run %s bullets = %+v, want exactly one bullet for repo svc at position 1", taskID, bullets)
		}
	}

	runs, err := st.ListRecentRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("store holds %d runs after one dispatch per surface, want 2: %+v", len(runs), runs)
	}

	waitForTerminalRunMCP(t, st, httpResp.TaskID)
	waitForTerminalRunMCP(t, st, mcpResp.TaskID)
}

// TODO(cli-dispatch-subcommands): add the CLI-subprocess leg of this parity
// test once that change lands.
//
// A raw HTTP POST /api/create-pr and a call to the sgt_create_pr MCP tool,
// each against its own green bullet but the SAME running server, must
// produce identical resulting bullet state (sealed) and an equivalent
// response shape.
func TestCreatePRViaHTTPAndViaSgtCreatePRToolProduceIdenticalBulletState(t *testing.T) {
	s, st, runID, _, addr := mcpCreatePRFixture(t, "green")

	// A second, independent green bullet/run on the SAME server and repo, so
	// the HTTP leg has its own target and cannot collide with the MCP leg's
	// seal.
	const intentID2 = "intent-mcpcp-2"
	if err := st.CreateIntent(&store.IntentRecord{ID: intentID2, Project: "mcpcp", Statement: "s2", Status: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBullet(&store.BulletRecord{ID: "bullet-mcpcp-2", IntentID: intentID2, Repo: "svc", Position: 1, Status: "green"}); err != nil {
		t.Fatal(err)
	}
	const runID2 = "run-mcpcp-2"
	if err := st.CreateRun(&store.RunRecord{ID: runID2, Project: "mcpcp", TaskID: runID2, Status: "passed", IntentID: intentID2}); err != nil {
		t.Fatal(err)
	}

	// Leg 1: raw HTTP POST, sealing the fixture's original bullet (run-mcpcp-1).
	httpCode, httpBody := postJSON(t, addr, "/api/create-pr", map[string]interface{}{
		"run_id": runID, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
	})
	if httpCode != http.StatusOK {
		t.Fatalf("HTTP leg status = %d, want 200; body=%s", httpCode, httpBody)
	}
	var httpResp sgtclient.CreatePRResponse
	if err := json.Unmarshal(httpBody, &httpResp); err != nil {
		t.Fatalf("decoding HTTP leg response: %v; body=%s", err, httpBody)
	}

	// Leg 2: the MCP tool, sealing the second bullet (run-mcpcp-2), against
	// the identical server.
	mcpText, err := s.executeTool("sgt_create_pr", map[string]interface{}{
		"run_id": runID2, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
	})
	if err != nil {
		t.Fatalf("sgt_create_pr returned an error: %v", err)
	}
	var mcpResp sgtclient.CreatePRResponse
	if err := json.Unmarshal([]byte(mcpText), &mcpResp); err != nil {
		t.Fatalf("decoding MCP leg response: %v; text=%s", err, mcpText)
	}

	if httpResp.Status != "created" || mcpResp.Status != "created" {
		t.Fatalf("Status = %q (HTTP) / %q (MCP), want created/created", httpResp.Status, mcpResp.Status)
	}
	if httpResp.Branch == "" || mcpResp.Branch == "" {
		t.Fatal("both legs must report a non-empty branch")
	}
	if httpResp.PRURL == "" || mcpResp.PRURL == "" {
		t.Fatal("both legs must report a non-empty pr_url")
	}
	if httpResp.Error != mcpResp.Error {
		t.Errorf("Error = %q (HTTP) vs %q (MCP), want equal (both empty)", httpResp.Error, mcpResp.Error)
	}

	origBullets, err := st.ListBulletsForIntent("intent-mcpcp-1")
	if err != nil {
		t.Fatal(err)
	}
	secondBullets, err := st.ListBulletsForIntent(intentID2)
	if err != nil {
		t.Fatal(err)
	}
	if len(origBullets) != 1 || origBullets[0].Status != "sealed" {
		t.Errorf("HTTP-leg bullet = %+v, want sealed", origBullets)
	}
	if len(secondBullets) != 1 || secondBullets[0].Status != "sealed" {
		t.Errorf("MCP-leg bullet = %+v, want sealed", secondBullets)
	}
}

// TODO(cli-dispatch-subcommands): add the CLI-subprocess leg of this parity
// test once that change lands.
//
// An unrecognized work type must be refused with byte-for-byte identical
// error text whether the caller used a raw HTTP POST /api/dispatch or the
// sgt_dispatch MCP tool.
func TestUnrecognizedTypeRefusalTextIsIdenticalViaHTTPAndSgtDispatch(t *testing.T) {
	_, _, _, addr := mcpDispatchFixture(t, "svc")

	httpCode, httpBody := postJSON(t, addr, "/api/dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks", "type": "bogus",
	})
	if httpCode != http.StatusBadRequest {
		t.Fatalf("HTTP leg status = %d, want 400; body=%s", httpCode, httpBody)
	}
	httpErrText := string(bytes.TrimRight(httpBody, "\n"))

	// A fresh fixture for the MCP leg: the HTTP leg above already exercised
	// this server, and a rejected dispatch creates no run to collide with,
	// but a fresh fixture keeps the two legs from sharing any state at all.
	s, _, _, _ := mcpDispatchFixture(t, "svc")
	_, err := s.executeTool("sgt_dispatch", map[string]interface{}{
		"project": "mcpo", "brief": "add stripe webhooks", "type": "bogus",
	})
	if err == nil {
		t.Fatal("expected sgt_dispatch to refuse an unrecognized type, got nil error")
	}

	if err.Error() != httpErrText {
		t.Errorf("error text differs between surfaces:\n  HTTP: %q\n  MCP:  %q", httpErrText, err.Error())
	}
}

// TODO(cli-dispatch-subcommands): add the CLI-subprocess leg of this parity
// test once that change lands.
//
// A non-green bullet must be refused with byte-for-byte identical error text
// whether the caller used a raw HTTP POST /api/create-pr or the
// sgt_create_pr MCP tool. Both fixtures build the identical intent/bullet/run
// id, so SealBulletForRun's refusal (which names the bullet id and its
// status) is textually identical across the two independent servers.
func TestNonGreenBulletRefusalTextIsIdenticalViaHTTPAndSgtCreatePR(t *testing.T) {
	_, _, runID, _, addr := mcpCreatePRFixture(t, "pending")
	httpCode, httpBody := postJSON(t, addr, "/api/create-pr", map[string]interface{}{
		"run_id": runID, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
	})
	if httpCode != http.StatusConflict {
		t.Fatalf("HTTP leg status = %d, want 409; body=%s", httpCode, httpBody)
	}
	httpErrText := string(bytes.TrimRight(httpBody, "\n"))

	s, _, runID2, _, _ := mcpCreatePRFixture(t, "pending")
	_, err := s.executeTool("sgt_create_pr", map[string]interface{}{
		"run_id": runID2, "project": "mcpcp", "repo": "svc", "title": "t", "body": "b",
	})
	if err == nil {
		t.Fatal("expected sgt_create_pr to refuse a non-green bullet, got nil error")
	}

	if err.Error() != httpErrText {
		t.Errorf("error text differs between surfaces:\n  HTTP: %q\n  MCP:  %q", httpErrText, err.Error())
	}
}
