# Tasks — Claude Code is the default harness and runs gauntlet roles

Change 1 of 2. One repository, `sgt`, and one PR on branch
`feat/claude-is-the-default-harness-and-runs-gauntlet-roles`. Every commit
carries `Change-Id: claude-is-the-default-harness-and-runs-gauntlet-roles`.

Every task is test-first. Write the named scenario tests, see them fail, then
implement. Each task is also verified by the repository gate:
`go build ./... && go vet ./internal/... && go test ./internal/... -count=1`.
Tests must not need a real `claude`: use a stub executable on `PATH`, the
`GauntletProbe` seam, and `HOME` and `SGT_FLEET_DIR` set to temp dirs.

## 1. No false pass from a cut-off claude session, and no inherited envelopes (lands before 2)

- [ ] 1.1 `BuildAgentCommand` adds `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`
  and `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, plus `--no-session-persistence`, for
  claude (design decision 1).
  Both are checked against the 2.1.322 binary.
- [ ] 1.2 Fail a claude attempt when one of its last three non-empty output
  lines begins with the termination notice. The fixture uses the CLI's full
  real line.
- [ ] 1.3 Remove `.sgt/envelope.json` before each attempt (design decision
  12).
- [ ] 1.4 Implement the outcome order of design decision 1a in
  `RunAgentPhase`. Rules 1–3 come before the agent envelope is read; for
  rules 4–6 it is read and redacted first, and kept when rule 6 fails a
  review. Update `TestReviewFindingsRoundTripThroughRunAgentPhase`
  (`runner_test.go:1316`): the findings still persist, and the phase is now
  `failed` with an error. List it in the PR.
- Verify: `specs/agent-harness/spec.md`:
  - "claude's background-wait ceiling is disabled" (2 scenarios);
  - "claude sessions share no auto memory" (2);
  - "A cut-off claude session never records a passed phase" (3);
  - "A phase never adopts another phase's envelope" (1);
  - "Process failure decides an attempt before its output does" (4).

## 2. claude is the default harness

- [ ] 2.1 `runner.DefaultAgent = "claude"` in `BuildAgentCommand` and in
  provenance.
- [ ] 2.2 `exec.LookPath` before each attempt, with the decision 2 reason and
  no retry.
- [ ] 2.3 Update the `config.go:94` comment, and every test that asserted the
  `opencode` fallback.
- Verify: "An unnamed harness resolves to claude" (3) and "A missing harness
  fails with a reason that names it" (1).

## 3. Role selection and the opt-out

- [ ] 3.1 `ProjectDefaults.GauntletRoles *bool` (`gauntlet_roles`) and
  `Project.GauntletRolesApply()`.
- [ ] 3.2 `Engine.GauntletRoles` and `Engine.GauntletVersion`, set only by
  callers. Matching fields on `PhaseRunner`. `roleFor`, and
  `BuildAgentCommandForRole`, with `BuildAgentCommand` kept as a wrapper.
- [ ] 3.3 `annotatePayloadWithProvenance` writes `agent`, `role` and
  `plugin_version` into every payload.
- [ ] 3.4 `PipelineFor(repoCfg, gauntletRoles)`, with both callers updated as
  in decision 9. `buildWorkflowGraph` gains a `roles` parameter.
- Verify: "Claude build, fix and review phases run as gauntlet roles by
  default" (6), and "A default pipeline gains a review phase when roles
  apply" (4). The save scenario extends the
  `TestRefineProjectPreservesUnmanagedConfig` fixture.

## 4. Plugin preflight

- [ ] 4.1 `Server.GauntletProbe` (default: `claude plugin list --json`,
  10s timeout, user or managed scope only, checks for `agents/alpha.md` and
  `agents/beta.md`) and `Server.gauntletPreflight`. A missing `claude` returns
  the harness-missing message, and a failed probe returns its own message.
- [ ] 4.1b A test helper that installs a stub probe on every test server.
  Update fixtures that name no agent, and make refusal tests assert the
  body.
- [ ] 4.2 Call it from `handleDispatch` (skipped for the proposal-only path,
  and for a repeated `request_id`, which otherwise takes today's path
  unchanged), `handleApprovePlan`, `handleRunResume` and `handleRunFix`, at the
  points in the decision 5 table. Thread `preflightResult` through
  `createRunAndDispatch`, `executeRun` and `runFixCycles`.
- Verify:
  - "A dispatch that needs the plugin is refused before anything exists": 5
    HTTP scenarios (dispatch, resume, approval, fix, missing claude); the
    `sgt run` scenario is covered in task 9;
  - "Requests that do not use roles are never probed" (7, including the
    probe-failure, repeated-request, scope and missing-role scenarios);
  - "The probed version reaches the phase record";
  - a unit test parsing a captured `claude plugin list --json` fixture.

## 5. Beta's verdict and review prompt

- [ ] 5.1 `parseVerdict`, tolerant of markdown emphasis and applied to
  untruncated output. Merge the verdict keys into the payload before the
  envelope is saved or recorded. A HOLD or blocking review is not retried.
- [ ] 5.2 `reviewPrompt(diff, brief, stage, repo, roles)`, as in decision 6.
  With roles off it is exactly today's prompt.
- [ ] 5.2b Redefine `DiffAgainstBase` (design decision 6), with the
  256 KiB cap and its marker, and claude prompts over 64 KiB passed on
  stdin. The rest of the redefinition: the merge base
  with `run.BaseBranch`, tracked and untracked changes through a throwaway
  index, `.sgt/` excluded, and the documented fallback. Correct its doc
  comment.
- [ ] 5.2c Write role-phase prompts (`build`, `fix` and `review` with roles
  on) under `<fleet root>/<run>/.prompts/<repo>/`, not `.sgt/`. `PhaseRunner.BaseBranch` is set from the run record.
- [ ] 5.2d For role phases, delete `.sgt/envelope.json` right after reading
  it. `appendRunProgress` and the fleet view skip dot-directories.
- [ ] 5.3 Save `review` envelopes to the router without `raw_output`, and
  never point `envelope_latest.json` at them.
- Verify:
  - "Beta's verdict is read from its complete output" (12);
  - "A HOLD verdict records a failed review carrying the gap" (1);
  - "A review with no verdict is never a pass" (1);
  - "The builder never receives the critic's bar or output" (2);
  - "The critic judges against the canonical intent" (5).

  The existing `reviewPrompt` independence tests pass with `roles=false`.

## 6. Verification freshness and the terminal guard

- [ ] 6.1 `dag.PhasePassed(phases, repo, name, kind, pipeline)` with the
  decision 8 table: name-and-kind lookups, and the pipeline-position rule
  for code-changing records. Use it
  in `Engine.phasePassed` and `Server.passedPhaseNames`.
- [ ] 6.1b `RunAgentPhase` records any `review` phase `failed` when its final
  payload has a blocking finding, for every harness.
- [ ] 6.2 `Engine.FixCycle`, copied into every `PhaseRunner` the engine builds
  (`engine.go:512` and `:751`), and set per cycle by `runFixCycles`.
- [ ] 6.3 `dag.RunVerified(phases, proj, repos, roles)` over the run's
  accountable repos, with gates required only when the pipeline has `test`.
  Call it before `run.delivered` in `executeRun`, and before `passed` in
  `runFixCycles`.
- [ ] 6.3b `srv.accountableRepos(run)`, used by the guard and by
  `handleRunResume` for its repo list and its blocked-bullet reconciliation.
  Add the "in no DAG stage" reason. `handleRunDetails` passes
  `proj.GauntletRolesApply()` to `passedPhaseNames`.
- [ ] 6.3c `runFixCycles` records `run.delivered` on `passed`, and the
  cancelled-attempt record carries `FixCycle`.
- [ ] 6.3d `blockedReasonForRun` uses the decision 8a precedence:
  1. an agent-named reason, unchanged;
  2. current review findings, matched by repo and phase name, which count
     when the repo has no review record at all;
  3. the newest current failed phase's bounded `Error`;
  4. the generic text.

  The existing reason tests in `run_lifecycle_test.go` and
  `wiki_wiring_test.go` pass unchanged.
- [ ] 6.4 Update the existing tests that assert a passed gate is skipped
  after a fix, and list them in the PR.
- Verify: `specs/verification-freshness/spec.md`:
  - "A verification counts only if it passed after the last code change" (13);
  - "A run never ends passed without a passing result for every required
    phase" (9);
  - "The test entry is satisfied by gate records" (1);
  - "A blocked reason says why the phase failed" (6).

  In `TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet`, the
  final status assertion changes from `passed` to `failed` with the "have
  not run yet — resume" reason (design decision 7). Its zero-phases
  assertion stays unchanged.

## 7. Operator fix on a HOLD

- [ ] 7.1 In `runFixCycles`, choose each cycle's basis with the decision 7
  table, add `holdFixPrompt(brief, gap)`, and use the exhaustion reason
  naming the repo and the failure kind.
- [ ] 7.2 `handleRunFix`, after the preflight, clears its repo's `blocked`
  bullet to `pending` before cycle 1. It refuses with 409 when only unrun
  repos remain, when the review produced no verdict, or when the build
  handed off.
- Verify: "An operator fix on a HOLD uses only the gap" (8). The `fix`
  basis looks back past `fix` records in the store. The existing
  gate-fix tests still pass, apart from those updated in 6.4.

## 8. Builder handoff detection and dashboard

- [ ] 8.1 Append the `HANDOFF:` contract to alpha prompts. Detect a handoff
  and record it `failed` with `handoff_path`, with no retry (decision 13).
- [ ] 8.3 The activity view uses the pull-request icon only for
  `run.delivered` envelopes.
- [ ] 8.2 Render `role` and `plugin_version` in the phase row, with em-dash
  fallbacks, then rebuild.
- Verify:
  - "A builder handoff is never recorded as a pass" (6). The contract is
    appended in `RunAgentPhase`, and the path match respects path
    boundaries;
  - "Phase provenance shows the role and plugin version" (6);
  - the dashboard scenarios through the existing node-based UI test pattern
    (`internal/ui/mobile_rail_test.go`), so their result comes from a
    command's exit status;

## 9. `sgt run` through `executeRun`

- [ ] 9.1 `ui.RunProjectCLI(ctx, st, proj, probe)` (decision 10), with the
  in-process `terminalReasons` map. `main.go` passes `nil` for the default
  probe. `runProject` calls it, prints the
  reason, and exits 0 only on `passed`.
- Verify: `specs/cli-factory-run/spec.md`:
  - "sgt run uses the same execution path as a dispatch" (3);
  - "sgt run refuses a project with nothing to run" (1);
  - the `sgt run` scenario in "A dispatch that needs the plugin is refused
    before anything exists".

  Tests call `ui.RunProjectCLI` directly with a temp store.

## 10. Docs and release note

- [ ] 10.1 `README.md` (around line 198), `internal/manual/manual.md` and
  `.agents/skills/sgt-setup/SKILL.md` state that claude is the default and
  describe gauntlet roles, `gauntlet_roles: false`, and installing the
  plugin. They also include a **Supported agents** section (PRD R6), which:
  - lists every name in `runner.SupportedAgents`;
  - marks `claude` as the default and as the only harness that runs gauntlet
    roles;
  - for every harness, lists the behaviour that applies to all runs:
    - every gate in a repo re-runs after a fix;
    - reviews see the run's whole diff;
    - a review with a blocking finding is recorded `failed`;
    - a blocked bullet shows its failed phase's own error;
    - `sgt run` follows the dispatch path.
- [ ] 10.2 Amend the requirement "A repo whose pipeline does not configure
  review is unaffected" in
  `openspec/changes/a-green-bullet-awaits-independent-review/specs/independent-review/spec.md`.
  It should apply when gauntlet roles do not apply, or when the pipeline is
  listed explicitly. Then the two specs can't contradict each other once
  both are archived.
- [ ] 10.3 `mise.toml`'s `[tasks.check]` checks for `claude` first, and
  reports a missing gauntlet plugin as a warning. Update
  `internal/repopolicy/mise_check_test.go`.
- [ ] 10.4 The PR description has a **Behaviour change** section covering:
  - `smith` and `tycho`, with the two pins (`defaults.agent: opencode`,
    `defaults.gauntlet_roles: false`);
  - every gate now re-runs after a fix, for all harnesses;
  - reviews now see the run's whole diff from its base, including committed
    and untracked work, for all harnesses. Before, they saw only uncommitted
    edits;
  - a blocked bullet's fallback reason is now the failed phase's own error,
    for all harnesses, including up to 300 characters of redacted gate
    output for a gate failure;
  - `sgt run` now also writes wiki entries, through `recordTerminalRun`;
  - claude sessions run with auto memory off;
  - a review with a blocking finding is now recorded `failed`, for all
    harnesses;
  - agent phases placed after `test` are trusted and not re-verified;
  - an operator fix that leaves other repos unrun now ends `failed` with
    "resume to continue", not `passed`;
  - the `sgt run` changes: commits, delivery, empty DAG refused, exit code;
  - HOLD auto-correction and handoff successors arrive with change 2.
- Verify: a new `internal/manual` test asserts that the manual contains:
  - the sentence `The default agent harness is claude.`;
  - the `gauntlet_roles: false` opt-out;
  - a `Supported agents` section that names every entry of
    `runner.SupportedAgents`, built from that slice so that adding a harness
    without documenting it fails the test. It fails before the docs change. Also
  `grep -q 'default agent harness is claude' README.md` must exit 0.
  `openspec validate a-green-bullet-awaits-independent-review --strict`
  must pass after 10.2.
