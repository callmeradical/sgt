# Proposal — Sgt maintains its own OKF wiki of its work

## Repository

One repository: `sgt`.

## Requirements served

PRD: `docs/prd-sgt-native-okf-wiki.md`.

## Problem

Sgt's durable record of a run (brief, repos, bullets, outcome, PR links,
review findings) lives only in its SQLite store, reachable through the
API/CLI/dashboard. There is no plain-file, browsable trail. The
operator's personal wiki at `~/wiki` already demonstrates a real,
recognizable convention — [Open Knowledge Format](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
v0.2 — but is the wrong place for Sgt's own operational history:
different content, different audience, and `~/wiki`'s own
`wiki-daily-digest` needs an LLM call because its source (raw agent
session transcripts) is unstructured — Sgt's own store data already
isn't.

## Proposal

- One wiki per project, rooted under Sgt's own data directory (see
  design.md for the exact path), never `~/wiki`.
- `recordTerminalRun` (`internal/ui/dispatch.go`) — the single place a
  run's outcome becomes a fact, per its own existing doc comment — is
  the trigger. When it fires, a new `internal/wiki` package renders that
  run into the project's wiki: a dated concept page (`type: run`
  frontmatter, the run's brief, its bullets' repos/status/PR links, and
  any blocked reason `blockedReasonForRun` already resolved), and
  updates that date's `index.md`/`log.md` and the wiki's root `index.md`
  to reference it.
- Deterministic rendering only — no LLM call, no new data captured;
  every fact rendered already exists on `store.RunRecord`/
  `store.BulletRecord` by the time `recordTerminalRun` runs.
- Best-effort, never fails the run: a wiki-write problem is logged, not
  propagated — the same posture `internal/runner/artifacts.go`'s
  `captureArtifacts` already established for a structurally identical
  concern (a derived side effect of a terminal event that must never
  block or break the primary path).

## Out of scope

Per the PRD: writing into `~/wiki`; LLM-synthesized narrative content;
any Obsidian-specific dependency; backfilling runs that predate this
change; any change to what data Sgt records.
