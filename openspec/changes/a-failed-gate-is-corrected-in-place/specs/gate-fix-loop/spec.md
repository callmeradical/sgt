# Corrective gate-fix loop

## ADDED Requirements

### Requirement: An operator can start a corrective cycle for a failed run

`POST /api/run-fix` SHALL start a corrective cycle for a run in a
resumable status whose worktree still exists, re-entering that exact
worktree and branch rather than creating a fresh one.

#### Scenario: Starting a fix cycle re-enters the existing worktree

- **WHEN** an operator calls `POST /api/run-fix` for a failed run whose
  fleet worktree still exists
- **THEN** the corrective agent phase runs inside that same worktree, on
  the same branch, not a newly created one

#### Scenario: A run whose worktree no longer exists cannot be fixed in place

- **WHEN** `POST /api/run-fix` is called for a run whose fleet worktree
  has already been reclaimed
- **THEN** the request is refused with a clear reason naming that the
  worktree no longer exists

#### Scenario: A non-resumable run cannot be fixed in place

- **WHEN** `POST /api/run-fix` is called for a run whose status is not
  one of the statuses `/api/run-resume` already accepts
- **THEN** the request is refused, mirroring `/api/run-resume`'s own
  refusal for the same class of run

### Requirement: The corrective agent receives the real, already-redacted failure

The corrective agent's prompt SHALL be built from the failing phase's
own recorded output — the same output already redacted and bounded at
its source — never raw or re-redacted separately.

#### Scenario: The fix prompt names the actual failing gate and its real output

- **WHEN** a corrective cycle starts for a run whose last failure was a
  named deterministic gate
- **THEN** the corrective agent's prompt includes that gate's name and
  its actual recorded output (the same `GateResult.Output` already
  produced by `redact.Text`/`redact.Truncate` at capture time)

### Requirement: A corrective cycle repeats automatically until it passes or exhausts its budget

Only the first corrective cycle SHALL require an explicit operator
action; every subsequent cycle SHALL start automatically, up to a
configurable bound.

#### Scenario: A failing cycle triggers another cycle without operator action

- **WHEN** a corrective cycle's re-attempted gate fails again, and fewer
  cycles have run than the configured bound
- **THEN** another corrective cycle starts automatically, with no
  further `POST /api/run-fix` call required

#### Scenario: A passing cycle concludes the run normally

- **WHEN** a corrective cycle's re-attempted gate passes
- **THEN** the run concludes with a passed outcome, the same as if the
  original gate had passed on its first attempt

#### Scenario: Exhausting the budget falls back to requiring a human

- **WHEN** the configured number of corrective cycles have all failed
- **THEN** the run reaches its terminal failed/blocked outcome exactly
  as it would have without this feature, with a reason explicitly
  naming that the corrective fix budget was exhausted — not a repeat of
  the original gate-failure text as if no correction had been attempted

### Requirement: The corrective-cycle bound is a configurable setting, not a fixed constant

The number of corrective cycles allowed before falling back to a human
SHALL be a per-project, per-repo-overridable setting, not a fixed
constant.

#### Scenario: An unset bound defaults to 5

- **WHEN** neither a project's `Defaults.FixRetries` nor a repo's own
  `FixRetries` override is set
- **THEN** the corrective loop allows exactly 5 cycles before falling
  back to requiring a human

#### Scenario: A repo-level override wins over the project default

- **WHEN** a repo sets its own `FixRetries` to a non-zero value
  different from `Defaults.FixRetries`
- **THEN** the repo's own value is the bound used for that repo's
  corrective cycles

### Requirement: Corrective-cycle phases are distinguishable from the run's own original phases

Every phase recorded during a corrective cycle SHALL record which cycle
it belongs to, and the dashboard SHALL render each cycle as its own
identified group distinct from the run's original phase sequence.

#### Scenario: A phase from cycle 2 is distinguishable from the original run's phases

- **WHEN** a run has gone through two corrective cycles
- **THEN** its phase records show, for each phase, which of the
  original run (cycle 0), cycle 1, or cycle 2 it belongs to

#### Scenario: The dashboard shows attempt count against the configured bound

- **WHEN** a run's corrective cycles are viewed in the dashboard
- **THEN** each cycle is labeled against the total configured bound
  (for example, "Attempt 2 of 5"), and the real repeated phase sequence
  within that cycle (for example `fix` → `build` → `test`) is shown as
  connected to the gate it re-attempts, not as an unconnected list
