# Design — A failed gate is corrected in place

## Ownership

One repository, `sgt`. Touches `internal/config/config.go` (new setting),
`internal/store/store.go` (new `phases.fix_cycle` column + migration),
`internal/runner/runner.go` (`PhaseRunner` stamps the cycle onto every
phase it records), `internal/dag/engine.go` (nothing structural — the
existing `Resume`/`phasePassed` mechanism is reused as-is), a new
`internal/ui/gatefix.go` (the corrective-loop orchestration and the new
endpoint), and `internal/ui/static/index.html` (rendering).

## Config: `FixRetries`

Mirrors `Retries`/`ResolvedRetries` exactly:

```go
// ProjectDefaults
FixRetries int `yaml:"fix_retries,omitempty" json:"fix_retries"` // corrective-cycle bound (0 = use the built-in default of 5)

// Repo
FixRetries int `yaml:"fix_retries,omitempty" json:"fix_retries"` // overrides Defaults.FixRetries for this repository

// Same resolution rule as ResolvedRetries: repo override wins if non-zero,
// else Defaults.FixRetries, else the built-in default of 5 (zero at every
// level means "unset", not "zero attempts" — starting even the first
// corrective cycle is already an explicit, separate operator action, so
// there is no need for a zero-attempts case to disable this per repo).
func (p *Project) ResolvedFixRetries(repoName string) int {
	if repo, ok := p.Repos[repoName]; ok && repo.FixRetries != 0 {
		return repo.FixRetries
	}
	if p.Defaults.FixRetries != 0 {
		return p.Defaults.FixRetries
	}
	return 5
}
```

## Store: `fix_cycle` on `phases`

```
ALTER TABLE phases ADD COLUMN fix_cycle INTEGER NOT NULL DEFAULT 0
```

`PhaseRecord` gains `FixCycle int` (0 = the run's own original attempt;
1 = the first corrective cycle's phases; 2 = the second; ...).
`PhaseRunner` gains a `FixCycle int` field (zero value for every existing
caller — no behavior change for a normal dispatch or plain Resume); both
`RunCodeGate` and `RunAgentPhase` already build a `PhaseRecord` before
calling `Store.RecordPhase` — set `FixCycle: pr.FixCycle` there. Add
`fix_cycle` to `runColumns`-equivalent handling for phases (wherever
phases are scanned back) so it round-trips.

## The corrective loop (`internal/ui/gatefix.go`)

```go
// handleRunFix answers POST /api/run-fix ({"id": "<run-id>"}), mirroring
// handleRunResume's preconditions exactly (resumable status, not already
// active, project loads) plus one more: the run's worktree must still
// exist — a corrective cycle re-enters it, it does not recreate it.
func (srv *Server) handleRunFix(w http.ResponseWriter, r *http.Request) { ... }

// runFixCycles drives the corrective loop for runID, starting at cycle 1.
// The operator triggered cycle 1; every cycle after that is entered
// automatically by this same loop, not by another API call.
func (srv *Server) runFixCycles(ctx context.Context, proj *config.Project, runID string, repoName string) {
	limit := proj.ResolvedFixRetries(repoName)
	for cycle := 1; cycle <= limit; cycle++ {
		failure := srv.lastFailedPhase(runID, repoName) // reads the real, already-redacted GateResult/output from the store
		pr := &runner.PhaseRunner{ /* ..., FixCycle: cycle */ }
		if _, _, err := pr.RunAgentPhase(ctx, "fix", fixPrompt(failure), proj.ResolvedRetries(repoName)); err != nil {
			// the fix phase itself errored (not the gate) - treat as this
			// cycle's failure and continue the loop rather than aborting early
			continue
		}
		engine := dag.NewEngine(proj, srv.Store, /* router */)
		engine.Resume = true // reuses phasePassed exactly as plain Resume does
		if err := engine.RunStage(ctx, runID, /* the stage that was failing */); err == nil {
			srv.recordTerminalRun(runID, "passed")
			return
		}
	}
	// Budget exhausted: falls back to today's existing outcome, with a
	// reason that says so explicitly rather than repeating the last
	// gate-failure text as if no correction was ever attempted.
	srv.recordTerminalRunWithReason(runID, "failed", fmt.Sprintf(
		"corrective fix budget exhausted (%d/%d attempts); gate still failing", limit, limit))
}

// fixPrompt builds the corrective agent's brief from failure's already-
// redacted Output/Command (RunCodeGate/RunAgentPhase already call
// redact.Text/redact.Truncate before anything is recorded - this reads
// that recorded value, it does not redact anything itself).
func fixPrompt(failure lastFailure) string { ... }
```

`recordTerminalRunWithReason` (or an equivalent small addition to
`recordTerminalRun`'s existing reason-resolution) is needed because
`blockedReasonForRun`'s existing sources (an agent's own `blocked_reason`
payload key, or a review phase's findings) have no way to say "the fix
loop itself gave up" — this is a new, third source of a blocked reason,
checked before falling through to the existing synthesized default.

## UI: attempts as child blocks, the retry path as a loop

`GET /api/run-details`'s phase list already carries every phase for a
run; each now also carries `fix_cycle`. The dashboard groups phases by
`fix_cycle` into visually distinct blocks under the run's own phase
list — cycle 0 rendered exactly as today, each `fix_cycle >= 1` rendered
as its own labeled child block ("Attempt N of M", M from
`ResolvedFixRetries` surfaced via the run-details response). Within a
cycle's block, render its real phase sequence (e.g. `fix` → `build` →
`test`) with a connecting loop indicator back to the gate it re-attempts,
distinguishing "this is a repeat of an earlier phase" from the run's own
original linear sequence — exact visual treatment (a looped SVG edge, an
indent plus a "↻" glyph, etc.) is an implementation choice, not fixed
here; the requirement is that the grouping and the repeated-phase
relationship are both visible, not that a specific graphic library or
markup shape is used.

## Rejected alternatives

**Modeling a corrective cycle as a new child run record**
(`ParentRunID`), rather than more phases on the same run. Rejected: the
codebase already models "another attempt at the same named phase" as
another `PhaseRecord` row with an incrementing counter (the existing
`Attempt` field) — extending that established shape with a second,
orthogonal counter (`FixCycle`, orthogonal because it tracks a whole
gate-fix-retest cycle, not one phase's own retries) is smaller and more
consistent than introducing a parallel run hierarchy.

**Reusing the existing `Attempt` field for cycle number instead of a new
`FixCycle` field.** Rejected: `Attempt` already has a real, different,
established meaning (this phase name's own invocation count within one
turn, per `RunAgentPhase`'s `retries` parameter) — overloading it would
make a phase's own retry count and its corrective-cycle number
indistinguishable from each other.

**Letting the corrective loop pick a different agent CLI or model than
the original run.** Rejected per the PRD: reuses whatever the original
run's phases already used, the same implicit reuse Resume already
relies on.
