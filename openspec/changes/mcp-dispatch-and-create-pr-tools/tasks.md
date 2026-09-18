# Tasks — MCP dispatch and create-PR tools

One repository, `sgt`. Task order matters: 2 depends on 1; 3 depends on
1 and 2 both landing.

## Task 1 — `internal/sgtclient` package

Repository: `sgt`. Depends on: nothing.

Read first: this change's `design.md` in full; `internal/ui/dispatch.go`'s
`handleDispatch`, `dispatchResponse`, `respondWithExistingRun`; `internal/ui/server.go`'s
`handleCreatePR`.

Build:
- New `internal/sgtclient/sgtclient.go` (or split across files):
  `DefaultAddr`, `doJSON`, `DispatchRequest`, `DispatchResponse`,
  `Dispatch`, `CreatePRRequest`, `CreatePRResponse`, `CreatePR` — exactly
  as design.md specifies. Field names/JSON tags must match the HTTP
  handlers' own structs verbatim; if you find a field this design.md
  missed, match the handler, not this doc, and note the discrepancy in
  your PR description.

Verification: `go build ./... && go vet ./internal/... && go test
./internal/... -count=1 -skip
'^(TestBuildProjectGraphAppliesExcludePatterns|TestBuildProjectGraphMergesEveryParticipatingRepo|TestIncludeGroupsExcludesNonMatchingRepos|TestBuildNeverLeavesOutputInAPartialState|TestPublishFailureRestoresPriorGraph|TestBuildNeverSpawnsSgtGraphify|TestQueryAgainstABuiltGraphReturnsAnAnswer|TestExplainAndAffectedAreDistinctFromQuery|TestMCPGraphQueryAgainstABuiltGraphReturnsAnswer|TestBuildGraphEndpointBuildsAndPublishes)$'`

Scenarios needing direct test coverage:
- `Dispatch` against a fake `httptest.Server` that echoes the request
  it received: asserts every `DispatchRequest` field round-trips.
- `Dispatch` against a fake server returning the "proposed" shape
  (no-repos path): `Status == "proposed"`, `IntentID`/`Repos` populated,
  dispatch-shape fields (`TaskID`, `ChangeDir`) zero.
- `Dispatch`/`CreatePR` against a fake server returning a non-2xx status
  with a plain-text body: the returned Go error's message is exactly
  that body text, unwrapped and unmodified.
- `Dispatch`/`CreatePR` with no server listening at the target address
  at all: the returned error names the address and says to start `sgt
  ui` (per design.md's `doJSON` error text).

## Task 2 — `sgt_dispatch` and `sgt_create_pr` MCP tools

Repository: `sgt`. Depends on: Task 1.

Read first: `internal/mcp/server.go` in full (tool list, `executeTool`,
the existing `sgt_seal_pr` and `sgt_run_status` cases as the two closest
patterns); `internal/mcp/run_follow.go`'s `encode`.

Build:
- Two new tool-list entries (`sgt_dispatch`, `sgt_create_pr`) with
  `inputSchema` matching `DispatchRequest`/`CreatePRRequest`'s fields.
- Two new `executeTool` cases, exactly as design.md sketches — decode
  `args` into the request struct, call `sgtclient.Dispatch`/`CreatePR`,
  `encode` the result or return the error as-is.
- A small `resolveUIAddr()` helper reading `SGT_UI_ADDR` with
  `sgtclient.DefaultAddr` fallback.
- Edit `sgt_seal_pr`'s `Description` field per design.md. No other
  change to that tool.

Verification: same command as Task 1.

Scenarios needing direct test coverage (against a real
`ui.NewServer(st, 0).Handler()` wrapped in `httptest.Server`, not a
fake — this is the Quality bar's "no mocking the HTTP layer" rule):
- `sgt_dispatch` with explicit `repos` creates the same run/intent/bullet
  rows a `POST /api/dispatch` call with identical fields would.
- `sgt_dispatch` with no `repos` records a proposed plan and starts no
  run — same store state as the HTTP path.
- `sgt_dispatch` called twice with the same `request_id` produces
  exactly one run row (query the store directly, not just check both
  responses were 200).
- `sgt_dispatch` with an unrecognized `type` is refused with the exact
  message `validateWorkType` produces over HTTP.
- `sgt_create_pr` against a green bullet seals it and calls the
  provider seam — reuse `installFakeGitHubProvider`'s pattern from
  `internal/ui`'s own tests if that fixture is reachable from
  `internal/mcp`'s test package, or replicate the minimal fake provider
  install there.
- `sgt_create_pr` against a non-green bullet is refused with
  `handleCreatePR`'s exact `SealBulletForRun` refusal text, and does
  not invoke the provider — mirror
  `TestCreatePRForNonGreenBulletIsRefusedAndNeverInvokesGH`'s assertion
  shape.
- `sgt mcp`'s tool list includes both new tools (a test reading the
  list, not just trusting the source).

## Task 3 — MCP-vs-HTTP parity test (half of the full cross-surface test)

Repository: `sgt`. Depends on: Task 2.

The full three-way parity test (HTTP / CLI subprocess / MCP) described
in this change's `design.md` "Test shape" section cannot be completed
until `cli-dispatch-subcommands` lands the CLI subcommands. Write the
two-way half available now:

- One test, one real `httptest.Server` wrapping the real handler: drive
  the identical dispatch via a raw HTTP POST and via the `sgt_dispatch`
  MCP tool, assert identical resulting store state (run, intent,
  bullets) for the same valid input.
- Same shape for `create-pr`: raw HTTP POST vs `sgt_create_pr`, same
  resulting bullet/envelope state.
- Same shape for one negative case each (non-green bullet for
  create-pr; unrecognized type for dispatch): identical error text from
  both surfaces.
- Leave a clearly marked `// TODO(cli-dispatch-subcommands): add the
  CLI-subprocess leg of this parity test once that change lands.`
  comment directly above the test function, naming the change, so it
  is discoverable rather than silently forgotten.

Verification: same command as Task 1, run specifically on this new test
file with `-v` to confirm it passes and genuinely exercises both
surfaces (read the test's own assertions, don't just trust `PASS`).
