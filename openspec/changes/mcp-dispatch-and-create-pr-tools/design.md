# Design — MCP dispatch and create-PR tools

## Ownership

One repository, `sgt`. New package `internal/sgtclient`. Touches
`internal/mcp/server.go` (two new tool registrations, two new
`executeTool` cases, one `Description` edit for `sgt_seal_pr`).

## `internal/sgtclient` — the shared HTTP client package

New package. Both this change and the later `cli-dispatch-subcommands`
change import it; nothing else does. One file per endpoint pair, or one
`sgtclient.go` — implementer's choice, but every exported function
follows the same shape: takes `addr string` (the resolved `sgt ui` base
URL) plus a typed request struct, returns a typed response struct plus
`error`. `addr` is always passed in, never resolved inside the package —
callers (the MCP tool handler here, CLI flag parsing later) resolve
`SGT_UI_ADDR`/its default themselves, so the package has no environment
dependency of its own and is trivially testable against an
`httptest.Server` URL.

```go
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
// response body (http.Error's own format: "<message>\n", text/plain) —
// this is what makes negative-path parity possible: the same refusal text
// handleDispatch/handleCreatePR/etc. already produce reaches the caller
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
```

### Request/response shapes — mirror the HTTP handlers exactly, field for field

```go
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

// DispatchResponse is the union of both of handleDispatch's success
// shapes: the "dispatched" shape (also reused verbatim for an
// idempotent-repeat via respondWithExistingRun) and the "proposed" shape
// (the no-repos path). Status distinguishes which one a given call
// returned; fields that don't apply to that shape are simply absent/zero.
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

func Dispatch(addr string, req DispatchRequest) (*DispatchResponse, error) {
	var out DispatchResponse
	if err := doJSON(http.MethodPost, addr, "/api/dispatch", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePRRequest mirrors handleCreatePR's decoded request struct exactly.
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

func CreatePR(addr string, req CreatePRRequest) (*CreatePRResponse, error) {
	var out CreatePRResponse
	if err := doJSON(http.MethodPost, addr, "/api/create-pr", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

`Runs`/`RunDetails` are NOT added by this change — `cli-dispatch-subcommands`
adds them later, in this same package, following the identical shape.

## `internal/mcp/server.go` — the two new tools

Two new entries in the tool-list slice (same literal-struct style every
existing entry already uses), and two new `case` branches in
`executeTool`, each doing exactly this:

```go
case "sgt_dispatch":
	var req sgtclient.DispatchRequest
	// decode args (map[string]interface{}) into req — repos is a
	// []interface{} in args and needs an explicit []string conversion;
	// every other field is a direct string/bool assertion, matching the
	// existing case blocks' own args[...].(type) style.
	resp, err := sgtclient.Dispatch(resolveUIAddr(), req)
	if err != nil {
		return "", err
	}
	return encode(resp)

case "sgt_create_pr":
	var req sgtclient.CreatePRRequest
	// same decode style
	resp, err := sgtclient.CreatePR(resolveUIAddr(), req)
	if err != nil {
		return "", err
	}
	return encode(resp)
```

`resolveUIAddr()` is a small new helper (`internal/mcp/server.go` or a
shared spot both this package and `cmd/sgt` can reach without an import
cycle — a tiny package-local function reading `SGT_UI_ADDR` with
`sgtclient.DefaultAddr` fallback is simplest; do not put addr-resolution
inside `sgtclient` itself, per the "addr is always passed in" rule
above). `encode` already exists (`internal/mcp/run_follow.go`) and is
reused, not reimplemented.

`sgt_seal_pr`'s tool entry gets a `Description` edit only:

> "Seal the verified worktree changes in your own current checkout and
> open a GitHub / Gitea Pull Request. For a coordinator-dispatched run's
> isolated worktree, use `sgt_create_pr` instead."

No other line in that tool's registration or its `executeTool` case
changes.

## Test shape (Quality bar → concrete tests)

- `internal/sgtclient/sgtclient_test.go`: `Dispatch`/`CreatePR` each
  against a real `httptest.Server` wrapping a fake handler that records
  the received request and returns a canned response — proves the
  request is built and the response decoded correctly, independent of
  `internal/ui`.
- `internal/mcp/server_test.go` (or a new `dispatch_tools_test.go` in
  that package): `sgt_dispatch`/`sgt_create_pr` against a real
  `httptest.Server` wrapping the REAL `ui.NewServer(st, 0).Handler()` —
  not a fake — asserting on actual store rows after the call, matching
  the Quality bar's "no mocking the HTTP layer" rule.
- The cross-surface parity test (Quality bar #1) is the most valuable
  single test in this change: it belongs in `internal/mcp` (or a new
  top-level integration test package if neither `internal/ui` nor
  `internal/mcp` is the natural home — importing `cmd/sgt`'s main isn't
  possible, so the CLI leg of the parity test runs the *compiled binary*
  as a subprocess via `exec.Command`, built once with `go build` in a
  `TestMain`/`sync.Once` to avoid rebuilding per test). This test cannot
  be written in full until `cli-dispatch-subcommands` lands its
  subcommands — write the MCP-vs-HTTP half of it now, in this change,
  and extend it with the CLI leg when that change lands. Note this
  explicitly in tasks.md so it isn't forgotten.
