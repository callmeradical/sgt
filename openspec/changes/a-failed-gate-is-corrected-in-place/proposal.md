# Proposal — A failed gate is corrected in place, not just retried

## Repository

One repository: `sgt`.

## Requirements served

PRD: `docs/prd-fix-a-failed-gate-in-place.md`.

## Problem

`RunCodeGate` (`internal/runner/runner.go`) runs a deterministic gate
exactly once, with no retry of its own — unlike `RunAgentPhase`, which
already retries within its own turn via `e.Project.ResolvedRetries`.
Once a gate fails for a real reason, `RunStage` (`internal/dag/
engine.go`) returns an error, `executeRun` (`internal/ui/dispatch.go`)
commits whatever exists and records the run terminal. The only recovery
paths today are `POST /api/run-resume` (re-runs the same command
verbatim — only helps a genuine flake) or a brand-new `POST /api/dispatch`
(a fresh worktree/branch, discarding the failed run's own). Nothing
looks at *why* a gate failed and acts on it.

## Proposal

- A new endpoint, `POST /api/run-fix` (mirrors `/api/run-resume`'s
  `{"id": "<run-id>"}` shape and its resumable-status/active-run/
  worktree-still-exists preconditions), starting a corrective cycle for
  a failed/blocked run.
- One cycle: an agent phase (kind `fix`) runs in the run's existing
  worktree/branch, its prompt built from the failing phase's own
  `GateResult.Output`/`Command` (or the failing agent phase's recorded
  output) — already redacted and bounded at the source
  (`RunCodeGate`/`RunAgentPhase` already call `redact.Text`/
  `redact.Truncate` before anything is recorded; this reuses that,
  building no new redaction path). After the fix phase completes, the
  pipeline re-runs from the failing phase onward, the same
  `phasePassed`-driven skip logic Resume already uses.
- If the gate now passes, the run concludes normally. If it fails again,
  the cycle repeats automatically — the operator triggers only the
  first cycle, per the PRD — until it passes or a configured bound of
  cycles is reached, then the run falls back to `blocked`/`failed`
  exactly as today, with a reason naming that the fix budget was
  exhausted.
- The bound is a setting: a new `FixRetries` field on `config.Project`'s
  `Defaults` and per-repo `Repo` (mirrors the existing `Retries`/
  `ResolvedRetries` project-default-with-repo-override pattern exactly),
  defaulting to 5 when unset.
- Each phase recorded during a corrective cycle carries which cycle it
  belongs to (0 = the original run, 1 = first fix cycle, 2 = second,
  ...), so the dashboard can group them into a visible "Attempt N of M"
  child block per cycle and render the real phase sequence each cycle
  traverses (e.g. `test` → `build` → `test`) as a connected loop,
  distinct from the original run's own linear phase sequence.

## Out of scope

Per the PRD: firing a corrective cycle automatically the instant any
gate fails (first cycle is always operator-triggered); a failure whose
worktree no longer exists (unchanged — a fresh dispatch is the existing
answer); any change to the existing plain Resume action; choosing or
overriding which agent CLI drives the fix phase (reuses the run's own,
same as Resume already does implicitly); building any new redaction
mechanism (reuses the existing choke point at the source).
