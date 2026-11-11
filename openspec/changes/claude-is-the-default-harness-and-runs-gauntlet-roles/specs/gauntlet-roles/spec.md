# Gauntlet roles

## ADDED Requirements

### Requirement: Claude build, fix and review phases run as gauntlet roles by default

When a project's resolved harness is `claude` and gauntlet roles apply, an
agent phase named `build` or `fix` SHALL be invoked with
`--agent gauntlet:alpha`, and an agent phase named `review` SHALL be invoked
with `--agent gauntlet:beta`. Every other phase name SHALL be invoked with no
`--agent` argument. Gauntlet roles apply unless the project sets
`defaults.gauntlet_roles: false`. They never apply to a harness other than
`claude`.

#### Scenario: A claude build phase runs as alpha

- **WHEN** a project with no `gauntlet_roles` setting runs a `build` phase with
  harness `claude`
- **THEN** the agent arguments contain `--agent gauntlet:alpha`

#### Scenario: A claude review phase runs as beta

- **WHEN** the same project runs a `review` phase
- **THEN** the agent arguments contain `--agent gauntlet:beta`

#### Scenario: A plan phase runs without a role

- **WHEN** the same project runs a `plan` phase
- **THEN** the agent arguments contain no `--agent` argument

#### Scenario: The opt-out restores the previous command

- **WHEN** a project sets `defaults.gauntlet_roles: false` and runs a `build`
  phase with harness `claude`
- **THEN** the agent arguments are identical to those produced before this
  change

#### Scenario: Other harnesses never receive a role

- **WHEN** a project with no `gauntlet_roles` setting runs a `build` phase with
  harness `goose`
- **THEN** the agent arguments contain no `--agent` argument and no `gauntlet`
  text

#### Scenario: Saving the project preserves the opt-out

- **WHEN** a project file containing `gauntlet_roles: false` under `defaults:`
  is saved through the refine path with an unrelated edit
- **THEN** the saved file still contains `gauntlet_roles: false`

### Requirement: A dispatch that needs the plugin is refused before anything exists

When gauntlet roles apply, `POST /api/dispatch`, plan approval,
`POST /api/run-resume`, `POST /api/run-fix` and `sgt run <project>` SHALL
each probe whether claude reports a plugin whose id
begins `gauntlet@` as installed and enabled. When the probe fails, the request
SHALL be rejected with an error naming what is missing and how to opt out. No run row, intent row, branch or
worktree SHALL be created, and an existing run's status SHALL NOT change.

#### Scenario: A dispatch without the plugin creates no run

- **WHEN** a project with roles on is dispatched and the probe reports no
  enabled gauntlet plugin
- **THEN** the response is 400 with a body naming `gauntlet` and
  `gauntlet_roles`, and the store contains no run for that request and the
  fleet directory contains no worktree for it

#### Scenario: A resume without the plugin leaves the run untouched

- **WHEN** a failed run of a project with roles on, whose bullet is
  `blocked`, is resumed while the probe reports no enabled gauntlet plugin
- **THEN** the response is 400 naming `gauntlet`, the run's status is still
  `failed`, the bullet is still `blocked`, and no new phase record exists for
  the run

#### Scenario: An approval without the plugin leaves the plan proposed

- **WHEN** a proposed plan for a project with roles on is approved while the
  probe reports no enabled gauntlet plugin
- **THEN** the response is 400 naming `gauntlet`, the intent's status is
  still `proposed`, its bullets are unchanged, and no run exists for it

#### Scenario: A fix without the plugin leaves the run untouched

- **WHEN** `POST /api/run-fix` is called for a failed run of a project with
  roles on, whose bullet is `blocked`, while the probe reports no enabled
  gauntlet plugin
- **THEN** the response is 400 naming `gauntlet`, the run's status is still
  `failed`, the bullet is still `blocked`, and no `fix` phase record exists

#### Scenario: A missing claude names the harness, not the plugin

- **WHEN** a project with roles on is dispatched and the probe cannot find
  the `claude` executable
- **THEN** the response is 400 with a body naming `claude` and
  `defaults.agent`, and no run exists for the request

#### Scenario: sgt run without the plugin creates no run

- **WHEN** `sgt run <project>` is invoked for a project with roles on while
  the probe reports no enabled gauntlet plugin
- **THEN** the command exits non-zero with a message naming `gauntlet` and
  `gauntlet_roles`, and the store contains no new run

### Requirement: Requests that do not use roles are never probed

When gauntlet roles do not apply to a project, no request SHALL invoke the
plugin probe.

#### Scenario: An opted-out project is not probed

- **WHEN** a project with `gauntlet_roles: false` is dispatched
- **THEN** the probe is not invoked

#### Scenario: A probe failure refuses with its own message

- **WHEN** a project with roles on is dispatched and the probe times out
- **THEN** the response is 400 with a body containing
  `could not read claude's plugin list`, and no run exists for the request

#### Scenario: A repeated request is answered before the probe

- **WHEN** a dispatch with `request_id` `R` already created a run, and the
  same request is sent again while the probe now reports no plugin
- **THEN** the probe is not invoked, and every field of the repeat's
  response, including `change_id`, `change_repo` and `change_dir`, equals
  the original response's

#### Scenario: A project-scope install does not satisfy the probe

- **WHEN** `claude plugin list --json` lists `gauntlet@gauntlet` enabled only
  with `scope: project`
- **THEN** the dispatch is refused with the no-plugin message

#### Scenario: A plugin without both roles is refused

- **WHEN** the enabled user-scope gauntlet install has no `agents/beta.md`
- **THEN** the dispatch is refused with the no-plugin message

#### Scenario: A plan proposal is not probed

- **WHEN** a project with roles on is dispatched with no repos named, so
  that only a proposed plan is recorded
- **THEN** the probe is not invoked, and the plan is recorded `proposed`

#### Scenario: A non-claude project is not probed

- **WHEN** a project with `agent: goose` is dispatched
- **THEN** the probe is not invoked

### Requirement: Beta's verdict is read from its complete output

The review prompt for a `gauntlet:beta` phase SHALL instruct beta to end with
exactly one line, `VERDICT: PASS` or `VERDICT: HOLD: <gap>`. The runner SHALL
parse the last such line from the attempt's complete output, before
truncation. A PASS SHALL record the review phase `passed` with no blocking
finding.

#### Scenario: A verdict after the truncation limit is still read

- **WHEN** a beta attempt's output is longer than the payload truncation limit
  and ends with `VERDICT: PASS`
- **THEN** the review phase is recorded `passed`

#### Scenario: A PASS with trailing text is read

- **WHEN** a beta attempt ends with `VERDICT: PASS — meets the bar.`
- **THEN** the review phase is recorded `passed`

#### Scenario: A long diff is capped and still delivered

- **WHEN** the run's diff is 300 KiB
- **THEN** the review prompt contains the truncation marker, the agent
  process starts, and its prompt is delivered on stdin

#### Scenario: An emphasised verdict line is read

- **WHEN** a beta attempt ends with `**VERDICT: PASS**`
- **THEN** the review phase is recorded `passed`

#### Scenario: The roles review prompt has one output contract

- **WHEN** the review prompt is built with roles on
- **THEN** it contains the `VERDICT:` contract, and contains neither the
  JSON-findings instruction nor `nothing else`

#### Scenario: A beta-written envelope cannot override the verdict

- **WHEN** a beta attempt writes `.sgt/envelope.json` with
  `gauntlet_verdict: pass` and no findings, and ends with
  `VERDICT: HOLD: G`
- **THEN** the review phase is `failed`, and its payload has
  `gauntlet_verdict` = `hold` and a blocking finding with summary `G`

#### Scenario: PASS clears findings beta wrote itself

- **WHEN** a beta attempt writes `.sgt/envelope.json` with an error finding
  and ends with `VERDICT: PASS`
- **THEN** the review phase is `passed`, and its payload's `findings` is
  empty

#### Scenario: A roles-off blocking review keeps its own findings

- **WHEN** a goose project's review attempt writes `.sgt/envelope.json` with
  an error finding summarised `S`, and exits 0
- **THEN** the review phase is `failed` with an error naming `S`, the
  recorded review envelope's `findings` still contains it, and when the run
  ends `failed` the bullet's `blocked_reason` contains `S`

#### Scenario: A review with no verdict is retried

- **WHEN** a project with roles on and `retries: 1` has a beta attempt exit
  0 with no `VERDICT:` line, and the second attempt ends `VERDICT: PASS`
- **THEN** there are two review attempt records, and the review phase is
  `passed`

#### Scenario: A roles-off blocking review is not retried

- **WHEN** a project with `gauntlet_roles: false` and `retries: 2` has a
  review attempt report a severity `error` finding
- **THEN** exactly one review attempt record exists, and it is `failed`

#### Scenario: Beta's output does not reach a downstream worktree

- **WHEN** upstream repo `a`'s review runs as beta with raw output containing
  `BETA-REASONING`, and downstream repo `b`'s stage then receives `a`'s
  handoff
- **THEN** no file under `b`'s worktree `.sgt/handoff/` contains
  `BETA-REASONING`

#### Scenario: The last verdict line wins

- **WHEN** a beta attempt's output quotes `VERDICT: PASS` mid-text and ends
  with `VERDICT: HOLD: x`
- **THEN** the recorded verdict is `hold` with gap `x`

### Requirement: A HOLD verdict records a failed review carrying the gap

A HOLD SHALL record the review phase `failed` with error
`gauntlet:beta HOLD: <gap>`. Its payload SHALL carry `gauntlet_verdict` =
`hold`, `gauntlet_gap` = the redacted gap, and a `findings` entry with axis
`gauntlet`, severity `error`, summary = the redacted gap, and disposition
`hold`.

#### Scenario: HOLD records a blocking finding carrying the gap

- **WHEN** a beta attempt ends with
  `VERDICT: HOLD: the empty-input scenario has no test`
- **THEN** the review phase is `failed`, `gauntlet_gap` is
  `the empty-input scenario has no test`,
  `handoff.HasBlockingFinding(handoff.ReviewFindings(payload))` is true for
  both the phase payload and the recorded review envelope, and when the run
  ends `failed` the bullet's `blocked_reason` contains the gap

### Requirement: A review with no verdict is never a pass

A beta attempt whose output has no verdict line, including beta's
"incomplete" report, SHALL record the review phase `failed` with a reason
saying no verdict was found.

#### Scenario: No verdict is never a pass

- **WHEN** a beta attempt exits 0 and its output contains no `VERDICT:` line
- **THEN** the review phase is `failed` and its error says no verdict was found

### Requirement: The builder never receives the critic's bar or output

No prompt sent to a `gauntlet:alpha` phase SHALL contain the review prompt's
verdict contract or any text from a beta attempt's output other than the
parsed gap.

#### Scenario: A HOLD fix prompt carries only the gap

- **WHEN** the fix prompt is built for a failed review phase whose payload has
  `gauntlet_gap` = `G` and whose `raw_output` contains the marker
  `BETA-REASONING`
- **THEN** the prompt contains `G` and the repo's rendered intent brief, and
  does not contain `BETA-REASONING` or `VERDICT:`

#### Scenario: The build prompt carries no review contract

- **WHEN** the `build` phase prompt is rendered for a repo whose pipeline
  includes `review`
- **THEN** the prompt does not contain `VERDICT:`

### Requirement: A builder handoff is never recorded as a pass

A `gauntlet:alpha` prompt SHALL end with the contract line
`If you hand off, end your output with exactly one line: HANDOFF: <absolute path>`.
A `gauntlet:alpha` attempt that exits 0 having handed off SHALL NOT be
recorded `passed`. It has handed off when one of the last three non-empty
output lines is `HANDOFF: <absolute path>`, or when a file under the gauntlet
handoffs directory, written during the attempt, names the worktree.

#### Scenario: A reported handoff is not a pass

- **WHEN** a `build` attempt as `gauntlet:alpha` exits 0 ending with
  `HANDOFF: /h/x.md`, where that file exists
- **THEN** that attempt's record is not `passed`, and its payload has
  `handoff_path` = `/h/x.md`

#### Scenario: An unreported handoff is still not a pass

- **WHEN** an alpha attempt exits 0 with no `HANDOFF:` line, but a file
  written under the handoffs directory during the attempt contains the
  worktree's path
- **THEN** that attempt's record is not `passed`

#### Scenario: The handoff contract is the last line of every alpha prompt

- **WHEN** a `fix` phase runs as `gauntlet:alpha` in a worktree with
  `.sgt/plan.json`
- **THEN** the prompt's final line is the `HANDOFF:` contract line, after
  the plan progress instructions

#### Scenario: A backticked worktree path in a handoff file is a match

- **WHEN** an alpha attempt in worktree `/f/r1/svc` exits 0 with no
  `HANDOFF:` line, and a file written during the attempt contains
  `` `/f/r1/svc`, branch `x` ``
- **THEN** the attempt is not recorded `passed`

#### Scenario: A sibling worktree's path is not a match

- **WHEN** an alpha attempt in worktree `/f/r1/svc` exits 0 with no
  `HANDOFF:` line, and the only file written during the attempt names
  `/f/r1/svc-api`
- **THEN** the attempt is recorded `passed`

#### Scenario: Another session's handoff file is ignored

- **WHEN** an alpha attempt exits 0 with no `HANDOFF:` line, and the only
  file written to the handoffs directory during the attempt names a
  different worktree
- **THEN** the attempt is recorded `passed`

### Requirement: An operator fix on a HOLD uses only the gap

When an operator-started corrective cycle finds that the repo's last failed
phase is a review with a `hold` verdict, the fix prompt SHALL be the repo's
canonical intent brief followed by the gap. When the previous cycle's `fix`
failed, the next cycle SHALL keep the same basis.

#### Scenario: An operator gate fix that reaches a HOLD uses the gap

- **WHEN** an operator-started fix cycle corrects a gate and the review that
  follows records `VERDICT: HOLD: G`
- **THEN** the next cycle's fix prompt contains `G` and no `raw_output` text
  from the review

#### Scenario: A failed HOLD fix keeps the gap

- **WHEN** an operator-started correction of a HOLD with gap `G` has its
  cycle-1 `fix` phase fail
- **THEN** the cycle-2 fix prompt contains `G` and no output from the failed
  `fix` phase

#### Scenario: A repeated operator fix still uses the gap

- **WHEN** an operator correction of a HOLD with gap `G` was exhausted with
  its last `fix` phase `failed`, and the operator calls `run-fix` again
- **THEN** the new cycle's fix prompt contains `G`, and contains no output
  from any `fix` phase

#### Scenario: The next beta does not see the previous verdict

- **WHEN** an operator fix cycle starts on a run whose bullet is `blocked`
  with a HOLD gap `G`
- **THEN** the bullet is `pending` before cycle 1, and the cycle-1 review
  prompt's brief contains no `Blocked reason` naming `G`

#### Scenario: run-fix refuses a review that produced no verdict

- **WHEN** a run's last failed phase is a review with no verdict, and
  `POST /api/run-fix` is called
- **THEN** the response is 409 with a body containing `no verdict` and
  `resume`, and no `fix` phase is recorded

#### Scenario: run-fix refuses a handed-off build

- **WHEN** a run's last failed phase is a `build` that handed off, and
  `POST /api/run-fix` is called
- **THEN** the response is 409 with a body containing `handed off` and
  `resume`, and no `fix` phase is recorded

#### Scenario: run-fix refuses when only unrun repos remain

- **WHEN** a run failed the guard only because repo `b` has not run, and
  `POST /api/run-fix` is called
- **THEN** the response is 409 with a body naming `b` and `resume`, and no
  `fix` phase is recorded

#### Scenario: Exhaustion on a HOLD names the review

- **WHEN** an operator-started correction's last cycle ends with the review
  recording `VERDICT: HOLD: still missing`
- **THEN** the run ends `failed` with a reason containing the repo name,
  `review still HOLD` and `still missing`

### Requirement: The critic judges against the canonical intent

A `gauntlet:beta` review prompt SHALL contain the run's canonical intent
brief for that repo, as rendered by `Store.RenderIntentBrief`, together with
the run's diff from its base. It SHALL contain no envelope or output from an
earlier phase.

#### Scenario: Committed and new files reach the reviewer

- **WHEN** the `build` phase commits a change to `a.go`, leaves `b.go`
  modified, and creates an untracked `c_test.go`
- **THEN** the review prompt's diff contains all three files and nothing
  under `.sgt/`, and the worktree's index is unchanged

#### Scenario: A beta-written envelope is not committed

- **WHEN** a beta attempt writes `.sgt/envelope.json` and the run's output is
  then committed
- **THEN** the run branch contains no `.sgt/envelope.json` from that attempt

#### Scenario: The prompts directory is not treated as a repo

- **WHEN** a run has a `.prompts` directory beside its worktrees
- **THEN** run progress sampling and the fleet view list no repo named
  `.prompts`

#### Scenario: Role prompts are not left in the worktree

- **WHEN** a HOLD with gap `G` is corrected by an operator fix, the fix runs
  as alpha, the next review runs as beta, and the run's output is committed
- **THEN** no file under the worktree contains the `VERDICT:` contract or
  `G`, and the fix and review envelopes' artifacts name paths outside the
  worktree

#### Scenario: The review prompt carries the intent brief

- **WHEN** a run with an intent reaches its `review` phase with roles on
- **THEN** the review prompt contains the rendered intent brief for that repo
  and the diff, and contains no text from the `build` phase's envelope or
  output

### Requirement: A default pipeline gains a review phase when roles apply

When gauntlet roles apply and a repo's factory lists no pipeline, the
effective pipeline SHALL be the default pipeline followed by `review`. The
dashboard's workflow definition SHALL use the same effective pipeline. A repo
whose factory lists a pipeline SHALL run and display exactly that list.

#### Scenario: Default pipeline with roles

- **WHEN** roles apply and the repo lists no pipeline
- **THEN** the effective pipeline is `plan, build, test, review`

#### Scenario: Default pipeline without roles

- **WHEN** a project sets `gauntlet_roles: false` and the repo lists no
  pipeline
- **THEN** the effective pipeline is `plan, build, test`

#### Scenario: Explicit pipeline is untouched

- **WHEN** roles apply and the repo lists `build, test`
- **THEN** the effective pipeline is `build, test`

#### Scenario: The workflow view matches execution

- **WHEN** roles apply and the repo lists no pipeline
- **THEN** the workflow definition served to the dashboard contains a `review`
  stage

### Requirement: Phase provenance shows the role and plugin version

Every agent phase payload SHALL carry `role` (the `--agent` value, or empty)
and `plugin_version` (the probed gauntlet version, or empty). The dashboard
SHALL show the role and version for a phase that ran as a gauntlet role. A
phase that ran without a role SHALL show an em dash with the text
`no gauntlet role`.

#### Scenario: A role-less phase shows one fallback

- **WHEN** a `plan` phase with no role is rendered
- **THEN** it shows `— no gauntlet role` and no version text

#### Scenario: A HOLD review is not shown as a delivered PR

- **WHEN** the activity view renders a run's review envelope that recorded a
  HOLD
- **THEN** it does not use the pull-request icon for that envelope

#### Scenario: Gate rows show no harness fields

- **WHEN** a run's phase list is rendered with a gate record and an agent
  record
- **THEN** the gate row shows no harness, role or version text, and none of
  their em-dash fallbacks

#### Scenario: A role-less phase records empty provenance

- **WHEN** a `plan` phase runs with roles on
- **THEN** its payload has `role` = `""` and the dashboard renders
  `— no gauntlet role` for it

#### Scenario: An agent-written envelope still records the harness

- **WHEN** a `build` attempt writes its own `.sgt/envelope.json` without an
  `agent` key
- **THEN** the recorded payload has `agent` = the resolved harness

#### Scenario: The probed version reaches the phase record

- **WHEN** the probe reports gauntlet version `0.1.0` and the dispatched run's
  `build` phase completes
- **THEN** that phase record's payload has `role` = `gauntlet:alpha` and
  `plugin_version` = `0.1.0`
