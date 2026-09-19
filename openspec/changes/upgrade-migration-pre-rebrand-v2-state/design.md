# Design — Migrate pre-rebrand v2 state on upgrade

## Ownership

One repository, `sgt`. New package `internal/upgrademigrate`. Touches
`cmd/sgt/main.go` (new `migrate` subcommand, a call at the top of
`run`/`status`/`ui`/`mcp`), `internal/ui/server.go` (new endpoint or
field exposing migration status), and `internal/ui/static/index.html`
(a banner rendering that status when it's not clean).

## `internal/upgrademigrate` — detection, migration, verification, sentinel

```go
package upgrademigrate

// Paths, resolved once per call the same way internal/config and
// internal/dag already do (via os.UserHomeDir()), never assumed global.
type paths struct {
	oldConfigDir string // ~/.config/sergeant
	oldDBPath    string // ~/.local/share/sergeant/sergeant.db
	oldFleetRoot string // ~/.local/share/sergeant-v2/fleet
	// v1's ~/.local/share/sergeant/fleet is deliberately never
	// computed or referenced here — this package has no path that
	// could accidentally touch it.

	newConfigDir string // ~/.config/sgt
	newDBPath    string // ~/.local/share/sgt/sgt.db
	newFleetRoot string // ~/.local/share/sgt-v2/fleet
	sentinelPath string // ~/.config/sgt/.migration-from-sergeant.json
}

// Sentinel is the on-disk record of the last migration attempt.
type Sentinel struct {
	Status      string    `json:"status"` // "verified" or "failed"
	SourcePaths []string  `json:"source_paths"`
	Timestamp   time.Time `json:"timestamp"`
	Conflicts   []string  `json:"conflicts,omitempty"`  // per-item skips: "config:foo.yaml", "store", "fleet:taskA/repoB"
	Mismatches  []string  `json:"mismatches,omitempty"` // verification failures, only set when status == "failed"
}

// Detected reports whether any recognized pre-rebrand v2 state exists.
// Never true because of v1's ~/.local/share/sergeant/fleet alone.
func Detected() (bool, error)

// Run performs detection, migration, and verification, writing the
// sentinel. Safe to call on every subcommand invocation:
//   - no pre-rebrand v2 state and no sentinel -> no-op, returns nil, nil
//   - sentinel already "verified" -> no-op (no re-scan of source paths)
//   - sentinel "failed" or absent with state detected -> runs migration
// Returns the resulting Sentinel (nil if nothing to do) so callers
// (main.go's automatic check, the `migrate` subcommand, the UI's status
// endpoint) all read the same shape.
func Run() (*Sentinel, error)

// LastResult reads the sentinel file without triggering migration —
// used by internal/ui's status endpoint so a dashboard load never
// itself performs filesystem copies.
func LastResult() (*Sentinel, error)
```

### Config migration

For each `<oldConfigDir>/*.yaml` and `*.yml`: if no file of the same
name exists at `<newConfigDir>`, copy it (content-identical, preserving
mode). If one does exist and its content differs, record
`"config:<filename>"` in `Conflicts` and skip it — never overwrite. If
its content is byte-identical, treat it as already-migrated (not a
conflict) so a partial prior run doesn't get permanently stuck on files
it already copied correctly.

### Store migration (WAL-safe)

Never `os.Rename`/copy `oldDBPath` directly — WAL mode
(`internal/store/store.go:231`) means recent writes can live only in
`sergeant.db-wal`, not in the main file. Instead:

```sql
ATTACH DATABASE 'oldDBPath' AS src;
VACUUM src INTO 'tmpSnapshotPath';
```

(equivalently, open `oldDBPath` directly and run `VACUUM INTO
'tmpSnapshotPath'` against it — either form produces one consistent,
WAL-flushed file with no separate `-wal`/`-shm` siblings). If
`newDBPath` does not already exist, move `tmpSnapshotPath` into place
as `newDBPath`. If `newDBPath` already exists, record `"store"` in
`Conflicts` and leave both files untouched — no row-level merge (Proposal's
Non-Goals). The moved file is then opened once through
`store.Open(newDBPath)` so its existing `migrate()` family
(`internal/store/store.go:282` onward) brings its schema fully current,
exactly as it would for a long-lived `sgt.db` — no new schema-handling
code in this package.

### Fleet migration

For each `<oldFleetRoot>/<task>/<repo>/` directory: if no directory
exists at the corresponding `<newFleetRoot>/<task>/<repo>/`, copy it
recursively preserving all file modes and the `.git` internals exactly
(equivalent to `cp -a`), then verify by running `git status
--porcelain` and `git rev-parse HEAD` in both the source and the copy
and asserting identical output. If a directory already exists at the
destination path, record `"fleet:<task>/<repo>"` in `Conflicts` and
skip it.

### Verification and sentinel-writing

After the three migration steps: compare project counts
(`config.ListProjects()` against both config dirs), run/phase counts
(query both stores directly — the source snapshot copy, not the live
`sergeant.db`, to avoid a second WAL read), and each successfully
copied worktree's git status/HEAD. Any mismatch is appended to
`Mismatches` and the sentinel is written with `Status: "failed"`; the
destination is left exactly as it is (Decision 5 — no rollback). If
everything matches (accounting for anything already skipped into
`Conflicts`, which is not itself a failure), the sentinel is written
`Status: "verified"`.

## Wiring into `cmd/sgt/main.go`

```go
case "run", "status", "ui", "mcp":
	if _, err := upgrademigrate.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "upgrade migration: %v\n", err)
		os.Exit(1)
	}
	// ... existing dispatch to runProject/showStatus/startUI/startMCP
case "migrate":
	sentinel, err := upgrademigrate.Run()
	// print sentinel (or "nothing to migrate" if nil) to stdout, exit
	// non-zero if sentinel.Status == "failed" or len(Conflicts) > 0
```

`Run()`'s own internal sentinel check (already-`verified` short-circuit)
means this added call is cheap on every normal invocation once
migration has completed — one stat of the sentinel file, no source-path
scanning.

## Surfacing in `internal/ui`

`internal/ui/server.go` gains a read of `upgrademigrate.LastResult()`
(never `Run()` — the dashboard must not trigger a migration attempt
itself) exposed via the existing `/api/analytics` response (a new
`migration` field, `nil` when there's nothing to report) or a small new
`/api/migration-status` endpoint if `handleAnalytics` is judged the
wrong place for it — implementer's call, but it must be reachable
without a dedicated new drawer being opened, since the whole point is
that an operator sees it without knowing to look. `index.html` renders
a dismissible-per-session banner when the field is non-nil and
`Status == "failed"` or `len(Conflicts) > 0`, naming the specific
conflicts/mismatches verbatim from the sentinel.

## Test shape (Quality bar → concrete tests)

- Fixture builder helper: writes a real pre-rebrand-shaped tree (YAML
  files under a fake `~/.config/sergeant`, a real SQLite db built by
  running `internal/store`'s own schema against a `sergeant.db` path,
  a real git worktree under a fake `~/.local/share/sergeant-v2/fleet`)
  rooted at a `t.TempDir()`-provided fake `$HOME`, so `os.UserHomeDir()`
  resolves into the fixture without touching the real home directory.
- `internal/upgrademigrate/migrate_test.go`: `Run()` against the
  fixture, then independently re-open the destination store/config/
  fleet directly (not via `upgrademigrate`'s own return value) and
  assert row/file/worktree parity with the source.
- Idempotency: call `Run()` twice against the same fixture, assert
  identical destination state and that the second call performed no
  filesystem writes beyond re-reading the sentinel (detectable via
  mtimes or a spy).
- v1-exclusion: fixture includes both
  `~/.local/share/sergeant/fleet/<task>/<repo>` (v1) and
  `~/.local/share/sergeant-v2/fleet/<task>/<repo>` (pre-rebrand v2);
  assert only the latter appears at the destination.
- Conflict: pre-populate one destination file/db/worktree before
  calling `Run()`; assert it's reported in `Conflicts`, left byte- (or
  git-state-) identical to what it was before, and unrelated items
  still migrated.
- WAL-safety: write to the fixture's `sergeant.db` and leave the write
  sitting in `-wal` (i.e. don't force a checkpoint) before calling
  `Run()`; assert the destination reflects that write.
- Failed→retry: force a verification failure (e.g. inject a mismatch by
  altering the destination between migration and verification, or a
  test-only seam), assert `Status == "failed"` and destination left in
  place, then call `Run()` again after removing the induced fault and
  assert it reaches `Status == "verified"`.
- Per-entrypoint scope: a small test invoking the compiled `sgt` binary
  (subprocess, `exec.Command`, matching the pattern established in
  `mcp-dispatch-and-create-pr-tools`'s parity tests) as each of `run`,
  `status`, `ui`, `mcp` against a fixture `$HOME`, asserting migration
  ran (sentinel written) for all four, not just one.
