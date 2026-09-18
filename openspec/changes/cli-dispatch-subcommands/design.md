# Design — CLI dispatch subcommands

## Ownership

One repository, `sgt`. Extends `internal/sgtclient` (added by
`mcp-dispatch-and-create-pr-tools`). Touches `cmd/sgt/main.go` only —
no other package changes.

## `internal/sgtclient` additions

```go
// RunsResponseItem mirrors runPayload (internal/ui/bulletstate.go)
// exactly: the embedded store.RunRecord plus Resumable. Importing
// internal/store here is fine — store has no dependency on sgtclient
// or ui, so there is no cycle — and means every RunRecord field stays
// available with no hand-copied field list to drift from the real one.
type RunsResponseItem struct {
	store.RunRecord
	Resumable bool `json:"resumable"`
}

func Runs(addr, project string) ([]RunsResponseItem, error) {
	path := "/api/runs"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	var out []RunsResponseItem
	if err := doJSON(http.MethodGet, addr, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RunDetailsResponse mirrors handleRunDetails' response map exactly.
type RunDetailsResponse struct {
	RunID       string                  `json:"run_id"`
	Phases      []store.PhaseRecord     `json:"phases"`
	Envelopes   []store.EnvelopeRecord  `json:"envelopes"`
	ResumeSkips []string                `json:"resume_skips"`
}

func RunDetails(addr, runID string) (*RunDetailsResponse, error) {
	var out RunDetailsResponse
	path := "/api/run-details?id=" + url.QueryEscape(runID)
	if err := doJSON(http.MethodGet, addr, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

Confirm `handleRuns`'s exact query-param handling (`internal/ui/server.go`,
`handleRuns`) and `handleRunDetails`'s exact response map
(`internal/ui/server.go`, `handleRunDetails`) directly before writing
this — this sketch is believed accurate but design.md is not the
source of truth the way the handler's own code is.

## `cmd/sgt/main.go` additions

Four new cases in the `switch command` block (alongside `run`,
`status`, `ui`, `mcp`, `version`), each following this exact shape —
parse flags, resolve `SGT_UI_ADDR` (default `sgtclient.DefaultAddr`),
call the matching `sgtclient` function, marshal the result to stdout,
exit 1 with the error on stderr if it fails:

```go
case "dispatch":
	runDispatchCommand(os.Args[2:])
case "runs":
	runRunsCommand(os.Args[2:])
case "run-details":
	runRunDetailsCommand(os.Args[2:])
case "create-pr":
	runCreatePRCommand(os.Args[2:])
```

- `dispatch`: flags `--project`, `--brief`, `--repo` (repeatable —
  `flag.Var` with a slice-accumulating `Value`, so `--repo svc --repo
  api` builds `[]string{"svc","api"}`), `--agent`, `--type`,
  `--change-id`, `--request-id`. Calls `sgtclient.Dispatch`.
- `runs`: flag `--project` (optional). Calls `sgtclient.Runs`.
- `run-details`: positional or `--id` for the run id (match whichever
  convention `run`/`status` already use in this file — read them
  first). Calls `sgtclient.RunDetails`.
- `create-pr`: flags `--run-id`, `--project`, `--repo`, `--title`,
  `--body`. Calls `sgtclient.CreatePR` (already exists from the other
  change).

`printUsage()` gains four new lines, one per subcommand, matching the
existing five's exact style (`fmt.Println("  sgt <cmd>    <one-line
description>")`).

`resolveUIAddr()` (added by `mcp-dispatch-and-create-pr-tools` inside
`internal/mcp`) is duplicated here as a small unexported helper in
`cmd/sgt/main.go` reading the same `SGT_UI_ADDR` env var — `cmd/sgt`
cannot import `internal/mcp` (wrong direction / not the right
dependency), and the helper is three lines, not worth a shared package
of its own. Both copies must read the same env var name and use the
same default; note this explicitly in your PR description as an
accepted, intentional small duplication (the alternative — a shared
`internal/sgtaddr` package for one function — was considered and
rejected as more ceremony than the duplication it would remove).

## Test shape (Quality bar → concrete tests)

- `internal/sgtclient/sgtclient_test.go` (extended): `Runs`/`RunDetails`
  against a fake `httptest.Server`, same shape as the existing
  `Dispatch`/`CreatePR` tests.
- A new `cmd/sgt` test package (`cmd/sgt/dispatch_cli_test.go` or
  similar): builds the real `sgt` binary once (`go build`, cached via
  `sync.Once`/`TestMain`), starts a real `httptest.Server` wrapping
  the real `ui.NewServer(...).Handler()`, runs the compiled binary as a
  subprocess with `SGT_UI_ADDR` pointed at it, and asserts on real
  store state after each of the four subcommands — mirroring
  `internal/repopolicy`'s existing pattern of running a *real* script/
  binary rather than reimplementing its behavior in the test
  (`TestMiseInstallLinksWikiDigestAndBuildsSgt` is the closest existing
  precedent in this repo for "run the real artifact as a subprocess,
  not a stand-in").
- **This is also where the full three-way parity test
  (`mcp-dispatch-and-create-pr-tools`'s Task 3, left half-finished with
  a `TODO(cli-dispatch-subcommands)` marker) gets completed**: add the
  CLI-subprocess leg to `internal/mcp/dispatch_parity_test.go`'s
  existing parity tests, so one test drives the same dispatch/create-pr
  three ways (raw HTTP, CLI subprocess, MCP tool) and asserts identical
  resulting store state / identical refusal text across all three.
  Find every `// TODO(cli-dispatch-subcommands)` comment in that file
  (there should be one per parity test — happy-path dispatch, happy-path
  create-pr, and one per negative case) and resolve each one; do not
  leave any unresolved.
