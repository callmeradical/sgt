# Tasks — Sgt maintains its own OKF wiki of its work

One repository, `sgt`, so one task.

## Task 1 — `internal/wiki` package and wiring into `recordTerminalRun`

Repository: `sgt`. Depends on: nothing. Read
`openspec/changes/sgt-native-okf-wiki/{proposal,design}.md` and
`specs/wiki/spec.md` first — they are binding. Read the actual OKF v0.2
specification at
https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md
directly before writing any rendering code — do not rely solely on this
change's paraphrase of it. Read `AGENTS.md`. Work test-first per decision
D3. Before writing anything, read: `internal/ui/dispatch.go`'s
`recordTerminalRun` and `blockedReasonForRun` (the exact call site and
what it already has in hand); `internal/store/store.go`'s `RunRecord`,
`BulletRecord`, `ListBulletsForIntent`; `internal/runner/artifacts.go`'s
`captureArtifacts` and `artifactsRoot` (the never-fails/env-override
precedents to match); `internal/dag/engine.go`'s `FleetRoot` (the
`SGT_FLEET_DIR` override pattern to mirror for `SGT_WIKI_ROOT`).

- Add `internal/wiki/wiki.go`: `ProjectRoot(project string) string`,
  `Entry` struct, `RecordRun(entry Entry)` per design.md — including the
  package-level mutex serializing writes.
- Implement rendering for: the run's own dated concept page; that date's
  `index.md` and `log.md` (append, not overwrite, existing entries); the
  wiki root `index.md` (append the date folder if not already listed).
  Follow the OKF v0.2 rules precisely: `okf_version` only on the
  bundle-root index; no frontmatter on dated indexes; every concept page
  carries `type`; links are bundle-relative absolute (leading `/`).
- Wire `wiki.RecordRun` into `recordTerminalRun`, called after
  `blockedReasonForRun` resolves, loading the run's bullets via
  `ListBulletsForIntent` after `AdvanceBulletsForRun` completes so the
  page reflects their final status.
- Do not add a new goroutine — call `wiki.RecordRun` synchronously and
  inline, per design.md's rejected-alternatives reasoning.
- Do not write into `~/wiki` or reference the operator's personal wiki
  path anywhere.
- Do not add an LLM/API call.

Verification: `go build ./... && go vet ./... && go test $(go list ./internal/... | grep -v repopolicy) ./cmd/sgt/... -count=1`.
Tests must cover every scenario in `specs/wiki/spec.md`: a terminal run
produces a real concept page with the required frontmatter and content;
the wiki root ends up under `SGT_WIKI_ROOT`/`ProjectRoot`, never
resembling `~/wiki`'s path; a simulated write failure (e.g. an
unwritable root) is logged and does not change the run's own recorded
status (assert this by checking the run's store row still reflects its
real terminal status after a forced wiki-write failure); the root
index's `okf_version` frontmatter and a dated index's absence of
frontmatter are both asserted directly by parsing the written files, not
inferred from the rendering code; a blocked run's page contains the same
reason string `blockedReasonForRun` produced (assert equality, not just
non-emptiness); and two runs completing concurrently (goroutines racing
real calls to `wiki.RecordRun` in a test) both end up represented in the
shared date `index.md`/`log.md`/root `index.md` afterward — run with `go
test -race` for this specific test to prove no data race exists, not
just that both entries happen to appear. Exit status decides the
outcome.
