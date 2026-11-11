# Slicing plan: `claude-is-the-default-harness-and-runs-gauntlet-roles` (loop `claude-default`)

The plan has a tracer, then 13 slices in three waves. The code was read at integration head `363311c`. Nothing was written or built.

Most of the change lands in a few shared functions: `RunAgentPhase`, `RunStage`, `runFixCycles`, `handleRunResume` and `executeRun`. To keep merges clean:
- The tracer lays down every signature that crosses slices.
- It leaves zero-value extension points where a later slice fills in behaviour.
- Slices that must edit the same function body are put in a fixed merge order. Where small conflicts are expected, the plan says so.

---

## Shared blocks

### Common builder constraints (include in every builder brief)

- **Where to work.** Repo `/Users/lcromley/Dev/sgt` (Go). Work in your assigned worktree, branched from the integration branch `feat/claude-is-the-default-harness-and-runs-gauntlet-roles` at the head you are given.
- **Binding documents.** `AGENTS.md`, `openspec/config.yaml`, and the change at `openspec/changes/claude-is-the-default-harness-and-runs-gauntlet-roles/`. Design decisions are cited by number. Do not edit that change's artifacts.
- **v1 is off limits.** Never use `bin/sgt-*` or tmux, and never write under `~/.local/share/sergeant/fleet`.
- **Test isolation.** Tests set `SGT_FLEET_DIR`, `SGT_CONFIG` and `HOME` to temp dirs. Any test whose run reaches a terminal status also sets `SGT_WIKI_ROOT` and `SGT_ARTIFACTS_ROOT`.
- **Never invoke the real `claude`.** It is installed on this machine at `/opt/homebrew/bin/claude`.
  - Any test that runs a claude phase puts a stub executable named `claude` first on `PATH`, or names a stub path whose base name is `claude` as the agent.
  - Plugin checks go only through the `Server.GauntletProbe` seam.
- **Test-first.** Write the slice's scenario tests and see them fail for the right reason before implementing. Scenarios marked *pin* already hold today; they still get a test.
- **Merge-friendly tests.** Put new tests in new test files named for the slice. When you modify an existing test, list its name, file and reason in your final report; that list goes into the PR.
- **Fixtures must not depend on slices that haven't merged yet.**
  - Use an explicit `factory.pipeline` unless the test is about the default pipeline.
  - Name the agent explicitly unless the test is about the default harness.
  - Produce failures with a non-zero exit.
  - Stubs that play beta end their output with `VERDICT: PASS` unless the test is about verdicts.
- **Code rules.** Use `writeJSON`, never `json.NewEncoder(w).Encode`. Every enforced-rule test in AGENTS.md keeps passing. Editing `internal/ui/static/index.html` needs `go build ./...` before it is served.
- **Commits.** Every commit carries the trailer `Change-Id: claude-is-the-default-harness-and-runs-gauntlet-roles`.
- **Done means** `go build ./... && go vet ./internal/... && go test ./internal/... -count=1` is green, apart from the known baseline failures:
  - `internal/graphify` and the `internal/mcp` graph tests (no LLM key);
  - `TestRunFixRepeatsAutomaticallyUntilItPasses` and `TestRunFixPromptNamesTheRealFailingGateAndItsOutput` (flaky under the full run; rerun them in isolation).

---

## Tracer: `tracer-roles-e2e`

**Behavior.** A project resolves to claude with roles on, the probe reports gauntlet `0.1.0`, and the project is dispatched. Its `build` phase runs as `claude … --agent gauntlet:alpha`, and the phase record's payload names `agent`, `role: gauntlet:alpha` and `plugin_version: 0.1.0`. If the probe can't be satisfied, the dispatch is refused before any record exists.

**Spec coverage.**
- `gauntlet-roles`:
  - "Claude build, fix and review phases run as gauntlet roles by default" (all 6);
  - "A default pipeline gains a review phase when roles apply" (all 4);
  - from "A dispatch that needs the plugin is refused…": "A dispatch without the plugin creates no run" and "A missing claude names the harness, not the plugin";
  - "Requests that do not use roles are never probed" (all 7);
  - from "Phase provenance…": "The probed version reaches the phase record", "An agent-written envelope still records the harness", and the payload half of "A role-less phase records empty provenance".
- tasks.md:
  - 3.1, 3.2, 3.3, 3.4, 4.1;
  - 4.1b: the helper and the refusal-body rule only;
  - 4.2: the `handleDispatch` call and all the threading only;
  - 6.2;
  - 5.2c: only "`PhaseRunner.BaseBranch` is set from the run record";
  - the 4.x unit test that parses a captured `claude plugin list --json`.

**Contracts it lays down** (later slices build on these exact shapes):
- **runner**
  - `const DefaultAgent = "opencode"`. The value is unchanged here; `claude-default` flips it. `BuildAgentCommand` and provenance use it for an empty name.
  - `PhaseRunner` gains `GauntletRoles bool`, `GauntletVersion string` and `BaseBranch string`.
  - `roleFor(phase string) string`.
  - `BuildAgentCommandForRole(agentCLI, model, role, prompt string) (exe string, args, env []string, stdin string)`. `--agent` is added for claude only, and `stdin` is always `""` in the tracer. `BuildAgentCommand(agentCLI, model, prompt)` stays as a three-value wrapper with role `""`.
  - `RunAgentPhase` sets `cmd.Stdin` when `stdin` is non-empty.
  - `annotatePayloadWithProvenance` also writes `agent`, `role` and `plugin_version`.
- **config:** `ProjectDefaults.GauntletRoles *bool` (`yaml:"gauntlet_roles,omitempty"`) and `(*Project).GauntletRolesApply() bool`.
- **dag**
  - `Engine` gains `GauntletRoles bool`, `GauntletVersion string` and `FixCycle int`.
  - At `engine.go:512` they are copied into the `PhaseRunner`, along with `BaseBranch` from the run record.
  - At `engine.go:751` only `FixCycle` is copied.
  - `PipelineFor(repoCfg config.Repo, gauntletRoles bool) []string`.
- **ui**
  - `type ProbeFunc func(ctx context.Context) ([]byte, error)`. It returns the raw stdout of `claude plugin list --json`, and an error wrapping `exec.ErrNotFound` when `claude` is missing.
  - `Server.GauntletProbe ProbeFunc`. `NewServer` sets the real probe with a 10s timeout.
  - `type preflightResult struct{ Roles bool; Version string }`.
  - `(srv *Server) gauntletPreflight(ctx, proj) (roles bool, version string, err error)`.
  - `createRunAndDispatch(…, pf preflightResult)`, `executeRun(…, pf preflightResult)` and `runFixCycles(…, pf preflightResult)`.
  - `buildWorkflowGraph(projectName, repoName string, repoCfg config.Repo, roles bool)`.
  - **Extension points.** `handleApprovePlan`, `handleRunResume` and `handleRunFix` each get a single `pf := preflightResult{}` statement right after the project loads, and pass it on. That keeps today's behaviour: roles off on those paths. `preflight-entries` replaces each one.

**Files.**

| File | Change |
|---|---|
| `internal/config/config.go` | field and resolver |
| `internal/runner/runner.go` | constant, fields, `roleFor`, `BuildAgentCommandForRole` and wrapper, role and stdin use in `RunAgentPhase`, provenance |
| `internal/dag/engine.go` | Engine fields, both `PhaseRunner` literals, `BaseBranch` population, `PipelineFor` and its call |
| `internal/ui/preflight.go` (new) | the `ProbeFunc` type, default probe, plugin-list parsing, `gauntletPreflight`, `preflightResult` |
| `internal/ui/server.go` | the `GauntletProbe` field and its `NewServer` default |
| `internal/ui/dispatch.go` | the `handleDispatch` call at the decision 5 placement; `createRunAndDispatch` sets the engine fields; `executeRun` gains a param |
| `internal/ui/plans.go`, `internal/ui/run_lifecycle.go`, `internal/ui/gatefix.go` | the placeholders. `runFixCycles` sets the role fields on its `PhaseRunner`, and `GauntletRoles`, `GauntletVersion` and `FixCycle = cycle` on each cycle's engine |
| `internal/ui/workflow.go` | `roles` param; `handleWorkflow` passes `proj.GauntletRolesApply()` |
| tests | One new helper installs a stub probe on every server built in `internal/ui` tests, with no edits to existing `NewServer` call sites. New test files for config, runner, dag, preflight, dispatch and workflow. The `TestRefineProjectPreservesUnmanagedConfig` fixture is extended. Callers of changed signatures in existing tests are updated, including the `annotatePayloadWithProvenance` tests. |

**Depends on:** nothing. It runs alone.

### Builder brief (tracer-roles-e2e)
**Outcome.** Gauntlet roles work end to end for a dispatch:
- A claude project with roles on runs `build` and `fix` as `gauntlet:alpha` and `review` as `gauntlet:beta`. Every other phase runs with no role, and no other harness ever gets one.
- Roles are set only by callers after a successful preflight. The engine never derives them from the project.
- Every agent phase payload records `agent` (the resolved executable), `role`, and `plugin_version`.
  - `plugin_version` is the probed version only when the phase ran with a role; otherwise it is `""`.
  - These keys are also merged into an envelope the agent wrote itself.
- When roles apply and a repo lists no pipeline, the pipeline is `plan, build, test, review`. The workflow view uses the same function.
- `defaults.gauntlet_roles: false` turns roles off and survives a refine save.
- **The preflight follows decision 5 exactly:**
  - user or managed scope only;
  - both `agents/alpha.md` and `agents/beta.md` under `installPath`;
  - the first enabled `gauntlet@*` entry's version;
  - the three error texts verbatim;
  - 400 before any write.
- **`handleDispatch` placement:**
  - the preflight runs after `ValidateAgent` and the agent override, and before change resolution;
  - it is skipped when no repos are named;
  - it is skipped (and nothing else is) when `request_id` already names a run.
- The decision 4 fields are threaded through `createRunAndDispatch`, `executeRun` and `runFixCycles`. Each fix cycle's engine carries its `FixCycle`.
- `PhaseRunner.BaseBranch` is filled from the run record.
- Lay down the contracts listed above exactly. Other slices code against them.

**Environment.**
- Plugin-list fixture:
  - Real 2.1.322 output has `id`, `version`, `scope`, `enabled`, `installPath`, `projectPath` and `projectEnabled`.
  - Capture a sample for the parser test, but replace operator home paths with neutral ones.
  - Probe stubs build a temp `installPath` holding the two role files.
- The default stub (the one every test server gets) reports an enabled user-scope `gauntlet@gauntlet` `0.1.0` with both role files. Refusal tests override it.
- `config` imports no internal package today; `runner` imports `handoff`, `naming`, `redact` and `store`. Keep the import graph acyclic, and keep one `DefaultAgent` literal.
- For "The opt-out restores the previous command", compare against the role-less command `BuildAgentCommand(agent, model, prompt)`, not a hard-coded argument list. `false-pass-order` adds `--no-session-persistence` to every claude command.

**Seams under test.**
- `(*config.Project).GauntletRolesApply`;
- `runner.BuildAgentCommandForRole` and `BuildAgentCommand` (args);
- `PhaseRunner.RunAgentPhase` with a stub `claude` on PATH, checking the recorded phase payload;
- `dag.PipelineFor`;
- `GET /api/workflow`;
- `POST /api/dispatch` with `Server.GauntletProbe` stubbed, checking the response body, the store, the fleet dir, and that the stub records its invocations;
- `POST /api/refine-project`;
- the plugin-list parser against the captured fixture.

Scenarios: those listed under Spec coverage.

---

## Wave 1 (parallel once the tracer has merged)

### S1 `false-pass-order`
**Behavior.** Every agent attempt is decided by decision 1a's fixed order. A claude attempt that was cut off, exited non-zero or timed out is `failed` whatever it printed. Any review with a blocking finding is `failed` and not retried, for every harness. No attempt ever inherits an envelope left by an earlier phase.

**Spec coverage.**
- `agent-harness`:
  - "claude's background-wait ceiling is disabled" (2);
  - "claude sessions share no auto memory" (2);
  - "A cut-off claude session never records a passed phase" (3);
  - "A phase never adopts another phase's envelope" (1);
  - from "Process failure decides…": "A PASS before the termination notice is a failure", "A PASS with a non-zero exit is a failure", and "A failed attempt does not store its own completed envelope" (*pin*).
- `gauntlet-roles`: "A roles-off blocking review keeps its own findings" and "A roles-off blocking review is not retried".
- `verification-freshness`: "A blocking review is not skipped on resume".
- tasks.md: 1.1, 1.2, 1.3, 1.4, 6.1b; from 6.3c, "the cancelled-attempt record carries `FixCycle`".

**Files.**
- `internal/runner/runner.go`:
  - claude case of `BuildAgentCommandForRole`: both env vars and `--no-session-persistence`;
  - `RunAgentPhase`: remove `.sgt/envelope.json` before each attempt; notice detection on the last three non-empty lines; the 1a order (rules 2, 3, 6, 7); envelope read and redaction only after rule 3; rule-6 envelope kept; synthesized `failed` summary; no retry on rule 6; `FixCycle` on the cancelled record.
- New `internal/runner/*_test.go`.
- `internal/runner/runner_test.go`: update `TestReviewFindingsRoundTripThroughRunAgentPhase`.
- New `internal/dag/*_test.go` for the resume scenario.

**Depends on:** tracer. It must merge before `claude-default` (hard constraint).

#### Builder brief (false-pass-order)
**Outcome.**
- `RunAgentPhase` decides each attempt in decision 1a's order:
  - rule 2: cancelled;
  - rule 3: process failure, meaning non-zero exit, budget timeout, or the termination notice;
  - rules 4 and 5: role judgements;
  - rule 6: blocking review;
  - rule 7: passed.
  Rule 1 (missing harness) arrives with `claude-default`.
- Rules 4 and 5 get no behaviour here. Leave exactly one place, after the agent envelope is read and redacted and before rule 6, where a role-specific judgement (alpha's handoff, beta's verdict) is consulted. Later slices plug in there without reordering, so keep their logic out of `RunAgentPhase`'s body.
- **Termination notice.** Claude attempts only. One of the last three non-empty lines must *start with* `Background tasks still running after \d+s; terminating.` (no end anchor). Reason: `agent claude terminated with background tasks still running`.
- **The claude command:**
  - its env carries `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0` and `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, overriding inherited values (last value wins in `cmd.Env`);
  - its args carry `--no-session-persistence`.
- **Rule 6.**
  - Any `review` whose final payload has a blocking finding is recorded `failed`, with error `review reported blocking findings: <summaries>`, and `RunAgentPhase` returns an error.
  - The agent's own envelope and payload are kept, with sgt's provenance keys merged in.
  - It is not retried.
- An attempt failed by rules 1–3 never records an agent-written envelope. One failed by rules 4–6 with no envelope gets summary `Phase <phase> failed for <repo>: <reason>`.
- A cancelled attempt's record carries `FixCycle`.

**Environment.**
- The notice fixture uses the full real line: `Background tasks still running after 600s; terminating. Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely.`
- The inherited-override scenarios set the variable in the test process. The stub `claude` echoes what it sees.
- The resume scenario runs at the engine level with a `goose` stub reviewer: blocking on the first run, clean on resume.

**Seams under test.**
- `runner.BuildAgentCommandForRole` / `BuildAgentCommand` (env and args);
- `PhaseRunner.RunAgentPhase` with stub `claude` and `goose` executables, checking recorded phase status and error, envelope summary and data, attempt count, and the returned error;
- `dag.Engine.RunStage` with `Resume=true`, checking that a fresh review record exists and the stage outcome.

Scenarios: those listed above.

### S2 `preflight-entries`
**Behavior.** Plan approval, resume and run-fix of a roles-on project:
- refuse with the preflight's message before any write when the plugin check fails;
- run with the probed roles and version when it passes;
- never probe when roles don't apply.

**Spec coverage.**
- "A resume without the plugin leaves the run untouched";
- "An approval without the plugin leaves the plan proposed";
- "A fix without the plugin leaves the run untouched";
- the "never probed" requirement as it applies to these three endpoints;
- tasks.md 4.2: the approval, resume and fix placements in the decision 5 table.

**Files.**
- `internal/ui/plans.go`: preflight before `resolveChange`, `UpdateIntentStatus` and any bullet write.
- `internal/ui/run_lifecycle.go`: replace the tracer placeholder; it runs before the reconciliation and `UpdateRunStatus`.
- `internal/ui/gatefix.go` `handleRunFix`: replace the placeholder; it runs before `UpdateRunStatus`.
- New `internal/ui/preflight_entries_test.go`.

**Depends on:** tracer.

#### Builder brief (preflight-entries)
**Outcome.**
- The three handlers each call `gauntletPreflight` immediately after the project loads, at the decision 5 placements, and pass the result on. They do this by replacing the single `pf := preflightResult{}` placeholder the tracer left in each one.
- A refusal is 400 with the preflight's error text. It changes no run status, bullet, intent or phase.
- A pass puts the roles and version on the engine or fix cycle, so the next role phase records `plugin_version`.
- A project whose roles don't apply is never probed on any of these paths.

**Environment.**
- Resume and fix fixtures need a failed run with a `blocked` bullet and its worktree on disk. Follow `gatefix_test.go`'s `gateFixFixture` pattern, but name a stub `claude` as the agent.
- The approval fixture starts from a proposed plan recorded through `POST /api/dispatch` with no repos.

**Seams under test.**
- `POST /api/plans/{intent_id}/approve`, `POST /api/run-resume` and `POST /api/run-fix`, with a stub `Server.GauntletProbe`;
- store reads of run status, bullet status, intent status, phases (including `fix` records) and runs for the intent.

Scenarios: the three listed above, plus a passing resume whose next role phase payload has `plugin_version`.

### S3 `freshness-rule`
**Behavior.** For every harness, a gate or review counts as passed only when its newest record of that name and kind passed after the repo's last code-changing record. Work phases count when their newest record passed. This applies to resume skips, resume's `skipped` list, and run details' `resume_skips`.

**Spec coverage.**
From `verification-freshness`, "A verification counts only if it passed after the last code change":
- "A gate broken by a fix is re-run"
- "A failed fix phase does not let stale gates count"
- "A gate fix re-runs every gate of its repo"
- "An agent phase before test makes earlier gate passes stale"
- "A gate named like a phase is not confused with it"
- "The rule applies to named harnesses"
- "The run details' skip list agrees with resume"
- "Resume's skipped list names only fresh passes"
- "Resume does not re-run passed work phases"
- "Resume after an exhausted correction re-runs stale gates"

tasks.md: 6.1; 6.4; from 6.3b, "`handleRunDetails` passes `proj.GauntletRolesApply()` to `passedPhaseNames`".

**Files.**

| File | Change |
|---|---|
| `internal/dag/freshness.go` (new) | `PhasePassed(phases []store.PhaseRecord, repo, name, kind string, pipeline []string) bool` |
| `internal/dag/engine.go` | `phasePassed` uses it; the four `RunStage` call sites pass kind and pipeline |
| `internal/ui/dispatch.go` | `passedPhaseNames(runID string, proj *config.Project, roles bool) []string` |
| `internal/ui/run_lifecycle.go` | the `"skipped":` line uses `pf.Roles` |
| `internal/ui/server.go` | `handleRunDetails` at line 404 |
| tests | new dag and ui test files; 6.4 updates in `gatefix_test.go` and `engine_test.go` |

**Depends on:** tracer.

#### Builder brief (freshness-rule)
**Outcome.** Implement decision 8's table and the pipeline-position rule as one shared function, `dag.PhasePassed`.
- A record counts only when both its name and its kind match: `agent` for pipeline phases, `code` for gates.
- The code-changing records are every `fix`, plus every non-`review` agent phase that the effective pipeline places *before* the verification. Gates sit at `test`'s position.
- `fix` is never required.

Use it in two places, both with `PipelineFor(repoCfg, roles)`:
- `Engine.phasePassed`, with `e.GauntletRoles`;
- `Server.passedPhaseNames`: resume passes the preflight result, and `handleRunDetails` passes `proj.GauntletRolesApply()`.

Consequence: after a fix, every gate of that repo re-runs, for every harness. Update the existing tests that assert a passed gate is skipped after a fix, and list them.

**Environment.**
- Fix-cycle scenarios use the `goose` stub pattern from `gateFixFixture`, and `FixCycle` per cycle from the tracer.
- Make reviews fail with a non-zero exit, not a blocking finding.
- `RecordPhase` stamps `created_at` at write time in nanoseconds. Seeded scenarios write their records in order.

**Seams under test.**
- `dag.PhasePassed` over seeded `[]store.PhaseRecord` (pipeline-position and name-plus-kind cases);
- `dag.Engine.RunStage` with `Resume` (records written or not);
- `POST /api/run-fix` (new gate records carrying `FixCycle`);
- `POST /api/run-resume` (`skipped`);
- `GET /api/run-details` (`resume_skips`).

Scenarios: those listed above.

### S4 `blocked-reason`
**Behavior.** A failed run's blocked bullets carry the reason the failure itself gives, for every harness, and never raw agent output.

**Spec coverage.** `verification-freshness`, "A blocked reason says why the phase failed" (all 6). tasks.md 6.3d.

**Files.**
- `internal/ui/dispatch.go`: `blockedReasonForRun` only.
- New `internal/ui/blocked_reason_test.go`.

**Depends on:** tracer.

#### Builder brief (blocked-reason)
**Outcome.** Implement decision 8a's order:
1. Walk envelopes in order; the last eligible reason wins.
   - An agent-named `blocked_reason` is always eligible.
   - A review envelope's blocking findings are eligible only while current. They count as before when the repo has no `review` phase record at all.
   - An envelope with no `PhaseID`, such as `run.delivered`, never supplies a review-finding reason.
2. Otherwise, use the newest current failed phase's `Error`, prefixed `<repo>: <phase>: ` and cut to 300 characters. A failed record is current while it is still the newest record of its repo, name and kind.
3. Otherwise, the generic text.

Where 8a's prose and the scenarios disagree, the scenarios decide; see Risks R2. In practice, a review envelope's findings stop being current once a newer review envelope exists for the same repo.

**Environment.**
- Test at the `recordTerminalRun` seam with seeded phase records and envelopes, so this slice needs no other slice.
- Seeded records must use exactly the shapes and `Error` texts the runner writes. Quote design decisions 1, 6 and 13 verbatim:
  - `gauntlet:alpha handed off at <path>; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue`;
  - `gauntlet:beta produced no PASS/HOLD verdict`;
  - `gauntlet:beta HOLD: <gap>`, with the HOLD findings payload;
  - review envelopes `Stage: "review"`, carrying `PhaseID`.
- The existing reason tests in `run_lifecycle_test.go` and `wiki_wiring_test.go` must pass unchanged.

**Seams under test.**
- `Server.recordTerminalRun(runID, "failed")`, then `Store.ListBulletsForIntent` (`BlockedReason`).

Scenarios: the six in "A blocked reason says why the phase failed".

### S5 `sgt-run-shared-path`
**Behavior.** `sgt run <project>` runs through `executeRun`, the same execution path a dispatch uses, and exits by the run's stored status and reason. It refuses a project with no DAG stages without writing anything.

**Spec coverage.**
- `cli-factory-run`: all 4 scenarios.
- `gauntlet-roles`: "sgt run without the plugin creates no run".
- tasks.md 9.1.

**Files.**

| File | Change |
|---|---|
| `internal/ui/runcli.go` (new) | `RunProjectCLI(ctx context.Context, st *store.Store, proj *config.Project, probe ProbeFunc) (status, reason string, err error)` |
| `internal/ui/server.go` | `terminalReasons map[string]string` with its own `sync.Mutex`, initialised in `NewServer` |
| `internal/ui/dispatch.go` | `recordTerminalRunWithReason` stores the reason in that map |
| `cmd/sgt/main.go` | `runProject` calls `RunProjectCLI(…, nil)`, prints the reason, exits 0 only on `passed` |
| tests | new `internal/ui/runcli_test.go` |

**Depends on:** tracer.

#### Builder brief (sgt-run-shared-path)
**Outcome.** Implement decision 10.
- `RunProjectCLI`:
  1. Refuses an empty DAG with `project P has no dag stages; nothing to run (use sgt dispatch to run its repositories)` and creates no run row.
  2. Builds an in-process server with `ui.NewServer(st, 0)`. A non-nil `probe` replaces `GauntletProbe`.
  3. Runs `gauntletPreflight` before the run row; a refusal returns before any write.
  4. Creates the run row as today (no intent, no brief).
  5. Builds the engine with the preflight result and calls `registerRun`.
  6. Calls `executeRun` synchronously with `brief=""`, the DAG stages' repos and `changeDir=""`.
  7. Returns the stored status and the reason recorded with it, read from `terminalReasons`.
- `main.go` has no stage loop and writes no status of its own.
- As a result, `sgt run` now commits output, records `run.delivered` and applies roles.

**Environment.**
- Tests call `ui.RunProjectCLI` directly with a temp store and a stub probe. Never call `main`.
- Use a real git repo, a stub agent and explicit pipelines: `[test]` with a failing gate for the failure scenario, `[build, test]` for commit, delivery and roles. That keeps outcomes stable when `terminal-guard` and `beta-verdict` merge.
- `go test ./cmd/... -count=1` must stay green.

**Seams under test.**
- `ui.RunProjectCLI`, checking the returned status, reason and error;
- the store (runs, the `run.delivered` envelope, the `build` phase payload);
- `git log` on the run branch in the fleet worktree.

Scenarios: the four `cli-factory-run` scenarios, plus "sgt run without the plugin creates no run".

### S6 `dashboard-provenance`
**Behavior.** The phase row shows the harness, role and plugin version for agent phases, with em-dash fallbacks, and nothing of the kind for gates. The activity view gives the pull-request icon only to `run.delivered` envelopes.

**Spec coverage.**
- "A role-less phase shows one fallback";
- "A HOLD review is not shown as a delivered PR";
- "Gate rows show no harness fields";
- the render half of "A role-less phase records empty provenance";
- the display part of the "Phase provenance shows the role and plugin version" requirement;
- tasks.md 8.2 and 8.3.

**Files.**
- `internal/ui/static/index.html`: `phaseDetailHTML` (~line 1326) and `activityHTML` (~line 2412; the `isPR` heuristic at 2417).
- New node-harness tests following the `phase_detail_test.go` / `stage_matrix_test.go` extract-and-run pattern.

**Depends on:** tracer, for payload keys only; tests feed payloads directly.

#### Builder brief (dashboard-provenance)
**Outcome.** Implement decision 11.
- **Agent phases** (`kind === 'agent'`) render `payload.agent`, `payload.role` and `payload.plugin_version` next to the existing model and provider rows:
  - a missing `agent` renders `— harness unknown`;
  - an empty `role` renders `— no gauntlet role` and no version text;
  - a non-empty `role` with an empty version renders `— version unknown`.
- **Gate rows** (`kind === 'code'`) render none of these fields and none of the fallbacks.
- **The activity view** uses the pull-request icon only when `type === 'run.delivered'`. This also drops today's summary-contains-"pr" heuristic.
- Rebuild after editing.

**Environment.** The node harness skips when `node` is absent. It is present here (fnm). Extract the real functions from the embedded file; never reimplement them in Go.

**Seams under test.** `phaseDetailHTML(phase)` and `activityHTML(envelopes)`, executed from the embedded `index.html` under node.

Scenarios: those listed above.

### S7 `docs-and-release`
**Behavior.** The operator docs, the setup skill and `mise run check` say that claude is the default harness and how gauntlet roles work, and the earlier review spec no longer contradicts this one.

**Spec coverage.** tasks.md 10.1, 10.2 and 10.3, with their verifications.

**Files.**
- `README.md` (around line 198), `internal/manual/manual.md`, `.agents/skills/sgt-setup/SKILL.md`;
- `openspec/changes/a-green-bullet-awaits-independent-review/specs/independent-review/spec.md`: amend "A repo whose pipeline does not configure review is unaffected";
- `mise.toml` `[tasks.check]`: `claude` checked first; a missing gauntlet plugin is a warning;
- `internal/repopolicy/mise_check_test.go`;
- a new `internal/manual` test.

**Depends on:** tracer, for wording only.

#### Builder brief (docs-and-release)
**Outcome.**
- The docs state `The default agent harness is claude.` and describe:
  - gauntlet roles (`build`/`fix` as alpha, `review` as beta, review added to default pipelines);
  - the `defaults.gauntlet_roles: false` opt-out;
  - pinning `defaults.agent`;
  - installing the plugin (`/plugin install gauntlet@gauntlet`).
- The independent-review requirement applies when gauntlet roles do not apply, or when the pipeline is listed explicitly.
- `mise run check` looks for `claude` first, and warns, without failing, when claude reports no enabled gauntlet plugin.

**Environment.**
- The new manual test must fail before the docs edit.
- The mise check test drives stubs on `PATH`. A stub `claude` must answer `plugin list --json`.

**Seams under test.**
- the `internal/manual` embedded manual content;
- `README.md` (grep);
- `openspec validate a-green-bullet-awaits-independent-review --strict`;
- the mise check script run under `mise_check_test.go`'s stub harness.

---

## Wave 2

### S8 `claude-default`
**Behavior.** A project or dispatch that names no harness runs `claude`. A harness missing from `PATH` fails the attempt with a reason naming it, without starting a process or retrying.

**Spec coverage.**
- `agent-harness`: "An unnamed harness resolves to claude" (3) and "A missing harness fails with a reason that names it" (1).
- tasks.md 2.1, 2.2, 2.3.
- From 4.1b: update the fixtures that name no agent.

**Files.**
- `internal/runner/runner.go`: `DefaultAgent = "claude"`; `exec.LookPath` before each attempt (decision 1a rule 1, no retry).
- `internal/config/config.go:94`: the comment.
- The tests that asserted the `opencode` fallback.
- `internal/ui/workflow_test.go` `TestWorkflowGraphUsesTheEngineDefaultPipelineWhenNoFactoryIsConfigured`: a bare project now resolves to claude with roles.
- Agentless fixtures:
  - in `internal/ui`, for example `o3` in `dispatch_idempotency_test.go`, `progress_test.go` and `plans_test.go`;
  - cross-package servers in `internal/mcp/dispatch_tools_test.go`, `internal/mcp/dispatch_parity_test.go` and `cmd/sgt/dispatch_cli_test.go`.

**Depends on:** tracer and `false-pass-order`, which must already be merged (hard constraint: task 1 before task 2).

#### Builder brief (claude-default)
**Outcome.**
- An empty agent name resolves to `claude` everywhere: the command, the provenance `agent` value, and `GauntletRolesApply`. `ValidateAgent("")` stays nil.
- Before each attempt the runner checks the executable is on `PATH`. When it isn't, the attempt is recorded `failed` with `agent harness "claude" not found on PATH; install it or set defaults.agent in the project` (naming the actual executable), no process starts, and there is no retry.
- Every fixture that names no agent now resolves to claude with roles on. Fixtures that aren't about the default must not change behaviour: pin them with `defaults.agent` or `gauntlet_roles: false`, as design decision 5's fixture note says.
- Servers built outside `internal/ui` can't use the package's test helper. Set the exported `GauntletProbe` field on them, or pin their projects.

**Environment.**
- Run `go test ./cmd/... -count=1` as well as `./internal/...`.
- Use a tripwire `claude` first on `PATH` to prove that no test reaches the real `/opt/homebrew/bin/claude`.

**Seams under test.**
- `runner.BuildAgentCommand("", …)`: exe and leading args;
- `BuildAgentCommand("opencode", …)`: identical to the base branch;
- `PhaseRunner.RunAgentPhase` with empty `AgentCLI`, checking the payload `agent`;
- `RunAgentPhase` with `PATH` set to a dir without `claude`, checking the record's status and error, and that no process ran.

### S9 `beta-verdict`
**Behavior.** A claude review runs as beta against the canonical intent brief. Its PASS/HOLD verdict, read from its complete output, decides the review record. A HOLD carries the gap as a blocking finding, a missing verdict is never a pass, and beta's reasoning never reaches another worktree.

**Spec coverage.**

From "Beta's verdict is read from its complete output":
- "A verdict after the truncation limit is still read"
- "A PASS with trailing text is read"
- "An emphasised verdict line is read"
- "The roles review prompt has one output contract"
- "A beta-written envelope cannot override the verdict"
- "PASS clears findings beta wrote itself"
- "A review with no verdict is retried"
- "Beta's output does not reach a downstream worktree"
- "The last verdict line wins"

Also:
- "HOLD records a blocking finding carrying the gap"
- "No verdict is never a pass"
- "The build prompt carries no review contract"
- "The review prompt carries the intent brief"
- `agent-harness`: "A judged failure is not summarised as completed"

tasks.md: 5.1, 5.2, 5.3.

**Files.**
- `internal/runner/verdict.go` (new): `parseVerdict(full string) (verdict, gap string, ok bool)`, and the rule-5 judgement plugged into the point `false-pass-order` left.
- `internal/runner/runner.go`:
  - verdict keys merged before `SaveEnvelope`, `RecordEnvelope` and `RecordPhase`;
  - the router copy for `review` without `raw_output`;
  - no retry on HOLD.
- `internal/handoff/envelope.go`: `SaveEnvelope` leaves `envelope_latest.json` alone for `review`.
- `internal/dag/engine.go`:
  - `reviewPrompt(diff, brief string, stage *config.DAGStage, repo string, roles bool)`;
  - the review case renders `brief` from the same source the build used (`RenderIntentBrief(intentID, repo, gates)`, else `stage.Brief`, else the generic prompt).
- `internal/dag/engine_test.go`: callers of `reviewPrompt`, passing `roles=false`.
- New test files.

**Depends on:** tracer and `false-pass-order`. Start it after `freshness-rule` has merged too, to avoid a conflict in `engine.go`'s review case.

#### Builder brief (beta-verdict)
**Outcome.** Implement decision 6's verdict rules.
- **Parsing.** For a `gauntlet:beta` attempt that got past rule 3, parse the full untruncated output from the end.
  - Before matching, strip leading `>`, `-`, `*`, `#`, whitespace and backticks, and any wrapping `**`, `__` or backtick emphasis.
  - Then match `^VERDICT:\s*(PASS|HOLD)\s*(?::\s*(.*))?$`.
- **Outcomes**, per the table:
  - **HOLD with a gap:** `failed`, error `gauntlet:beta HOLD: <gap>`; payload has `gauntlet_verdict: hold`, the redacted `gauntlet_gap`, and one `findings` entry (axis `gauntlet`, severity `error`, summary = gap, disposition `hold`). Not retried.
  - **PASS:** `passed`, `gauntlet_verdict: pass`, `findings: []`.
  - **No verdict, or HOLD with an empty gap:** `failed`, `gauntlet:beta produced no PASS/HOLD verdict`, retried like any other failure.
  - sgt's keys override any same-named keys beta wrote itself.
- **Routing.** For a `review` envelope, the copy saved to the router has no `raw_output`, and `envelope_latest.json` keeps pointing at the last non-review phase. The store record keeps the full redacted payload.
- **Prompt.**
  - With roles on, the review prompt follows decision 6's four points: the brief as the bar, the diff as orientation, full files readable in the worktree, and one closing `VERDICT:` contract. It has no JSON-findings instruction and no "nothing else".
  - With roles off it is exactly today's prompt.
  - It never takes an envelope.

**Environment.**
- Stub `claude` scripts print the verdict lines.
- The downstream scenario needs a two-repo stage, `b` after `a`, so `InjectHandoffToWorktree` runs.
- Use explicit pipelines.

**Seams under test.**
- `parseVerdict`, as a table test for the line forms;
- `PhaseRunner.RunAgentPhase` with `GauntletRoles=true` and a stub claude, checking phase status, error and payload, the stored envelope, the router files, the attempt count and the envelope summary;
- `dag.reviewPrompt`;
- `dag.Engine.RunStage`, with the stub capturing the prompt it received, for the brief and build-prompt scenarios;
- the files under `b`'s `.sgt/handoff/` worktree.

### S10 `review-scope`
**Behavior.**
- The reviewer sees the run's whole change since its base: committed, uncommitted and untracked work, with `.sgt/` excluded, capped and delivered on stdin when large.
- Role-phase prompts and beta's own envelope never land in the worktree or on the run branch.
- `.prompts` is never treated as a repo.

**Spec coverage.**
- "A long diff is capped and still delivered";
- "Committed and new files reach the reviewer";
- "A beta-written envelope is not committed";
- "The prompts directory is not treated as a repo";
- tasks.md 5.2b, 5.2c (prompt location; `BaseBranch` was done in the tracer), and 5.2d.

**Files.**
- `internal/runner/runner.go`:
  - `DiffAgainstBase` redefined, and its doc comment corrected;
  - the claude case of `BuildAgentCommandForRole` returns `stdin` for prompts over 64 KiB;
  - in `RunAgentPhase`, role-phase prompts are written to `<fleet root>/<run>/.prompts/<repo>/prompt_<phase>_<attempt>.txt`, with `Artifacts` naming that path;
  - role phases delete `.sgt/envelope.json` right after reading it.
- `internal/dag/engine.go` review case: the diff cap and its marker, the names of files that were cut, and the "diff may be incomplete" note when `BaseBranch` is empty.
- `internal/ui/dispatch.go` `appendRunProgress` and `internal/ui/fleet.go` handleFleet: skip dot-directories.
- New test files.

**Depends on:** tracer and `false-pass-order`. Merge it after `beta-verdict`; small conflicts are expected (see below).

#### Builder brief (review-scope)
**Outcome.** Implement decision 6's diff, size and prompt-location rules.
- **The diff:**
  - base is `git merge-base <BaseBranch> HEAD`;
  - tracked changes come from `git diff <base>`;
  - untracked files are added through a throwaway `GIT_INDEX_FILE` copy with `git add --intent-to-add --all`, so the real index is never touched;
  - `.sgt` is excluded (`-- . ':(exclude).sgt'`);
  - with an empty `BaseBranch`, it falls back to `git diff HEAD` plus untracked files.
- **Caps:**
  - claude: 256 KiB of diff in the prompt;
  - other harnesses: the whole prompt under 120 KiB, with the diff up to 96 KiB;
  - the marker is `[diff truncated at <cap>; read the remaining files in the worktree]`, and the cut files are named;
  - any claude prompt over 64 KiB goes on stdin, not as an argument.
- `BuildAgentCommand` must never silently drop a prompt (see R8).
- **Role phases** (`build`, `fix` and `review` with roles on):
  - prompts live under `.prompts` beside the worktrees; role-less phases keep `.sgt/prompt_<phase>.txt`;
  - a role phase's `.sgt/envelope.json` is deleted right after it is read.
- Progress sampling and the fleet view skip any directory whose name starts with `.`.

**Environment.**
- `runner` cannot import `dag` (`dag` imports `runner`). Derive the run directory from the worktree path, or carry it on a field.
- `TestDiffAgainstBaseReturnsUncommittedChanges` builds a `PhaseRunner` with no `BaseBranch` and must keep passing.
- The 300 KiB scenario must show the process actually started and read its prompt from stdin: the stub echoes stdin's length or a marker.

**Seams under test.**
- `PhaseRunner.DiffAgainstBase` against a real git repo with a committed change to `a.go`, a modification to `b.go` and an untracked `c_test.go`, checking `git diff --cached` and the index hash before and after;
- `BuildAgentCommandForRole` (stdin);
- `dag.Engine.RunStage` review case (prompt and marker);
- `dag.CommitRunOutput`, then `git ls-tree` on the run branch;
- `Server.appendRunProgress` (changes recorded) and `GET /api/fleet` (repos listed).

### S11 `handoff-detect`
**Behavior.** A builder (alpha) attempt that handed off is never recorded `passed`. It is `failed`, naming the handoff, with `handoff_path` set, and is not retried.

**Spec coverage.** "A builder handoff is never recorded as a pass" (all 6). tasks.md 8.1.

**Files.**
- `internal/runner/handoff_detect.go` (new): the rule-4 judgement at the point `false-pass-order` left.
- `internal/runner/runner.go`: append the contract line last to every `gauntlet:alpha` prompt, after `planPromptExtension`; capture the attempt's start and end times; no retry.
- New test file.

**Depends on:** tracer and `false-pass-order`. Merge it after `review-scope`; a small conflict is expected in the prompt-assembly lines.

#### Builder brief (handoff-detect)
**Outcome.** Implement decision 13.
- The contract line `If you hand off, end your output with exactly one line: HANDOFF: <absolute path>` is the final line of every alpha prompt, whoever built that prompt.
- After an alpha attempt exits 0, it has handed off when either:
  - one of the last three non-empty lines of the untruncated output matches `^HANDOFF:\s*(/\S+)$`; or
  - a file under `~/.local/state/gauntlet/handoffs/`, modified during the attempt, names this worktree's absolute path as a whole path. The match is rejected only when the next character is a path-name character `[A-Za-z0-9_-]`, or a `.` followed by one. The most recently modified match wins.
- **Recording:**
  - status `failed`;
  - payload `handoff_path`;
  - reason `gauntlet:alpha handed off at <path>; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue`;
  - no retry;
  - the summary is never "completed".

**Environment.**
- Resolve the handoffs directory from `HOME` at call time; tests set `HOME` to a temp dir.
- No `handed_off` status and no successor; those belong to change 2.

**Seams under test.** `PhaseRunner.RunAgentPhase` for `build` and `fix` with `GauntletRoles=true`, a stub claude, and files written into the temp handoffs dir. Check the record's status and payload, the attempt count, and the prompt the stub received (its final line).

### S12 `terminal-guard`
**Behavior.** No run ends `passed`, by dispatch, resume, operator fix or `sgt run`, unless every required phase of every repo it is accountable for passes by the freshness rule. Resume re-runs repos that never ran, and a passed fix records delivery.

**Spec coverage.**
- "A run never ends passed without a passing result for every required phase" (all 9).
- "The test entry is satisfied by gate records" (1).
- From the freshness requirement: "A review before test does not make gates stale" and "An agent phase after test is trusted".
- tasks.md: 6.3; 6.3b (`accountableRepos`, its use in resume for the repo list and the reconciliation, and the "in no DAG stage" reason); from 6.3c, `runFixCycles` recording `run.delivered` on `passed`.

**Files.**

| File | Change |
|---|---|
| `internal/dag/freshness.go` | `RunVerified(phases []store.PhaseRecord, proj *config.Project, repos []string, roles bool) (ok bool, reason string)` |
| `internal/ui/accountable.go` (new) | `(srv *Server) accountableRepos(run *store.RunRecord) []string` |
| `internal/ui/dispatch.go` `executeRun` | the guard after the last stage and before `run.delivered` |
| `internal/ui/gatefix.go` `runFixCycles` | the guard before `passed`; delivery through `describeDelivery`; the "have not run yet" reason |
| `internal/ui/run_lifecycle.go` | `repos :=` and the reconciliation set use `accountableRepos` |
| `internal/ui/gatefix_test.go` | `TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet`: final status `failed` with the "have not run yet — resume" reason; its zero-phases assertion stays |
| tests | new test files |

**Depends on:** tracer and `freshness-rule`.

#### Builder brief (terminal-guard)
**Outcome.** Implement decision 8's guard and decision 7's fix-path endings.
- **Accountable repos:**
  1. the intent's bullet repos, in `Position` order;
  2. otherwise, the project's DAG-stage repos;
  3. otherwise, `reposForRun`.
- **What is required.** For each accountable repo, every pipeline phase from `PipelineFor(repoCfg, roles)`, and the configured gates when the pipeline has `test`, must pass by `PhasePassed`.
  - `fix` is exempt.
  - `test` is satisfied by the gate records, or by the placeholder `test` gate record when no gates are configured.
- **Reasons, verbatim:**
  - `repo <name> has not run`;
  - `repo <name> has a bullet but is in no stage of this project's DAG; it cannot run`, which never says to resume;
  - `phase <name> on <repo> has no passing result since the last code change`;
  - `phase <name> on <repo> did not pass`;
  - plus `(start a corrective cycle with POST /api/run-fix)` when the repo's newest `fix` failed.
- **In `executeRun`.** A guard failure records the run `failed` with its reason, and no `run.delivered` envelope.
- **In `runFixCycles`:**
  - a passing stage leads to the guard over every accountable repo;
  - when other accountable repos have no records, the run is `failed` with `corrected <repo>; <b, c> have not run yet — resume the run to continue`, and no bullet turns green;
  - otherwise `run.delivered` is recorded through the same `describeDelivery` call `executeRun` uses, then `passed`.
- **Resume** passes `accountableRepos` to `executeRun` and uses the same set for its blocked-bullet reconciliation.

**Environment.**
- `dispatch.go`'s `stageRunner` interface lets an `executeRun` test drive stages with a fake that writes chosen phase records. Use it for "A stale gate fails the run" and "A guard failure records no delivery", which the real engine doesn't produce on its own.
- Beta stubs end with `VERDICT: PASS`.

**Seams under test.**
- `dag.RunVerified` over seeded records;
- `Server.executeRun` with a fake `stageRunner`, checking run status, reason and envelopes;
- `POST /api/dispatch`, `POST /api/run-fix` and `POST /api/run-resume` end to end with git repos and stub agents, checking run status, bullet statuses, phase records per repo, and `run.delivered`.

---

## Wave 3

### S13 `hold-fix`
**Behavior.** An operator `run-fix` on a HOLD gives alpha only the canonical brief and the gap, keeps that basis across failed fixes and repeated calls, names the HOLD when it runs out of budget, and refuses with 409 when there is nothing a fix can correct.

**Spec coverage.**
- "An operator fix on a HOLD uses only the gap" (all 8).
- "A HOLD fix prompt carries only the gap".
- "Role prompts are not left in the worktree".
- tasks.md 7.1 and 7.2.

**Files.**
- `internal/ui/gatefix.go`:
  - `runFixCycles` chooses its basis per cycle using the decision 7 table, looking back past `fix` records in the store;
  - `holdFixPrompt(brief, gap string) string`;
  - the exhaustion reason `corrective fix budget exhausted (N/N attempts) on <repo>; review still HOLD: <gap>`, or `…; gate still failing`;
  - `handleRunFix`, after the preflight, clears its repo's `blocked` bullet to `pending` before cycle 1, and has three 409 refusals.
- New `internal/ui/holdfix_test.go`.

**Depends on:** `preflight-entries`, `beta-verdict`, `review-scope`, `handoff-detect` and `terminal-guard`, all merged.

#### Builder brief (hold-fix)
**Outcome.** Implement decision 7.
- **Basis per cycle**, from the repo's last failed phase:
  - a `review` with `gauntlet_verdict == "hold"`: the prompt is `holdFixPrompt`, which is the canonical brief (or the stage `Brief`, or the generic prompt, for a run with no intent), then a section naming the gap as the task. It has no review output and no verdict contract;
  - a `fix`: look back past every `fix` record to the newest failed non-`fix` phase and use its basis. That works within one correction and across a repeated `run-fix`;
  - anything else: today's `fixPrompt`.
- A review failure never puts `raw_output` in a prompt. A review with no gap uses its sgt-authored `Error`.
- **`handleRunFix`**, after the preflight and before `UpdateRunStatus`:
  - clears the repo's `blocked` bullet to `pending`;
  - refuses with 409:
    - `nothing to fix in <repo>; <b, c> have not run yet — resume the run`, when that repo already passes `dag.RunVerified` on its own;
    - `the review on <repo> produced no verdict; resume the run to review again`, when the last failed phase is a review with no `gauntlet_verdict` (process-level failures included);
    - `the build on <repo> handed off; resume the run to continue`, when it is a build with `handoff_path`.

**Environment.**
- Use stub `claude` scripts that branch on the `--agent` value: beta prints `VERDICT: HOLD: G` with `BETA-REASONING` in its output; alpha records its prompt from argv or stdin into a file outside the worktree.
- `fix_retries` should be small.

**Seams under test.**
- `POST /api/run-fix`: status codes and bodies, `fix` phase records, run status and reason, bullet status before cycle 1;
- the prompts the stub alpha received in each cycle;
- the review prompt the stub beta received, checking its brief has no `Blocked reason` naming `G`;
- `holdFixPrompt`;
- worktree file contents after `CommitRunOutput`;
- fix and review envelope `Artifacts`.

---

## Parallel plan

**Waves.**
- **Wave 0:** `tracer-roles-e2e`, alone.
- **Wave 1** (all start once the tracer has merged): `false-pass-order`, `preflight-entries`, `freshness-rule`, `blocked-reason`, `sgt-run-shared-path`, `dashboard-provenance`, `docs-and-release`.
- **Wave 2:**
  - `claude-default` starts after `false-pass-order` merges;
  - `terminal-guard` starts after `freshness-rule` merges;
  - `beta-verdict`, `review-scope` and `handoff-detect` start after both `false-pass-order` and `freshness-rule` merge. They build in parallel and merge one at a time, each rebased on the head.
- **Wave 3:** `hold-fix`.

**File overlap (by function).** Function-level collisions after the tracer:

| File | Slices (function) | Expected conflict |
|---|---|---|
| `internal/runner/runner.go` | T (fields, `roleFor`, command, provenance); S1 (`RunAgentPhase` order, claude env and flag); S8 (`LookPath` at the top of the attempt, `DefaultAgent`); S9 (verdict merge before save, review router copy); S10 (`DiffAgainstBase`, stdin in the claude case, prompt location, delete after read); S11 (alpha contract after the plan extension) | All serialized behind S1. S9/S10 touch adjacent lines in the envelope-read region; S10/S11 touch adjacent prompt-assembly lines. Small; keep both edits. |
| `internal/runner/verdict.go`, `handoff_detect.go` (new) | S9 and S11 respectively | none |
| `internal/dag/engine.go` | T (fields, literals, `PipelineFor`); S3 (`phasePassed` and its 4 call sites); S9 (review case: brief and `reviewPrompt` call); S10 (review case: cap and marker) | S3 merges before S9 and S10. S9/S10 are adjacent in the review case. Small. |
| `internal/dag/freshness.go` | S3 creates it; S12 adds `RunVerified` | serial (S12 depends on S3) |
| `internal/ui/dispatch.go` | T (`handleDispatch`, signatures); S3 (`passedPhaseNames`); S4 (`blockedReasonForRun`); S5 (`recordTerminalRunWithReason`); S10 (`appendRunProgress`); S12 (`executeRun` body) | different functions; clean |
| `internal/ui/gatefix.go` | T (signature, role fields, `FixCycle`, placeholder); S2 (`handleRunFix` placeholder); S12 (`runFixCycles` pass path); S13 (`runFixCycles` basis and exhaustion, `handleRunFix` 409s and clear) | S13 last, so it merges cleanly after S2 and S12 |
| `internal/ui/run_lifecycle.go` | T (placeholder after `LoadProject`); S2 (that placeholder); S3 (`"skipped":` line); S12 (`repos :=` and reconciliation) | separate hunks, separated by comment lines; clean |
| `internal/ui/server.go` | T (`GauntletProbe` and `NewServer`); S3 (`handleRunDetails`); S5 (`terminalReasons` field and `NewServer`) | S5 after T; clean |
| `internal/ui/plans.go` | T (pass `pf`); S2 (preflight) | serial |
| `internal/ui/workflow.go` | T only | — |
| `internal/ui/fleet.go` | S10 only | — |
| `internal/ui/static/index.html` | S6 only | — |
| `internal/handoff/envelope.go` | S9 only | — |
| `internal/config/config.go` | T (field); S8 (comment) | clean |
| `cmd/sgt/main.go` | S5 only | — |
| `internal/ui/gatefix_test.go` | S3 (6.4 updates); S12 (one assertion) | different tests; clean |
| `internal/mcp/*_test.go`, `cmd/sgt/dispatch_cli_test.go` | S8 only | — |
| Docs, `mise.toml`, the other change's spec | S7 only | — |

**Merge order** (one at a time, full checks after each):
1. `tracer-roles-e2e`
2. `false-pass-order` (task 1; unblocks the runner cluster)
3. `freshness-rule`
4. `preflight-entries`
5. `blocked-reason`
6. `sgt-run-shared-path` (also run `go test ./cmd/... -count=1`)
7. `dashboard-provenance` (rebuild)
8. `claude-default` (task 2, after task 1 as required; also run `./cmd/...` and the tripwire)
9. `docs-and-release`
10. `terminal-guard`
11. `beta-verdict`
12. `review-scope` (rebase; resolve review-case and envelope-region conflicts)
13. `handoff-detect` (rebase; resolve prompt-assembly conflict)
14. `hold-fix`

## Integration step

1. **On the integrated head:**
   - `go build ./...`, `go vet ./internal/...`, `go test ./internal/... -count=1`, and `go test ./cmd/... -count=1`;
   - the tripwire-`claude` run, with an empty log;
   - `command -v node`, so the UI tests ran rather than skipped;
   - `openspec validate claude-is-the-default-harness-and-runs-gauntlet-roles --strict`;
   - `openspec validate a-green-bullet-awaits-independent-review --strict`;
   - `grep -q 'default agent harness is claude' README.md`.
   Then check that `internal/store` has no schema change.
2. **Cross-slice seam check.** `blocked-reason` tested against seeded strings. Confirm against real runner output, by one end-to-end run or test, that:
   - a real handoff failure blocks with `<repo>: build:` plus the handoff text;
   - a resumed no-verdict review after a HOLD names the missing verdict and not the gap.
   If the strings drifted, fix in a commit carrying the trailer.
3. **Trailers.** `git log main..HEAD --format='%h %(trailers:key=Change-Id,valueonly)'` shows the Change-Id on every commit.
4. **tasks.md.** Tick 1.1 through 10.3 in `openspec/changes/claude-is-the-default-harness-and-runs-gauntlet-roles/tasks.md` once each one's verification has passed. Tick 10.4 when the PR description is drafted.
5. **Draft the PR description (do not open the PR).** Include:
   - summary;
   - `Change-Id: claude-is-the-default-harness-and-runs-gauntlet-roles`;
   - the **Behaviour change** section with every 10.4 bullet: the `smith`/`tycho` pins (`defaults.agent: opencode`, `defaults.gauntlet_roles: false`); every gate re-runs after a fix; the whole diff from base goes to review; the blocked-reason fallback to the phase error (up to 300 characters of redacted gate output); wiki entries from `sgt run`; auto memory off; blocking review `failed` for all harnesses; post-`test` agent phases trusted; operator fix with unrun repos ends `failed`; the `sgt run` changes (commits, delivery, empty DAG refused, exit code); HOLD auto-correction and successors arrive with change 2;
   - a **Tests deliberately updated** list collected from every slice report: at least `TestReviewFindingsRoundTripThroughRunAgentPhase`, `TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet`, the 6.4 list, the opencode-fallback tests, `TestWorkflowGraphUsesTheEngineDefaultPipelineWhenNoFactoryIsConfigured`, `mise_check_test.go`, the annotate-provenance and `reviewPrompt` call sites, and the fixture pins;
   - the verification commands;
   - the attribution line.

## Risks: spec gaps a builder would otherwise guess at

- **R1. Opt-out contradicts auto-memory.** "The opt-out restores the previous command" asks for args identical to before this change. But `--no-session-persistence` is required on every claude phase. *Resolution:* the opt-out test compares against the role-less command, not a pre-change literal.
- **R2. "Current" review findings contradict a scenario.** Read literally, 8a says findings count while the repo's newest review record is `failed`. That would make a resolved HOLD's gap the reason for a later no-verdict review, which contradicts "A resolved HOLD is not the reason for a later no-verdict review". *Resolution:* the scenarios decide. Findings are current only on the repo's newest review envelope that carries a `PhaseID`, and only while the newest review record is `failed`; legacy data with no review record counts as before.
- **R3. Duplicate tasks.** tasks.md 1.4 already contains rule 6, which overlaps 6.1b. Both go to `false-pass-order`.
- **R4. Split tasks.** Some tasks are split across slices:
  - 4.1b: helper in T, fixtures in S8;
  - 4.2: dispatch and threading in T, other entry points in S2;
  - 5.2c: `BaseBranch` in T, prompt location in S10;
  - 6.3b: the `handleRunDetails` argument in S3, the rest in S12;
  - 6.3c: the cancelled `FixCycle` in S1, delivery in S12.
- **R5. The probe signature isn't pinned by the design.** T pins `ProbeFunc` as returning raw `claude plugin list --json` stdout, so scope and role-file filtering stay testable at the preflight seam.
  - The real 2.1.322 output includes `scope`, `projectPath` and `projectEnabled`. Verified at plan time: `gauntlet@gauntlet` 0.1.0 at user scope, with `agents/alpha.md` and `agents/beta.md` present.
  - The captured fixture must be scrubbed of operator paths.
- **R6. `plugin_version` on role-less phases is ambiguous.** Decided as `""` when `role` is empty.
- **R7. Test isolation across packages.** A real `claude` is on `PATH` here. The internal/ui test helper can't reach `internal/mcp` or `cmd/sgt` servers, and `cmd/sgt` isn't covered by `./internal/...`. S8 owns those fixtures; S5 and S8 also run `./cmd/...`.
- **R8. The three-value wrapper and large prompts.** `BuildAgentCommand` keeps three return values while large claude prompts move to stdin. It must never drop a prompt: either keep the argument form, or ensure no caller passes over 64 KiB, and test it.
- **R9. Import cycles.** `runner` cannot import `dag`, so the `.prompts` path comes from the worktree's parent or a field. A `config` import of `runner` would be cycle-free today but inverts the layering; there must be one `DefaultAgent` literal either way.
- **R10. Scenarios that already pass today.** These are regression tests, not red-first: "A failed attempt does not store its own completed envelope", "Saving the project preserves the opt-out" (the refine path patches only `agent`), "A named harness is unchanged", and `TestDiffAgainstBase*`. Critics should not HOLD them for lacking red evidence.
- **R11. A `HANDOFF:` line naming a missing file.** It is still a handoff, never a pass. Change 1 fails it either way.
- **R12. States the engine can't produce.** "A stale gate fails the run" and "Agent phase before test…" need seeded records or the `stageRunner` fake. This is intended, not test coupling.
- **R13. The guard may fail old tests.** Applying the guard to accountable repos can fail existing tests whose fixture's bullets don't match its DAG or its run repos. Each failure must be listed, not hidden.
- **R14. Known pre-existing divergences, not to be fixed.** Resume, fix and approval rebuild from `proj.Defaults.Agent`. The workflow view shows the saved config, not a dispatch's override. The gate `fixPrompt` embeds raw gate output.
- **R15. The baseline flaky tests sit in the area S3, S12 and S13 change.** Separate regressions from flakes by running them in isolation before calling a HOLD.
- **R16. Change 2 is out of scope.** No automatic HOLD correction, no successor, no `handed_off` status.
