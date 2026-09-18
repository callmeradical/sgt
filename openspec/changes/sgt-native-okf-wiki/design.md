# Design — Sgt maintains its own OKF wiki of its work

## Ownership

One repository, `sgt`. Adds `internal/wiki/` (rendering and file layout),
touches `internal/ui/dispatch.go`'s `recordTerminalRun` (the call site).

## Root location

```go
// ProjectRoot resolves project's wiki root. SGT_WIKI_ROOT overrides the
// parent directory (mirrors dag.FleetRoot's SGT_FLEET_DIR and
// artifactsRoot's SGT_ARTIFACTS_ROOT), so tests never write into an
// operator's real home directory.
func ProjectRoot(project string) string {
	base := os.Getenv("SGT_WIKI_ROOT")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share", "sgt", "wiki")
	}
	return filepath.Join(base, project)
}
```

Default: `~/.local/share/sgt/wiki/<project>/` — alongside
`~/.local/share/sgt/artifacts/`, a sibling durable-output root, never
`~/wiki` (the operator's personal vault, a symlink to their Obsidian
vault — confirmed by direct inspection, not assumed).

## Layout (OKF v0.2 conformant)

```
<ProjectRoot>/
  index.md                  # okf_version frontmatter allowed here only
  2026-08-29/
    index.md                # no frontmatter (spec: only bundle-root may carry it)
    log.md                  # one ISO-8601 date heading, entries appended below it
    sgt-1788017560-abfe899e1.md   # one concept page per run, type: run
```

- Reserved filenames (`index.md`, `log.md`) and the always-required `type`
  frontmatter field on every concept page follow the spec exactly — see
  the spec quotes already gathered for `docs/prd-sgt-native-okf-wiki.md`.
- Cross-links use the spec's recommended bundle-relative absolute form
  (leading `/`, e.g. `/2026-08-29/sgt-....md`), not relative paths.
- Root `index.md` lists dated folders newest-first (an implementation
  choice — OKF only mandates order for `log.md`, not `index.md`).

## Package `internal/wiki`

```go
package wiki

// Entry is the rendering input for one run — already-durable facts
// recordTerminalRun has in hand by the time it calls RecordRun.
type Entry struct {
	Run           store.RunRecord
	Bullets       []store.BulletRecord
	BlockedReason string // "" unless the run's bullets moved to blocked
}

// RecordRun renders entry into ProjectRoot(entry.Run.Project)'s wiki: the
// run's own dated concept page, that date's index.md/log.md, and the
// wiki's root index.md. Idempotent: safe to call more than once for the
// same run — overwrites the run's own page, never duplicates an
// index/log line already present for it. Never returns an error and
// never panics: an I/O failure is logged and RecordRun returns, the same
// posture internal/runner/artifacts.go's captureArtifacts already
// established for a structurally identical concern (a derived side
// effect of a terminal event that must never fail the run it describes).
func RecordRun(entry Entry) { ... }
```

Concurrency: `recordTerminalRun` can fire for two different runs of the
same project at nearly the same time (independent dispatches). A naive
read-modify-write on a shared `index.md`/`log.md` can lose an update.
`internal/wiki` keeps one package-level `sync.Mutex` serializing every
`RecordRun` call — file I/O for a handful of short markdown files is
cheap enough that a single global lock costs nothing measurable and
removes the race entirely, rather than a more complex per-project or
per-date locking scheme this scale does not need.

## Rendered content

Concept page frontmatter: `type: run`, `title` (the run's slug or a
truncated brief), `description` (one line: status + brief excerpt),
`tags` (`[<work type>, <status>]`), `timestamp` (`run.UpdatedAt`,
RFC3339). Body: the brief in full, a table of the run's bullets (repo,
status, PR link or `—`), and the blocked reason section only when
`entry.BlockedReason != ""`.

Date `index.md`: one bullet per run created/updated that day, linking to
its concept page with a one-line description (status + brief excerpt).

Date `log.md`: one ISO-8601 date heading (this bundle's own date) with
one prose bullet appended per run as it completes — `**<Status>**:
[<title>](/<date>/<run-id>.md) — <description>`, matching the spec's
"leading bold word is a convention, not a requirement" and the real
vault's own observed style.

Root `index.md`: `okf_version: "0.2"` frontmatter, one bullet per date
folder that has at least one run, newest first.

## Wiring

`internal/ui/dispatch.go`'s `recordTerminalRun`, after
`blockedReasonForRun` resolves (the same point it already has
`bulletStatus`/`reason` in hand): load the run's bullets (already does,
via `AdvanceBulletsForRun`'s callers — read them back via
`ListBulletsForIntent` if not already in scope) and call
`wiki.RecordRun` with the assembled `Entry`. This runs synchronously
inline, not in a goroutine — matching `captureArtifacts`'s own
synchronous, best-effort call shape, and avoiding a new goroutine
lifecycle this codebase has repeatedly had to fix leaks in elsewhere.

## Rejected alternatives

**Writing asynchronously in a goroutine.** Rejected: adds a goroutine
lifecycle with no corresponding shutdown path, the exact bug class
already fixed at least once elsewhere in this codebase's history; local
markdown file writes are cheap enough not to need it.

**One wiki for all projects.** Rejected per the PRD's now-resolved open
question: per-project matches every other Sgt view's scoping.

**Storing okf_version per concept page.** Rejected: the spec states
index files are the only place frontmatter carries it (bundle-root
specifically), and it is not a recognized field for a concept page's
frontmatter.
