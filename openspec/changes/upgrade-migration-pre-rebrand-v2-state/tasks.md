# Tasks — Migrate pre-rebrand v2 state on upgrade

One repository, `sgt`. Task order matters: 2 depends on 1; 3 depends on
1; 4 depends on 1 and 2 (needs the sentinel shape and a real migration
to have something to display); 5 depends on all of 1-4.

## Task 1 — `internal/upgrademigrate` core: detection, migration, verification, sentinel

Repository: `sgt`. Depends on: nothing.

Read first: this change's `design.md` in full; `docs/prd-upgrade-migration.md`
(especially the Decisions section); `internal/config/config.go`'s
`LoadProject`/`internal/config/list.go`'s `ListProjects` (config path
resolution); `internal/store/store.go`'s `Open` (WAL pragma, `migrate()`
family); `internal/dag/engine.go`'s `FleetRoot`/`FleetDir` (v1-vs-v2
fleet distinction — read the doc comment at lines 91-96 carefully, it
is the definitive statement of what NOT to touch).

Build:
- `paths` struct and its resolution (mirrors existing path-resolution
  logic in `config.go`/`list.go`/`store.go`/`engine.go` exactly — same
  `os.UserHomeDir()` pattern, same default suffixes).
- `Sentinel` struct, `Detected()`, `Run()`, `LastResult()` exactly as
  design.md specifies.
- Config migration (per-file copy, skip-and-report on differing
  conflict, treat byte-identical as already-migrated).
- Store migration (`VACUUM INTO` for WAL-safety, conflict-and-skip if
  destination already exists, no row-level merge).
- Fleet migration (recursive copy preserving git internals, verified
  via `git status --porcelain`/`git rev-parse HEAD` comparison,
  conflict-and-skip per `<task>/<repo>` item).
- Verification pass writing `Status: "verified"` or `"failed"` with
  `Mismatches` populated on failure; no rollback on failure.

Verification: `go build ./... && go vet ./internal/... && go test
./internal/upgrademigrate/... -race -count=1`

Scenarios needing direct test coverage (see design.md's "Test shape"
section for the fixture-builder approach):
- Full migration against a complete fixture (config + store + fleet),
  re-read independently at the destination — proves data survives, not
  just that `Run()` returns no error.
- Idempotency: `Run()` called twice, identical destination state,
  second call performs no source-path re-scan writes.
- v1-exclusion: fixture has both v1's `sergeant/fleet` and pre-rebrand
  v2's `sergeant-v2/fleet` present; only the latter is touched.
- Per-item conflict: a pre-existing differing destination file, db, and
  worktree (three separate sub-tests or one fixture exercising all
  three) — each reported by name in `Conflicts`, left untouched,
  unrelated items still migrate.
- WAL-safety: a write left sitting in `-wal` (no forced checkpoint)
  survives into the destination.
- Failed→retry: induce a verification mismatch, assert `Status ==
  "failed"` and destination left in place with the mismatch named, then
  clear the fault and call `Run()` again, assert it reaches
  `Status == "verified"`.

## Task 2 — Wire automatic migration into `run`/`status`/`ui`/`mcp`, and the `sgt migrate` subcommand

Repository: `sgt`. Depends on: Task 1.

Read first: `cmd/sgt/main.go`'s `main()` switch statement and
`printUsage`/`printHelpTopic` (the manual-driven help system —
`internal/manual/manual.go`'s `commandTable` needs a `sgt migrate`
entry, matching the pattern the `mcp-dispatch-and-create-pr-tools`/
`cli-dispatch-subcommands` changes already used for their four
subcommands).

Build:
- A call to `upgrademigrate.Run()` at the top of the `run`, `status`,
  `ui`, and `mcp` cases (before any of their existing logic), failing
  loudly (stderr + non-zero exit) if it returns an error — never
  silently swallowed.
- New `migrate` case calling `upgrademigrate.Run()` and printing the
  resulting sentinel (or "nothing to migrate" if `Run()` returns `nil,
  nil`), exiting non-zero if `Status == "failed"` or `Conflicts` is
  non-empty.
- `internal/manual/manual.go`'s `commandTable`: one new entry for `sgt
  migrate`.

Verification: same command as Task 1, plus `go test ./cmd/sgt/... -count=1`.

Scenarios needing direct test coverage:
- Per-entrypoint: the compiled binary invoked as each of `run`,
  `status`, `ui`, `mcp` against a fixture `$HOME` with pre-rebrand v2
  state present, asserting the sentinel is written (migration ran) for
  every one of the four — not just one, and not asserted only via code
  inspection of the switch statement.
- `sgt migrate` against a fixture with no pre-rebrand v2 state prints
  "nothing to migrate" and exits 0.
- `sgt migrate` against a fixture with an induced conflict prints the
  conflict and exits non-zero.
- `sgt --help`/`sgt help migrate` includes the new subcommand (matches
  the existing test pattern used for the `dispatch`/`runs`/
  `run-details`/`create-pr` manual entries).

## Task 3 — Dashboard/API surfacing of migration status

Repository: `sgt`. Depends on: Task 1.

Read first: `internal/ui/server.go`'s `handleAnalytics`; `internal/ui/static/index.html`'s
existing banner/alert patterns (if any exist — otherwise the simplest
addition that fits the page's existing structure, not a new drawer).

Build:
- Expose `upgrademigrate.LastResult()` (read-only — never triggers
  migration from a dashboard load) through `internal/ui`'s API, either
  as a new field on the existing analytics response or a small new
  endpoint — implementer's call, documented in the PR description.
- `index.html`: a banner rendered when the exposed status is non-nil
  and (`Status == "failed"` or `Conflicts` non-empty), naming the
  specific conflicts/mismatches verbatim.

Verification: same command as Task 1, plus `go test ./internal/ui/... -count=1`.

Scenarios needing direct test coverage:
- The new endpoint/field returns `nil`/absent when no sentinel exists.
- It returns the sentinel's actual `Status`/`Conflicts`/`Mismatches`
  when one does, proven against a real `upgrademigrate.Sentinel`
  written to a fixture path the test server is pointed at — not a
  hand-built fake response.
- A dashboard load (`GET` on whatever endpoint/field this task adds)
  does not itself write or modify the sentinel file (assert mtime
  unchanged before/after the request).

## Task 4 — Full quality-bar closure pass

Repository: `sgt`. Depends on: Tasks 1-3.

Read first: `docs/prd-upgrade-migration.md`'s Quality bar section in
full — this task exists specifically to catch any bar item Tasks 1-3
didn't already cover end-to-end, not to re-do their work.

Build: nothing new by default — this task is a verification pass. If a
Quality bar item has no corresponding test after Tasks 1-3, write it
here rather than skipping it.

Verification: `go build ./... && go vet ./... && go test ./... -count=1`
on both macOS and the Linux container (`sgt-linux-test`), green, with
every Quality bar item traceable to a specific test by name in the PR
description.
