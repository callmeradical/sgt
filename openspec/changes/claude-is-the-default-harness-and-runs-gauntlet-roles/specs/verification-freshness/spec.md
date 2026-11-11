# Verification freshness

## ADDED Requirements

### Requirement: A verification counts only if it passed after the last code change

For every harness, a gate or `review` SHALL count as passed, for skipping on
resume, for the resume response's skipped list, and for the run's outcome,
only when its newest record of the same name and kind for that repo is
`passed`, and is newer than that repo's newest `fix` record and every record
of an agent phase other than `review` placed before it in the repo's
effective pipeline. Any other pipeline phase
SHALL count as passed when its newest record for that repo is `passed`.

#### Scenario: A gate broken by a fix is re-run

- **WHEN** gate `G` passed, the review failed, an operator-started cycle-1
  fix makes `G` fail, and cycle 2 runs
- **THEN** `G` has a new phase record with fix cycle 2

#### Scenario: A failed fix phase does not let stale gates count

- **WHEN** gate `G` passed and an operator-started cycle-1 `fix` phase fails
- **THEN** the cycle-2 run of the stage records a new `G` phase record with
  fix cycle 2, newer than the failed `fix` record

#### Scenario: A gate fix re-runs every gate of its repo

- **WHEN** gates `A` and `B` ran, `A` passed and `B` failed, and an
  operator-started fix cycle runs
- **THEN** both `A` and `B` have new phase records with fix cycle 1

#### Scenario: An agent phase before test makes earlier gate passes stale

- **WHEN** a repo's pipeline is `build, docs, test`, where `docs` is an agent
  phase, and the store holds a passed gate record followed by a newer `docs`
  record (seeded directly, since the engine would not produce this order)
- **THEN** `PhasePassed` reports that gate as not passed

#### Scenario: A review before test does not make gates stale

- **WHEN** a repo's pipeline is `build, review, test`, and all three pass
- **THEN** the run ends `passed`

#### Scenario: An agent phase after test is trusted

- **WHEN** a repo's pipeline is `build, test, docs`, and every phase passes
  on the first dispatch
- **THEN** the run ends `passed`

#### Scenario: A gate named like a phase is not confused with it

- **WHEN** a repo has a gate named `build`, and the agent `build` phase
  failed while the `build` gate passed
- **THEN** the agent `build` phase does not count as passed

#### Scenario: The rule applies to named harnesses

- **WHEN** a project with `agent: goose` has gate `A` passed and gate `B`
  failed, and an operator-started fix cycle runs
- **THEN** both `A` and `B` have new phase records with fix cycle 1

#### Scenario: A blocking review is not skipped on resume

- **WHEN** a project with `gauntlet_roles: false`, or a goose project, has a
  review that reported a severity `error` finding, and the run is resumed
- **THEN** the review phase was recorded `failed`, the resume re-runs it, and
  the run is not `passed` unless the new review has no blocking finding

#### Scenario: The run details' skip list agrees with resume

- **WHEN** a run's details are fetched and then the run is resumed
- **THEN** the details' `resume_skips` equals the resume response's
  `skipped`

#### Scenario: Resume's skipped list names only fresh passes

- **WHEN** a run's review passed, a later `fix` ran, and the run is resumed
- **THEN** the resume response's `skipped` list does not contain `review`

#### Scenario: Resume does not re-run passed work phases

- **WHEN** a run whose `plan` and `build` passed and whose gate failed is
  resumed
- **THEN** no new `plan` or `build` record is written for that repo

#### Scenario: Resume after an exhausted correction re-runs stale gates

- **WHEN** a correction was exhausted with its last `fix` phase `failed`, and
  the run is resumed
- **THEN** every gate of that repo re-runs, and the run can end `passed` if
  they and the review pass

### Requirement: A run never ends passed without a passing result for every required phase

Before a run is recorded `passed`, by dispatch, resume, an operator fix cycle,
or `sgt run`, sgt SHALL check every repo the run is accountable for: its
intent's bullets, or, for a run with no intent, its resolved stages. Every pipeline phase
and every configured gate must count as passed by the freshness rule. A `fix`
phase is never required. When the check fails, the run SHALL
be recorded `failed` with a reason naming the phase and the repo.

#### Scenario: A stale gate fails the run

- **WHEN** at the end of a run a repo's gate has no passed record newer than
  that repo's newest `build` or `fix` record
- **THEN** the run's terminal status is `failed` with a reason naming that
  gate and repo

#### Scenario: A default pipeline with every phase passing passes

- **WHEN** a repo with the default pipeline and roles on runs `plan`,
  `build`, its gates and `review`, all passing
- **THEN** the run ends `passed`

#### Scenario: An unrun repo fails the run

- **WHEN** a dispatch over repos `a` and `b` fails on `a`, and an
  operator-started fix cycle corrects `a`
- **THEN** `b` still has no phase records, and the run ends `failed` with a
  reason naming `b` and saying to resume, not `passed`, and no bullet is
  `green`

#### Scenario: Resume after "has not run yet" runs the remaining repo

- **WHEN** a non-DAG dispatch over repos `a` and `b` failed on `a` before
  `b` ran, `a` was then corrected, and the run is resumed
- **THEN** `b` gets `build` and gate phase records, `a`'s passed phases are
  not re-run, and the run can end `passed`

#### Scenario: A bullet repo outside the DAG is reported, not left for resume

- **WHEN** a DAG project's intent has a bullet for repo `x`, which is in no
  DAG stage, and every stage passes
- **THEN** the run ends `failed` with a reason saying `x` is in no stage and
  cannot run, and the reason does not say to resume

#### Scenario: Gates are not required when the pipeline has no test

- **WHEN** a repo's pipeline is `plan, build` and it configures gates, and
  `plan` and `build` pass
- **THEN** the run ends `passed`

#### Scenario: A guard failure records no delivery

- **WHEN** a run's stages complete but the guard fails
- **THEN** the run is `failed`, and no `run.delivered` envelope exists for it

#### Scenario: A passed run-fix records delivery

- **WHEN** an operator fix cycle ends with the run `passed`
- **THEN** a `run.delivered` envelope exists for the run

#### Scenario: A failed fix later made good does not fail the run

- **WHEN** a repo's newest `fix` record is `failed`, and every gate and the
  review have passing records newer than it
- **THEN** the run can end `passed`

### Requirement: The test entry is satisfied by gate records

For the terminal check, a repo's `test` pipeline entry SHALL be satisfied by
its configured gate records, or by the single placeholder `test` gate record
when the repo configures no gates.

#### Scenario: Configured gates satisfy the test entry

- **WHEN** a repo's pipeline is `build, test` with gates `lint` and `unit`,
  and both gates have fresh passing records
- **THEN** the run ends `passed`, even though no record is named `test`

### Requirement: A blocked reason says why the phase failed

Envelope reasons SHALL be taken as today, with the last eligible one
winning. An agent-named `blocked_reason` is always eligible. A review's
blocking findings are eligible only while that repo's newest review record
is `failed`. When none is eligible, a failed run's reason SHALL be the newest current
failed phase's sgt-authored error, prefixed with its repo and phase
and cut to 300 characters, before falling back to the generic text. It SHALL
never contain raw agent output.

#### Scenario: A handoff failure is not described as a gate failure

- **WHEN** a run fails because its `build` attempt handed off, under change
  1's interim behaviour
- **THEN** the bullets' `blocked_reason` begins `<repo>: build:` and names
  the handoff, and does not contain `gates did not pass`

#### Scenario: A resolved HOLD is not the reason for a later no-verdict review

- **WHEN** a HOLD with gap `G` blocked the run, the run is resumed, and the
  fresh beta produces no verdict
- **THEN** the bullets' `blocked_reason` names the missing verdict and does
  not contain `G`

#### Scenario: A resolved HOLD in one repo is not the reason for another repo's failure

- **WHEN** repo `a`'s HOLD with gap `G` was corrected by `run-fix`, the run
  is resumed, and repo `b`'s gate fails
- **THEN** the bullets' `blocked_reason` names `b` and its gate, and does
  not contain `G`

#### Scenario: An agent-reported reason still wins

- **WHEN** a `build` exits 0 with `blocked_reason: "needs a human decision"`
  in its envelope, and the run then fails on a gate
- **THEN** the bullets' `blocked_reason` is `needs a human decision`

#### Scenario: A later current HOLD outranks an earlier agent reason

- **WHEN** a `plan` envelope names `blocked_reason: "R"`, and the run's
  review later records a current HOLD with gap `G`
- **THEN** the bullets' `blocked_reason` is `G`

#### Scenario: Raw output never reaches the reason

- **WHEN** an agent attempt exits 1 with output containing `RAW-TRANSCRIPT`
- **THEN** the bullets' `blocked_reason` does not contain `RAW-TRANSCRIPT`
