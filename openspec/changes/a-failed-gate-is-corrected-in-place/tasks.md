# Tasks — A failed gate is corrected in place

One repository, `sgt`, so one task.

## Task 1 — `FixRetries` setting, `fix_cycle` tracking, the corrective loop, and its dashboard rendering

Repository: `sgt`. Depends on: nothing. Read
`openspec/changes/a-failed-gate-is-corrected-in-place/{proposal,design}.md`
and `specs/gate-fix-loop/spec.md` first — they are binding. Read
`AGENTS.md`. Work test-first per decision D3. Before writing anything,
read: `internal/ui/run_lifecycle.go`'s `handleRunResume` (the exact
preconditions and worktree-reuse pattern to mirror for `handleRunFix`);
`internal/dag/engine.go`'s `RunStage`, `phasePassed`, and the `Resume`
field (the skip-already-passed mechanism this reuses, not reimplements);
`internal/runner/runner.go`'s `RunCodeGate`/`RunAgentPhase` and
`GateResult` (where output is already redacted/bounded at the source,
and where each builds its own `PhaseRecord`); `internal/config/
config.go`'s `Retries`/`ResolvedRetries` (the exact pattern `FixRetries`/
`ResolvedFixRetries` mirrors); `internal/ui/dispatch.go`'s
`recordTerminalRun`/`blockedReasonForRun` (where a corrective-budget-
exhausted reason becomes a third source, alongside the two that already
exist).

- Add `FixRetries` to `config.ProjectDefaults` and `config.Repo`, and
  `Project.ResolvedFixRetries(repoName string) int` per design.md
  exactly (repo override wins if non-zero, else project default if
  non-zero, else the built-in default of 5).
- Add `phases.fix_cycle INTEGER NOT NULL DEFAULT 0` (migration for
  existing databases) and `PhaseRecord.FixCycle int`. Add
  `PhaseRunner.FixCycle int`; both `RunCodeGate` and `RunAgentPhase` set
  it on the `PhaseRecord` they already build before calling
  `Store.RecordPhase`. Zero for every existing caller — no behavior
  change for a normal dispatch or a plain Resume.
- Add `POST /api/run-fix` (`internal/ui/gatefix.go`) per design.md:
  same preconditions as `/api/run-resume` (resumable status, not
  already active) plus refusing when the run's fleet worktree no longer
  exists.
- Add the corrective-loop orchestration (`runFixCycles` or an
  equivalent name): builds the fix agent's prompt from the last failed
  phase's real, already-recorded (already-redacted) output and gate/
  phase name; runs the fix phase; re-enters `RunStage` with
  `engine.Resume = true` exactly as plain Resume does; on success,
  concludes the run passed; on failure, repeats automatically up to
  `ResolvedFixRetries`'s bound; on exhausting the bound, concludes the
  run's existing failed/blocked outcome with an explicit reason naming
  budget exhaustion (a new source `blockedReasonForRun` — or an
  equivalent resolution — checks, distinct from its two existing
  sources).
- Add `fix_cycle` to `GET /api/run-details`'s phase payload and to the
  dashboard's phase rendering: group phases by `fix_cycle` into labeled
  child blocks ("Attempt N of M"), rendering each cycle's real repeated
  phase sequence connected to the gate it re-attempts. Exact visual
  treatment is an implementation choice per design.md; the grouping and
  the repeated-phase relationship must both be real and visible, not
  simulated with static example data.
- Do not change `/api/run-resume`'s existing behavior. Do not add a new
  redaction mechanism — reuse the already-redacted `GateResult.Output`/
  agent-phase output as recorded. Do not let a corrective cycle pick a
  different agent CLI or model than the run's own.

Verification: `go build ./... && go vet ./... && go test $(go list ./internal/... | grep -v repopolicy) ./cmd/sgt/... -count=1`.
Tests must cover every scenario in `specs/gate-fix-loop/spec.md`:
`POST /api/run-fix` re-enters the real existing worktree (assert against
real git state, not a mock); refusal when the worktree is gone; refusal
for a non-resumable run; the fix prompt contains the real failing gate's
actual recorded output; a cycle that fails triggers the next cycle
automatically with no second API call; a cycle that passes concludes the
run passed; exhausting the configured bound falls back to the existing
failed/blocked outcome with the new, distinct exhaustion reason (not a
repeat of the original gate-failure text); `ResolvedFixRetries`'s
default-vs-override resolution (unset defaults to 5; a repo override
wins); every phase across at least two corrective cycles is queryable by
its real `fix_cycle` value; and the dashboard's rendering function
(extracted and run under real `node`, per this repository's established
`extractJSFunction` harness pattern) groups a fixture with multiple
cycles into distinct, correctly labeled blocks. Exit status decides the
outcome.
