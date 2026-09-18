# Tasks — CLI dispatch subcommands

One repository, `sgt`. Task order matters: 2 depends on 1; 3 depends
on 1 and 2 both landing. Depends on `mcp-dispatch-and-create-pr-tools`
having already landed `internal/sgtclient`, `Dispatch`, and `CreatePR`
— confirm those exist on this branch before starting (they should,
since this branch is stacked on that one).

## Task 1 — Extend `internal/sgtclient` with `Runs`/`RunDetails`

Repository: `sgt`. Depends on: `mcp-dispatch-and-create-pr-tools`
(already landed on this branch).

Read first: this change's `design.md`; `internal/ui/server.go`'s
`handleRuns` and `handleRunDetails` directly (not design.md's sketch
alone — confirm field names/query-param handling against the real
code); `internal/sgtclient/sgtclient.go` (the existing `doJSON`/
`Dispatch`/`CreatePR` to match style).

Build: `Runs`, `RunsResponseItem`, `RunDetails`, `RunDetailsResponse`
in `internal/sgtclient`, per design.md.

Verification: `go build ./... && go vet ./internal/... && go test
./internal/... -count=1 -skip
'^(TestBuildProjectGraphAppliesExcludePatterns|TestBuildProjectGraphMergesEveryParticipatingRepo|TestIncludeGroupsExcludesNonMatchingRepos|TestBuildNeverLeavesOutputInAPartialState|TestPublishFailureRestoresPriorGraph|TestBuildNeverSpawnsSgtGraphify|TestQueryAgainstABuiltGraphReturnsAnAnswer|TestExplainAndAffectedAreDistinctFromQuery|TestMCPGraphQueryAgainstABuiltGraphReturnsAnswer|TestBuildGraphEndpointBuildsAndPublishes)$'`

Scenarios needing direct test coverage:
- `Runs("", "")` and `Runs(addr, "myproject")` against a fake server
  recording the request path: confirm the `project` query param is
  omitted vs present exactly matching `handleRuns`' own `""`/`"all"`
  scoping convention.
- `RunDetails` against a fake server: confirm `phases`/`envelopes`/
  `resume_skips` all decode.

## Task 2 — Four new `cmd/sgt` subcommands

Repository: `sgt`. Depends on: Task 1.

Read first: `cmd/sgt/main.go` in full (the existing `run`/`status`/
`ui`/`mcp`/`version` switch, `printUsage`, `showStatus` for the closest
existing flag-parsing/output precedent).

Build: `dispatch`, `runs`, `run-details`, `create-pr` subcommands per
design.md — flags, `SGT_UI_ADDR` resolution (a small local helper,
duplicated from `internal/mcp`'s `resolveUIAddr` per design.md's
explicit note, not shared), JSON-to-stdout on success, error-to-stderr
plus exit 1 on failure. `printUsage()` updated.

Verification: same command as Task 1.

Scenarios needing direct test coverage (build the real `sgt` binary
once, run it as a real subprocess against a real `httptest.Server`
wrapping the real `ui.NewServer(...).Handler()` — no mocking the
binary or the server, matching this repo's own
`TestMiseInstallLinksWikiDigestAndBuildsSgt` precedent for "run the
real artifact"):
- `sgt dispatch --project ... --brief ... --type ... --repo svc`
  against the fixture server creates the same run/intent/bullet rows
  the equivalent raw HTTP POST would — assert on the server's store
  directly, not just the CLI's stdout.
- `sgt dispatch` with no `--repo` at all records a proposed plan.
- `sgt dispatch --request-id <same key>` invoked twice produces exactly
  one run row — checked directly against the fixture's store
  (`ListRecentRuns`), not inferred from two successful exits. The
  inherited Quality bar (docs/prd-mcp-dispatch-and-create-pr-tools.md
  #3) requires this for the CLI surface too, not only MCP.
- `sgt runs --project X` and `sgt runs` (no flag) return what
  `handleRuns` would for the same scoping.
- `sgt run-details <id>` returns phases/envelopes for a run seeded
  directly into the fixture's store.
- `sgt create-pr` against a green bullet succeeds; against a non-green
  bullet fails with `handleCreatePR`'s exact refusal text.
- Any subcommand run with `SGT_UI_ADDR` pointed at an address nothing
  is listening on exits non-zero with the actionable
  "not reachable... start it with `sgt ui`" message on stderr.
- `sgt --help` (or `sgt` with no args) lists all four new subcommands.

## Task 3 — Complete the three-way parity test

Repository: `sgt`. Depends on: Task 1, Task 2.

Read first: `internal/mcp/dispatch_parity_test.go` in full — every
`// TODO(cli-dispatch-subcommands)` comment marks exactly what this
task must add.

Build: nothing new in production code. For each parity test in that
file, add the CLI-subprocess leg: run the compiled `sgt` binary as a
subprocess (same binary Task 2's own tests already build/cache)
against the identical `httptest.Server` the HTTP and MCP legs already
use, and extend each test's assertions to require all three legs
agree — identical resulting store state for the happy-path tests,
byte-identical refusal text for the three negative-path tests
(non-green bullet, unrecognized type, unknown change-id).

Remove every `// TODO(cli-dispatch-subcommands)` comment as its test
is completed. Grep for the marker before finishing this task and
confirm zero remain.

Verification: same command as Task 1, plus running
`go test ./internal/mcp/... -run TestParity -v` (or whatever the
parity tests are actually named — check) individually to confirm all
three legs of each are genuinely exercised, not just present.
