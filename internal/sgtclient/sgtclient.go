// Package sgtclient is the one shared HTTP client for sgt ui's dispatch and
// create-PR endpoints.
//
// Decision 3 (docs/prd-mcp-dispatch-and-create-pr-tools.md) requires this
// package to be the single place `internal/mcp`'s tool cases (and later,
// `cli-dispatch-subcommands`'s CLI subcommands) build the request and decode
// the response for these two endpoints — no local http.NewRequest/json.Marshal
// at either call site. That is what actually enforces "must not drift into
// independent contracts," rather than leaving it as prose.
//
// Every exported function takes addr string (the resolved `sgt ui` base URL)
// plus a typed request struct, and returns a typed response struct plus
// error. addr is always passed in, never resolved inside this package —
// callers resolve SGT_UI_ADDR/its default themselves, so this package has no
// environment dependency of its own and is trivially testable against an
// httptest.Server URL.
package sgtclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAddr is sgt ui's own default (NewServer's port <= 0 fallback).
const DefaultAddr = "http://127.0.0.1:8484"

// httpClient is package-level so tests can swap it (e.g. shorter timeout);
// production code never overrides it.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// doJSON posts body (or nil for GET) to addr+path and decodes the response
// into out. A non-2xx response becomes an error whose text is exactly the
// response body (http.Error's own format: "<message>\n", text/plain) — this
// is what makes negative-path parity possible: the same refusal text
// handleDispatch/handleCreatePR already produce reaches the caller
// unchanged, not re-wrapped or re-worded.
func doJSON(method, addr, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(addr, "/")+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sgt ui is not reachable at %s: %w; start it with `sgt ui`", addr, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s", strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

// DispatchRequest mirrors handleDispatch's decoded request struct
// (internal/ui/dispatch.go) exactly.
type DispatchRequest struct {
	Project   string   `json:"project"`
	Brief     string   `json:"brief"`
	Repos     []string `json:"repos,omitempty"`
	Agent     string   `json:"agent,omitempty"`
	Type      string   `json:"type"`
	ChangeID  string   `json:"change_id,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

// DispatchResponse is the union of both of handleDispatch's success shapes:
// the "dispatched" shape (also reused verbatim for an idempotent-repeat via
// respondWithExistingRun) and the "proposed" shape (the no-repos path).
// Status distinguishes which one a given call returned; fields that don't
// apply to that shape are simply absent/zero.
type DispatchResponse struct {
	Status  string `json:"status"` // "dispatched" or "proposed"
	TaskID  string `json:"task_id,omitempty"`
	Project string `json:"project,omitempty"`

	ChangeID      string `json:"change_id,omitempty"`
	ChangeDir     string `json:"change_dir,omitempty"`
	ChangeRepo    string `json:"change_repo,omitempty"`
	ChangeCreated bool   `json:"change_created,omitempty"`

	IntentID string          `json:"intent_id,omitempty"`
	Repos    []string        `json:"repos,omitempty"`
	Bullets  json.RawMessage `json:"bullets,omitempty"` // opaque; callers needing bullet fields decode further
}

// Dispatch calls POST /api/dispatch against a running `sgt ui` at addr.
func Dispatch(addr string, req DispatchRequest) (*DispatchResponse, error) {
	var out DispatchResponse
	if err := doJSON(http.MethodPost, addr, "/api/dispatch", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePRRequest mirrors handleCreatePR's decoded request struct
// (internal/ui/server.go) exactly.
type CreatePRRequest struct {
	RunID   string `json:"run_id"`
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
}

// CreatePRResponse mirrors handleCreatePR's writeJSON call exactly.
type CreatePRResponse struct {
	Status string `json:"status"`
	RunID  string `json:"run_id"`
	PRURL  string `json:"pr_url"`
	Branch string `json:"branch"`
	Error  string `json:"error"`
}

// CreatePR calls POST /api/create-pr against a running `sgt ui` at addr.
func CreatePR(addr string, req CreatePRRequest) (*CreatePRResponse, error) {
	var out CreatePRResponse
	if err := doJSON(http.MethodPost, addr, "/api/create-pr", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
