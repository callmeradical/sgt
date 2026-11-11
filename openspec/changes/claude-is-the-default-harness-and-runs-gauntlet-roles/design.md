# Design — Claude Code is the default harness and runs gauntlet roles

This is change 1 of 2 for `docs/prd-claude-code-default-and-gauntlet-roles.md`.
Change 2, `held-reviews-self-correct-and-builder-handoffs-continue`, adds the
automatic HOLD correction (PRD R11) and builder handoff successors (PRD R10).
Until change 2 lands:

- a HOLD fails the run through the existing review path
  (`a-green-bullet-awaits-independent-review`), and the operator corrects it
  with `POST /api/run-fix`;
- a builder handoff is recorded `failed`, never `passed`.

## Context

- `internal/runner/runner.go` `BuildAgentCommand` falls back to `opencode`
  when the agent is empty (`runner.go:412`). The `claude` case builds
  `--print --dangerously-skip-permissions [--model M] <prompt>` and adds no
  environment.
- `RunAgentPhase` judges an attempt only by `cmd.Run()`'s error and its
  deadline. The claude CLI's 600s background-wait termination exits 0.
- The synthesized payload stores `raw_output` through `redact.Truncate`,
  which keeps the **head** of the output. A verdict on beta's last line can
  be cut off.
- `internal/dag/engine.go` `RunStage` already has a `"review"` case. It
  builds `reviewPrompt(diff, stage, repo)` with no envelope parameter, so
  independence is structural. It fails the stage on
  `handoff.HasBlockingFinding`. `PipelineFor` returns `DefaultPipeline()`
  (`plan, build, test`) when a repo lists no pipeline.
- `PhaseRunner.DiffAgainstBase` (`runner.go:394-406`) runs
  `git diff --merge-base HEAD`. That compares against `merge-base(HEAD, HEAD)`,
  which is `HEAD` itself, so it shows only **uncommitted** edits to tracked
  files (`TestDiffAgainstBaseReturnsUncommittedChanges`). Its doc comment
  claims otherwise. Committed work and new untracked files never reach the
  reviewer. The run stores its base branch as `run.BaseBranch`, set in
  `prepareWorktree`.
- `CommitRunOutput` stages with `git add -A` (`engine.go:308`), so `.sgt/`,
  including prompt files, is committed into the branch.
- Claude Code's auto memory is per repository and shared across worktrees,
  and it is loaded into every session (Claude Code memory docs). Every fleet
  worktree comes from `git worktree add` on the operator's repo
  (`engine.go:188-196`). So every alpha, every beta and the operator's own
  sessions share one memory directory. The 2.1.322 binary recognises
  `CLAUDE_CODE_DISABLE_AUTO_MEMORY` (checked in the binary). `--bare` would
  also disable auto memory, but it skips plugin loading too, so it can't be
  used with roles.
- `claude --agent <name>` with an unknown name exits 1 with
  `--agent '<name>' not found` (verified on 2.1.322). Under decision 1a that
  is a process failure.
- `Engine.phasePassed` skips any phase that has *ever* passed when `Resume`
  is set. `RecordPhase` sets `created_at` to the write time, in nanoseconds.
- `internal/ui/gatefix.go` `runFixCycles` is started only by
  `POST /api/run-fix`. Its `fixPrompt` embeds the failing phase's
  `raw_output`, which for a review phase would hand beta's reasoning to
  alpha.
- There are five entry points that start or restart work:
  - `handleDispatch`, which the UI, `sgt dispatch` and MCP `sgt_dispatch`
    all reach over HTTP;
  - `handleApprovePlan`;
  - `handleRunResume`;
  - `handleRunFix`;
  - `sgt run <project>` (`cmd/sgt/main.go:192` `runProject`).

  The first four go through `Server.executeRun` or `runFixCycles`.
  `runProject` builds its own engine, writes its own run row, runs only
  `proj.DAG.Stages`, and writes `passed` directly (`main.go:242`). With no
  DAG it records `passed` having run nothing.
- `claude plugin list --json` returns
  `[{id, version, enabled, installPath, ...}]`. It was verified on claude
  2.1.322, which reports `gauntlet@gauntlet` 0.1.0, enabled.
- `claude --print --agent gauntlet:zeta` was verified to run the plugin role
  headlessly with no `--plugin-dir`.

## Decisions

### 1. Two layers against the false pass: remove the ceiling and detect the notice

`BuildAgentCommand` appends `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0` to the
claude environment. It also appends `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, and
passes `--no-session-persistence` (present in 2.1.322's `--help`), for every
claude phase. Every phase runs in the same worktree, so without the flag
alpha's full transcript would land under
`~/.claude/projects/<encoded worktree>/`, where beta could read it with
Bash. Without it, notes alpha writes would load into beta's
next session, and beta's notes into the next alpha, breaking R8 and R9
through a channel no prompt rule can close. Dispatched sessions would also
write into the operator's own project memory. Both variables are pinned by
unit tests on the literal. The runner builds
`cmd.Env = append(os.Environ(), extraEnv...)`, and `os/exec` keeps the last
value of a duplicated key, so this override wins over an inherited value.

`RunAgentPhase` also fails a claude attempt, with reason
`agent claude terminated with background tasks still running`, when one of
the last three non-empty lines of its output **begins** with the notice. The
regex is `^Background tasks still running after \d+s; terminating\.`, with
no `$`. The real CLI line continues with
` Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely.`, so the
match must not require the line to end there. Matching only at line start,
and only near the end of the output, means an agent that quotes the notice
isn't failed by mistake.

*Alternatives:* the env var alone would silently regress if a CLI version
renames it. Detection alone would keep killing long sessions at 600s, which
contradicts `DefaultAgentTimeout = 0`.

### 1a. One order for deciding an attempt's outcome

`RunAgentPhase` decides each attempt in a fixed order. The first rule that
applies wins:

1. **Harness missing** (`exec.LookPath`): `failed`, and no process starts
   (decision 2).
2. **Cancelled:** `cancelled`, as today.
3. **Process failure:** a non-zero exit, the budget timeout, or the
   termination notice (decision 1). The attempt is `failed`. A verdict,
   handoff or envelope in its output is ignored, so a beta that printed
   `VERDICT: PASS` before the CLI terminated it is `failed`, never `passed`.
4. **Handoff,** for `gauntlet:alpha` (decision 13).
5. **Verdict,** for `gauntlet:beta` (decision 6).
6. **Blocking finding,** for any `review` (decision 6).
7. **Otherwise:** `passed`.

**When the agent's envelope is read.** Rules 1–3 are decided before the
agent's own `.sgt/envelope.json` is read. An attempt failed by any of them
records sgt's failure envelope, never an agent-written "completed" one.

An attempt that gets past rule 3 has its agent envelope read and redacted,
as today (`runner.go:631`), *before* rules 4–6. That matters for rule 6:
with roles off, a review's findings exist only in the envelope the agent
wrote. When rule 6 fails a review, the agent's envelope and payload are
kept as the recorded ones, with sgt's provenance keys merged in. Its
findings therefore reach `blockedReasonForRun`, and the implemented rule
from `a-green-bullet-awaits-independent-review` (a blocking finding's
summary becomes the bullet's blocked reason) keeps holding for every
harness. For rule 5, sgt's verdict keys are merged over the agent's
envelope, as decision 6 says.

**A failed attempt is never summarised as completed.** When an attempt
fails under rules 4–6 and the agent wrote no envelope, the synthesized
summary is `Phase <phase> failed for <repo>: <reason>`. That applies to a
handoff, a HOLD, a missing verdict and a blocking finding alike. It is never
`Phase <phase> completed for <repo>`, which keeps the existing rule at
`runner.go:626-627`. A review attempt failed by a HOLD (rule 5) or a blocking finding (rule 6)
is a judgement, so it is not retried. A review that produced no verdict
(also rule 5) *is* retried, like any other failure. It is not a judgement. Rules 1 and 3 keep
today's retry behaviour, except that rule 1 never retries (decision 2).

### 2. One default constant

Add `runner.DefaultAgent = "claude"`. `BuildAgentCommand` and the provenance
`agent` value use it for an empty name, and `ValidateAgent("")` stays nil.

Before `exec`, `RunAgentPhase` calls `exec.LookPath(exe)`. When it fails, the
runner records a failed attempt with reason
`agent harness "claude" not found on PATH; install it or set defaults.agent in the project`,
starts no process, and skips retries, which cannot change the outcome.

*Alternative:* a `PATH` check at dispatch would refuse every dispatch where
claude is absent, including tests and CI images. Checking per phase keeps
the record. The gauntlet preflight (decision 5) refuses earlier when claude
runs with roles anyway.

### 3. The opt-out is a tri-state pointer

`ProjectDefaults.GauntletRoles *bool` (yaml `gauntlet_roles,omitempty`).
`nil` means on. `Project.GauntletRolesApply() bool` is true when the
resolved agent's base name is `claude` (where the resolved agent is
`Defaults.Agent`, or `DefaultAgent` when that is empty) and the pointer is
nil or true. The refine path patches the YAML node tree, so the key survives
a save.

### 4. Roles are set by callers, after the preflight, and selected by phase name

`Engine` gains `GauntletRoles bool`, `GauntletVersion string` and
`FixCycle int`. `PhaseRunner` gains `GauntletRoles bool` and
`GauntletVersion string`.

- **Who sets the fields.** Only callers set them, and only after a
  successful preflight (decision 5). The engine never derives roles from the
  project itself. A caller that skipped the preflight therefore leaves roles
  off, and that can't produce a phase record naming a role that didn't run.
- **Where they flow.** The engine copies all three into the agent-phase
  `PhaseRunner` it builds (`engine.go:512`). `runFixCycles` sets the two role
  fields on its `PhaseRunner` (`gatefix.go:156`), and sets all three on the
  engine it builds for each cycle. The gate-only runner at `engine.go:751`
  takes only `FixCycle`.
- **Role selection.** `roleFor(phase)` maps `build` and `fix` to
  `gauntlet:alpha`, `review` to `gauntlet:beta`, and anything else to `""`.
- **Command building.** `BuildAgentCommandForRole(agent, model, role, prompt)`
  inserts `--agent <role>` for claude only. `BuildAgentCommand(agent, model,
  prompt)` stays as a `role=""` wrapper.
- **Provenance.** `annotatePayloadWithProvenance` writes `agent` (the
  resolved executable), `role` and `plugin_version` into every payload,
  including one the agent wrote itself, so R14's harness is always recorded.
  For an agent phase, the dashboard renders a missing `agent` as
`— harness unknown`.

*Alternative:* a "you are alpha" preamble in the prompt would duplicate
gauntlet's role definitions inside sgt, and that duplication is the drift the
PRD exists to remove.

### 5. One preflight, called by every entry point

`Server.gauntletPreflight(ctx, proj) (roles bool, version string, err error)`
returns `false, "", nil` without probing when `proj.GauntletRolesApply()` is
false. Otherwise it calls `Server.GauntletProbe`, a seam that by default
runs `claude plugin list --json` with a **10s** timeout. It accepts only
entries whose `scope` is `user` or `managed`. A `project` or `local` install
is keyed to the operator's checkout path, and doesn't apply inside a fleet
worktree. It also checks that `<installPath>/agents/alpha.md` and
`<installPath>/agents/beta.md` exist, so a plugin version without those
roles is refused up front instead of failing every phase. If a role is
still missing at run time, claude exits 1 (see Context), which decision 1a
records as `failed`. A record can never name a role that didn't run. That is below
`sgtclient`'s 30s HTTP timeout (`sgtclient.go:37`), so a slow probe can't
time out the caller while the server goes on to create the run. It takes
the version of the first entry whose `id` starts with `gauntlet@` and whose
`enabled` is true. Errors:

- **`claude` not found** (`exec.ErrNotFound`): the harness-missing message
  from decision 2, naming `claude` and `defaults.agent`.
- **No enabled user- or managed-scope plugin, or one without both roles:**
  `gauntlet roles are on for project P but claude reports no enabled gauntlet plugin (install: /plugin install gauntlet@gauntlet), or set defaults.gauntlet_roles: false`.
- **Probe failed** (timeout, non-zero exit, or output that isn't the
  expected JSON):
  `could not read claude's plugin list for project P: <error>; retry, or set defaults.gauntlet_roles: false`.
  The request is refused before any write, the same as when no plugin is
  found.

**Test fixtures.** Every server a test builds gets a stub `GauntletProbe`
from one test helper. No test can reach the real `claude`. Fixtures that
name no agent (for example `o3` in `dispatch_idempotency_test.go`) either
set `gauntlet_roles: false` or rely on the stub. Every refusal test asserts
the body text, not only the status code.

Each caller runs the preflight immediately after the project loads, before
any write:

| Caller | Placement |
|---|---|
| `handleDispatch` | after `ValidateAgent` and the agent override, and before O3 change resolution; skipped when the request names no repos, because that only records a proposed plan and starts no work, and approval runs its own preflight. A request whose `request_id` already names a run (`GetRunByRequestID`) **skips the preflight and nothing else**. It then goes down exactly today's path (`changeRepo`, `resolveChange`, and `createRunAndDispatch` hitting `ErrDuplicateRequestID`, then `respondWithExistingRun`), so its response is built the same way as before and matches the original (D10, `TestARepeatedDispatchIsIndistinguishableFromTheOriginal`). A plugin check that has started failing since can't refuse a repeat. Skipping the preflight is safe because a repeat creates no run, worktree or phase |
| `handleApprovePlan` | before `resolveChange`, `UpdateIntentStatus` and any bullet write |
| `handleRunResume` | before the blocked-bullet reconciliation and `UpdateRunStatus` |
| `handleRunFix` | before `UpdateRunStatus` and before clearing the bullet from `blocked` |
| `sgt run` (decision 10) | before the run row is created |

The results are carried as one `preflightResult{Roles, Version}` value. It
is passed into `createRunAndDispatch`, which gains that parameter and builds
the engine, and into `executeRun` and `runFixCycles`, and it lands on the
engine (decision 4). The version is not stored as a
run column, so no schema change is needed: the phase payload is the record.

### 6. The verdict is parsed in the runner, from untruncated output

When the attempt's role is `gauntlet:beta`, `RunAgentPhase` calls
`parseVerdict(full string) (verdict, gap string, ok bool)` on the full
`outBuf`. It scans lines from the end. Before matching, it strips leading
`>`, `-`, `*`, `#`, whitespace and backticks, and any wrapping `**`, `__` or
backtick emphasis. It then matches
`^VERDICT:\s*(PASS|HOLD)\s*(?::\s*(.*))?$`, case-sensitive on `VERDICT`.

| Verdict | Phase status | Error | Payload |
|---|---|---|---|
| HOLD with a non-empty gap | `failed` | `gauntlet:beta HOLD: <gap>` | `gauntlet_verdict: hold`, `gauntlet_gap`, one-element `findings` (axis `gauntlet`, severity `error`, summary gap, disposition `hold`); the gap passes `redact.Text` first |
| PASS | `passed` | — | `gauntlet_verdict: pass`, and `findings` set to `[]`, because the verdict is the judgement |
| none, including beta's "incomplete", or HOLD with an empty gap | `failed` | `gauntlet:beta produced no PASS/HOLD verdict` | — |

**A blocking finding fails the review, for every harness.** Today the
`"review"` case records the phase `passed` whenever the agent exits 0, and
only fails the *stage* on `handoff.HasBlockingFinding` (`engine.go:545-561`).
A resume then skips that review as passed, and the guard accepts it, so the
run goes green with the finding unresolved. With this change, `RunAgentPhase`
records any `review` phase `failed` when its final payload has a blocking
finding. That covers both cases:

- **Roles off:** the agent's own JSON findings, with error
  `review reported blocking findings: <summaries>`;
- **Roles on:** the HOLD row above.

A review phase record is therefore `passed` only when it carries no blocking
finding.

**Beta's output never reaches another builder.** `Router.SaveEnvelope`
writes `envelope_<stage>.json` and `envelope_latest.json`
(`handoff/envelope.go:97-116`), and `InjectHandoffToWorktree` copies these
into downstream worktrees, where that repo's alpha can read them. For a
`review` envelope, the runner saves a copy to the router **with
`raw_output` removed**, keeping only the verdict, gap and findings. The
router also leaves `envelope_latest.json` alone for `review` envelopes, so
it keeps pointing at the last non-review phase. The store's envelope record
is unaffected and still has the full redacted payload for the operator.

**Order.** The verdict keys are merged into `env.Payload` **before**
`Router.SaveEnvelope` and `Store.RecordEnvelope` (`runner.go:703`), and
before the phase record. `blockedReasonForRun` reads a HOLD's gap from the
review *envelope's* `findings`, so a blocked bullet's reason carries the gap.

sgt's own keys win. If beta wrote `.sgt/envelope.json` itself, through
Bash, the runner still sets `gauntlet_verdict`, `gauntlet_gap` and
`findings` from the parsed verdict, replacing any same-named keys, and the
phase status comes only from the verdict.

A HOLD, or a roles-off review with a blocking finding, is a judgement, not
a flaky attempt, so it is not retried. Otherwise `retries > 0` would re-ask
the reviewer until one attempt passed, and that attempt would then be the
newest record. A missing verdict is retried like any other failure. The
verdict is parsed only for an attempt that has not already failed by
decision 1a, rule 3.

**The diff the reviewer sees is the run's whole change.** `DiffAgainstBase`
is redefined, for every harness, as the change from the run's base to the
working tree:

1. `base := git merge-base <run.BaseBranch> HEAD`. `PhaseRunner` gains a
   `BaseBranch` field. The engine sets it from the run record `RunStage`
   reads (`engine.go:568`), not from `prepareWorktree`. That function
   returns early, before `GetRun`, when the worktree already exists;
2. tracked changes: `git diff <base>`, which covers committed and
   uncommitted work against the working tree;
3. untracked files: added through a throwaway index
   (`GIT_INDEX_FILE` set to a temp copy, then
   `git add --intent-to-add --all`), so the worktree's real index is never
   touched;
4. `.sgt/` is excluded (`-- . ':(exclude).sgt'`).

**Size.** For claude, the diff in the prompt is capped at 256 KiB. For
every other harness, whose prompt is always one command-line argument, it
is capped so that the **whole** prompt (brief, diff and contract) stays
under 120 KiB, below Linux's 128 KiB single-argument limit. The diff gets
whatever room the rest of the prompt leaves, up to 96 KiB. When it is longer,
the cut is marked `[diff truncated at <cap>; read the remaining files in
the worktree]`, where `<cap>` is the cap that applied, and the review prompt
names the files that were cut. For
any claude prompt over 64 KiB, the prompt goes on stdin (`claude --print`
reads stdin when no prompt argument is given), not as one argument.
`BuildAgentCommandForRole` returns a fourth value, `stdin string`, which is
empty when the prompt is an argument. `RunAgentPhase` sets `cmd.Stdin` from
it. `BuildAgentCommand` keeps its three-value signature, and discards
`stdin` only when it is empty. Linux's 128 KiB single-argument limit then can't
stop the process from starting, retry after retry.

When `run.BaseBranch` is empty, for runs recorded before it existed, the
fallback is today's `git diff HEAD` plus the untracked files. The review
prompt then says that the diff may be incomplete. Its doc comment is
corrected. `TestDiffAgainstBaseReturnsUncommittedChanges` keeps passing,
since uncommitted changes are still included, and new tests cover committed
and untracked work. This fixes a pre-existing defect: before it, a review
re-run after `commitAll` saw an empty diff.

**A role phase's own envelope file is consumed.** For a role phase,
`RunAgentPhase` deletes `.sgt/envelope.json` right after reading it. A beta
that wrote one through Bash therefore never has it committed onto the run
branch, where a later alpha could find it in git history (R9).

**Prompts are kept out of repo scans.** The `<fleet root>/<run>/.prompts/`
directory sits beside the repo worktrees. `appendRunProgress`
(`dispatch.go:789-801`) and the fleet view (`fleet.go:93-114`) skip any
directory whose name starts with `.`, and a test covers both. Prompt files
are deleted with the run directory, the same as the worktrees.

**Role prompts are never in the worktree.** For every phase that runs as a
gauntlet role (`build`, `fix` and `review`), `RunAgentPhase` writes the
prompt to `<fleet root>/<run>/.prompts/<repo>/prompt_<phase>_<attempt>.txt`
instead of `.sgt/prompt_<phase>.txt`, and the envelope's `Artifacts` names
that path. `CommitRunOutput` stages the whole worktree, so a prompt left in
`.sgt/` would be committed and readable:

- beta's bar and the `VERDICT:` contract would reach the next alpha (R9);
- an alpha fix prompt, which carries the HOLD gap, would reach the next
  beta (R8).

Role-less phases keep `.sgt/prompt_<phase>.txt`, as today.

**The review prompt.** It becomes
`reviewPrompt(diff, brief string, stage, repo, roles bool)`, and still takes
no envelope. `brief` is `Store.RenderIntentBrief(intentID, repo, gates)`,
the canonical rendering AGENTS.md requires. When the run has no intent
(`sgt run`), it is the same prompt source the build phase used
(`stage.Brief`, or the generic phase prompt), so beta always judges against
what the builder was given (R9). With `roles` true, the prompt follows beta's own rules:

- it drops "judge the diff below, nothing else" and the JSON-findings
  instruction;
- it gives the brief as the bar and the diff as orientation;
- it says the full files may be read in the worktree;
- it asks for beta's PASS/HOLD answer, ending with one `VERDICT:` line.

sgt builds the `findings` payload itself. With `roles` false, the prompt is
exactly today's.

*Alternatives:* parsing `raw_output` in the engine would read truncated text.
Having beta write `envelope.json` is fragile, because beta has no Edit or
Write tool and the envelope branch is skipped for failed attempts.

### 7. HOLD in this change: the existing review path, and an operator fix that respects the bar

A HOLD fails the review phase (decision 6). The existing `"review"` case then
fails the stage, and the run ends `failed`. `AdvanceBulletsForRun` blocks the
run's bullets, which is today's behaviour for any failed run. Nothing new
starts automatically; that is change 2.

`runFixCycles` stays as it is today, with these changes:

- **Each cycle follows the failure it finds.** At the start of a cycle, it
  reads the repo's last failed phase:

  | Last failed phase | Basis and prompt |
  |---|---|
  | `review` with `gauntlet_verdict == "hold"` | basis is the HOLD; `holdFixPrompt(brief, gap)` = the canonical brief (or, for a run with no intent, the stage's `Brief`, or the generic phase prompt, as for `reviewPrompt`), then a section naming the gap as the task, with no review output and no verdict contract |
  | `fix` | look back past every `fix` record for the repo to the newest failed phase that isn't a `fix`, and use that phase's basis. This works both within a correction and when the operator calls `run-fix` again after a correction ran out of budget, because the basis is re-derived from the store, not kept in memory. A HOLD gap is never replaced by a fix's own output |
  | anything else | today's `fixPrompt` |

  A review-phase failure never puts `raw_output` into a prompt. A review with
  no gap uses the phase's sgt-authored `Error` text. Today's gate
  `fixPrompt` remains a pre-existing exception to the canonical-brief rule,
  and this change does not widen it.
- **Exhaustion reason.** It names the repo and the last failure:
  `corrective fix budget exhausted (N/N attempts) on <repo>; review still HOLD: <gap>`,
  or today's `...; gate still failing`.
- **Nothing left to fix.** `handleRunFix` refuses with 409 when the repo of
  the last failed phase already passes `dag.RunVerified` on its own, which
  happens when the run failed only because other repos haven't run. The
  message is `nothing to fix in <repo>; <b, c> have not run yet — resume the run`.
- **A review with no verdict is not something to fix.** When the last failed
  phase is a review with no `gauntlet_verdict`, `handleRunFix` refuses with
  409: `the review on <repo> produced no verdict; resume the run to review again`.
  That covers both a missing verdict line and a reviewer that failed at the
  process level (non-zero exit, timeout, or the termination notice).
- **No earlier verdict in the next brief.** Before cycle 1, `handleRunFix`
  clears its repo's `blocked` bullet to `pending`, the same reconciliation
  resume does (issue #20). `RenderIntentBrief` then can't show the next beta
  the previous HOLD's "Blocked reason".
- **Cycle numbers.** Each cycle's engine sets `FixCycle`, so re-run gates and
  reviews carry their cycle. Today they are left at 0. The cancelled-attempt
  write (`runner.go:597-607`) also records `FixCycle`, so a cancelled
  attempt keeps its cycle.
- **Final check and delivery.** Before recording `passed`, it runs the
  decision 8 guard over every repo the run is accountable for, not only the
  one it fixed. When that passes, it records the `run.delivered` envelope
  through the same `describeDelivery` call `executeRun` uses. Every path to
  `passed` then has the same commit and delivery record (R16a).
- **Other repos that never ran.** The cycle still re-runs only its own
  repo's stage. That is the operator's "just the repo stage" decision,
  pinned by `TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet`, and
  it is kept. But when that stage passes while other accountable repos have
  no records, the run is recorded `failed` with the reason
  `corrected <repo>; <b, c> have not run yet — resume the run to continue`,
  not `passed`. Before this change, `AdvanceBulletsForRun` would have turned
  every bullet green, including ones with no work behind them. Resume then
  runs the remaining repos, and skips the corrected repo's passed phases by
  the freshness rule. That test's final assertion changes from `passed` to
  `failed` with that reason. Its zero-phases-for-`other` assertion, which is
  the actual decision, stays.

### 8. Verification freshness and the terminal guard, for every harness

| Class | Members | Counts as passed when |
|---|---|---|
| Verification | gates (`kind == "code"`) and `review` | the newest record for the repo is `passed` **and** newer than the repo's newest code-changing record that bears on it, of any status |
| Work | `plan`, `build` and custom agent phases | the newest record for the repo is `passed` |
| Correction | `fix` | never required |

**Code-changing records.** For a verification at pipeline position *p*,
the code-changing records that bear on it are:

- every `fix` record;
- every record of an agent phase **other than `review`** that the repo's
  effective pipeline places *before* *p*. In `build, review, test`, `review`
  does not make the gates stale.

Gates sit at the position of `test`.

A factory may put an agent phase *after* `test`, as in `build, test, docs`.
Under D2 the workflow is trusted, so a `docs` edit made after the gates ran
does not make them stale. Without this rule, such a pipeline could never
pass. `review` never counts as a code change, because its prompt forbids
edits. The residual risk is recorded under Risks.

**Lookups match name *and* kind.** A record counts only when both its name
and its `Kind` match: `agent` for pipeline phases, `code` for gates. The
operator's `sgt.yaml` has gates named `build` and `test`, and without the
kind filter the `build` gate record would be judged as the `build` agent
phase.

**Shared rule.** `dag.PhasePassed(phases, repo, name, kind, pipeline)`
implements the table. `pipeline` is the repo's effective pipeline, from
`PipelineFor(repoCfg, roles)`, which the pipeline-position rule needs. Every
caller has it:

- the engine uses `e.GauntletRoles`;
- `passedPhaseNames(runID, proj, roles)` takes the preflight result of the
  resume request it serves;
- `handleRunDetails` (`server.go:404`, for `resume_skips`) runs no preflight,
  so it passes `proj.GauntletRolesApply()`, which is what the next resume's
  preflight will set when it succeeds. The view and the resume agree. Two callers use it:

- `Engine.phasePassed`, for skipping on resume;
- `Server.passedPhaseNames`, for resume's `skipped` list.

Work phases behave as today. Only verification is judged against code
changes.

**Terminal guard.** `dag.RunVerified(phases, proj, repos, roles) (ok bool, reason string)`
requires every pipeline phase and every configured gate to pass by the
table, for each repo the run is **accountable for**.

- `roles` is the preflight result. It resolves the effective pipeline, and
  so whether `review` is required, the same way `PipelineFor` does.
- The accountable repos come from `srv.accountableRepos(run)`:
  1. the repos of the run's intent bullets, in bullet `Position` order;
  2. otherwise, for a run with no intent (`sgt run`, or runs recorded before
     intents existed), the repos of the project's DAG stages;
  3. otherwise, today's `reposForRun` phase-record list.

  Repos that merely have phase records don't define the set, so an unrun
  repo fails the guard with `repo <name> has not run`.
- When an intent bullet's repo appears in no stage of a DAG project, no
  resume can ever run it. The guard then fails with
  `repo <name> has a bullet but is in no stage of this project's DAG; it cannot run`
  and never says to resume. This mismatch exists today. The change makes it
  visible instead of silently green.
- **Resume uses the same set.** `handleRunResume` today derives its repos
  from `reposForRun`, which returns only repos that already have phase
  records. In a non-DAG dispatch over `[a, b]` that failed on `a`, `b` would
  therefore never run again, and the guard would fail every resume. Resume
  now passes `srv.accountableRepos(run)` to `executeRun` instead, so
  `resolveStages` builds its `custom-dispatch` stage over `a` and `b`. `a`'s
  passed phases are skipped by the freshness rule, and `b` runs. The
  blocked-bullet reconciliation in resume uses the same set. That makes
  "resume the run to continue" true.
- **The fix path is unchanged.** `runFixCycles` still uses `reposForRun` only
  to find the stage of the repo it is fixing. Its scope is that repo alone.

`fix` is exempt. The `test` entry is satisfied by the
configured gate records, or by the placeholder `test` gate record when no
gates are configured. The guard runs:

- in `executeRun`, after the last stage and **before** the `run.delivered`
  envelope is recorded. A run that fails the guard therefore never carries a
  delivery record;
- in `runFixCycles` before `recordTerminalRun(..., "passed")`.

On failure the run is `failed`, with reason
`phase <name> on <repo> has no passing result since the last code change`
for verification, or `phase <name> on <repo> did not pass` for work. When
the repo's newest `fix` failed, the reason adds
`(start a corrective cycle with POST /api/run-fix)`.

**Scope.** This applies to every harness. A stale gate pass is false whatever
agent changed the code, and the operator chose this on 2026-11-11. It changes
operator-started gate fixes: after a fix, every gate and the review of that
repo re-run, not only the failed one. The scope is still that repo's own
stage, so the "just the repo stage" decision pinned by
`TestRunFixNeverAdvancesToADownstreamRepoThatHasNotRunYet` stands. Existing
tests that assert a passed gate is skipped after a fix are updated, and the
PR lists them.

### 8a. A blocked bullet's reason is never the generic text when a phase says why

`blockedReasonForRun` (`dispatch.go:701-731`) falls back to
`gates did not pass; no further automatic attempt available` when no
envelope names a reason. Change 1 adds three failure kinds where that text
is false: a builder handoff, a review with no verdict, and the termination
notice. It is also false today for a plain agent failure.

So before the generic text, the fallback now uses the newest **current**
failed phase record's sgt-authored `Error`, prefixed `<repo>: <phase>: ` and
cut to 300 characters. A failed record is current only when it is still
the newest record of its repo, name and kind. A review that HOLDed and was
later passed, or a gate that failed and later passed, is not current. `Error` is the attempt's `failureReason`. The raw agent output
goes only into the returned Go error (`runner.go:758`), never into `Error`,
so it can't leak. The generic text remains only for a failed run that has
no failed phase record.

This applies to every harness, and the PR calls it out.

**Only a current review finding counts.** `blockedReasonForRun` walks
every envelope in the run, and a later passing review never clears an
earlier finding. So a **review** envelope's blocking findings count only
while they are current, which means the newest `review` phase record for
that repo is `failed`.

- **Matching is by repo and phase name, not by `PhaseID`.** `RecordEnvelope`
  stores the attempt-1 id (`runner.go:717`), but later attempts are recorded
  as `<id>-attemptN` (`runner.go:736`).
- **Legacy and test data.** When a repo has no `review` phase record at all,
  its findings count as before. The existing seeded-envelope tests
  (`TestABlockingReviewFindingBlocksTheBulletWithItsSummaryAsReason`,
  `TestMultipleBlockingReviewFindingsProduceOneJoinedReason`) therefore keep
  passing unchanged.
- **Agent-named `blocked_reason`s are unchanged.** The
  `a-stuck-bullet-is-blocked-not-failed` rule stays exactly as it is today:
  the last envelope naming a reason wins, including from a phase that exited
  0 and passed. That is D5(b)'s "blocked on a human decision". Its tests
  (`TestAFailedRunBlocksItsBulletsWithTheAgentReportedReason`,
  `TestABlockedReasonWithASecretIsRedactedOnTheBullet`, and the
  `wiki_wiring_test.go` case) stay unchanged.
- **Envelopes with no phase.** An envelope with no `PhaseID`, such as
  `run.delivered`, which is recorded with `Stage: "review"`, never supplies
  a review-finding reason.

**Order.** As today, `blockedReasonForRun` walks the envelopes in order,
and the last *eligible* reason wins. An agent-named `blocked_reason` is
always eligible. A review finding is eligible only while it is current. The
only change from today is that a stale review finding is skipped. So an
early agent-named reason followed by a current HOLD still yields the HOLD
gap, as it does today. When no envelope reason is eligible, the newest
current failed phase's `Error` is used, and after that the generic text.

**Gate failures.** A gate record's `Error` holds the gate's own redacted
output (`runner.go:320`). So a gate failure's blocked reason becomes up to
300 characters of that output. That output is sgt-run and deterministic,
not agent reasoning, and it says truthfully why the run is blocked. It can
reach a later brief's "Blocked reason". Resume and `run-fix` both clear the
blocked state before the next phase, though, so it never reaches the next
reviewer.

### 9. Effective pipeline is one function

`PipelineFor(repoCfg config.Repo, gauntletRoles bool) []string` appends
`review` to `DefaultPipeline()` when `gauntletRoles` is set and the repo
lists no pipeline. There are two callers:

- `engine.go:523` passes `e.GauntletRoles`;
- `workflow.go:131` passes `proj.GauntletRolesApply()`.

`buildWorkflowGraph` gains a `roles bool` parameter so that caller can pass
the flag.

Execution only happens after a successful preflight, which sets
`e.GauntletRoles = proj.GauntletRolesApply()` from the *effective* project,
including a dispatch's `agent` override. The workflow view shows the
project's saved configuration. The two differ only for a run dispatched with
an agent override that switches roles on or off. That run's drill-down
still shows the phases that actually ran, because they are records, and the
project view never claims to describe that one run. This is noted under
Risks.

### 10. `sgt run` goes through `executeRun`

`runProject` stops owning a stage loop. It calls
`ui.RunProjectCLI(ctx, st, proj, probe ProbeFunc) (status, reason string, err error)`,
where `main.go` passes `nil` for the default `claude plugin list` probe and
tests pass a stub. The function:

1. returns an error without writing anything when `proj.DAG` has no stages:
   `project P has no dag stages; nothing to run (use sgt dispatch to run its repositories)`;
2. builds an in-process `ui.Server` with `ui.NewServer(st, 0)` and runs
   `gauntletPreflight` (decision 5). A refusal returns before any write;
3. creates the run row as today (no intent, no brief), and builds the engine
   with the preflight results;
4. calls `registerRun`, then calls `executeRun` **synchronously**, with
   `brief=""`, the DAG stages' repos and `changeDir=""`. `sgt run` carries
   no OpenSpec change today, and O3 governs dispatch, not this command;
5. reads the run's stored terminal status, and the reason recorded with it.
   `store.RunRecord` has no reason column, so `recordTerminalRunWithReason`
   also keeps the reason it was given in an in-process
   `Server.terminalReasons` map, guarded by its own `sync.Mutex`, because
   run goroutines write it and `RunProjectCLI` reads it. That is the same text it hands to bullets
   and the wiki. `RunProjectCLI` reads it after the synchronous `executeRun`
   returns. No schema change is needed.

`main.go` prints the reason and exits 0 only for `passed`. As a result,
`sgt run` now does all of these:

- commits agent output, so work survives cleanup;
- records `run.delivered`;
- applies roles and the terminal guard;
- in change 2, gets the automatic HOLD correction, with no CLI change.

### 11. Dashboard

The phase row in `internal/ui/static/index.html` (`phaseDetailHTML`)
renders `payload.agent`, `payload.role` and `payload.plugin_version` next
to the existing model provenance, **for agent phases only**. Gate rows have
no harness, and show none of these fields or their fallbacks. For agent
phases:

- an empty role renders `— no gauntlet role`, and nothing about the
  version, since there is no plugin to have a version;
- an empty version under a non-empty role renders `— version unknown`.

The activity view (`index.html:2417`) gives the pull-request icon to every
`stage === 'review'` envelope. It changes to use that icon only for
`type === 'run.delivered'`, so a HOLD review doesn't look like a delivered
PR.

Rebuild after editing (`//go:embed`).

### 12. A phase never inherits another phase's envelope

`RunAgentPhase` removes `.sgt/envelope.json` before each attempt. Otherwise
a review that exits cleanly adopts the build's envelope. By the time the
next attempt starts, the envelope has already been read, routed and stored.

### 13. A builder handoff is detected and failed (interim)

Alpha's protocol (`agents/alpha.md`, "Context handoffs") is to stop at its
context threshold and write a handoff under
`~/.local/state/gauntlet/handoffs/`.

**Contract.** `RunAgentPhase` appends this line to every `gauntlet:alpha`
prompt, after `planPromptExtension`, so it is always last whoever built the
prompt (the engine's default case, `fixPrompt` or `holdFixPrompt`):
`If you hand off, end your output with exactly one line: HANDOFF: <absolute path>`.

**Detection.** After an alpha attempt exits 0, sgt looks for a handoff in
two ways:

- one of the last three non-empty lines of the untruncated output matches
  `^HANDOFF:\s*(/\S+)$`;
- otherwise, a file under the handoffs directory was modified between the
  attempt's start and its exit and contains this worktree's absolute path
  as a whole path. The match counts unless the next character is a path-name
  character (`[A-Za-z0-9_-]`), or is a `.` followed by one. That way
  `/fleet/<run>/svc` doesn't match `/fleet/<run>/svc-api` or `svc.bak`, but
  does match `` `/fleet/<run>/svc` ``, `/fleet/<run>/svc.`, `...svc,`,
  `...svc]`, `...svc:` and `...svc/sub`. The gauntlet handoff skill writes
  markdown, where paths usually sit in backticks or before punctuation.
  If several match, the most recently modified one is used.

**Outcome in this change.** The attempt is recorded `failed`, with payload
`handoff_path` and reason
`gauntlet:alpha handed off at <path>; successors arrive with change held-reviews-self-correct-and-builder-handoffs-continue — resume the run to continue`.
`handleRunFix` refuses with 409 when the last failed phase is a handed-off
build: `the build on <repo> handed off; resume the run to continue`. A fix
there would embed alpha's output and then re-run `build`, so two builders
would run with no basis in the handoff.
It is not retried, because a retry would discard the handed-off state.

**In change 2.** Change 2 replaces this failure with a `handed_off` record
and a successor. The requirement shared by both changes is that a handoff is
never a pass.

## Risks and trade-offs

- **Session fan-out (accepted).** With roles on and review added, one bullet
  in this change runs plan + build + review. Operator fix cycles add up to
  `fix_retries` × (fix + review). Change 2 raises the automatic worst case;
  see its design. The operator accepted the fan-out on 2026-11-11.
- **CLI contract drift.** `--agent`, `claude plugin list --json`, the
  background-wait env var, and the notice text are all claude CLI surfaces.
  Each has a unit test pinned to the literal sgt depends on.
- **Plugin scope.** The probe reports what the claude binary on sgt's `PATH`
  reports, which is the truthful answer for headless runs.
- **Beta mutating the worktree.** Beta has Bash. The prompt forbids
  modification, and any change it makes is committed and visible. Because
  `review` doesn't count as a code change (decision 8), a gate that passed
  before such an edit would still count. This residual risk is accepted.
- **Role prompts are reachable, not given.** Prompts under
  `<fleet root>/<run>/.prompts/` are never in any prompt, and never
  committed. But an agent with Bash can still find them outside its
  worktree (`../.prompts/`). The guarantee is "never given", not "not
  reachable". This is accepted, alongside the beta-mutation risk.
- **Beta's bar omits the OpenSpec change on explicit-repo dispatches
  (pre-existing).** Those dispatches create their intent without a
  `ChangeID` (`dispatch.go:305-310`), so `RenderIntentBrief` doesn't name
  the change whose scenarios beta's rule 3 judges against. Beta can still
  read `openspec/changes/<id>/` in the worktree. Recording the change id on
  every intent is deferred.
- **User-level instruction files are shared.** Every headless claude
  session also loads the operator's `~/.claude/CLAUDE.md`. On this machine
  it tells agents to read and post to a shared chat channel, and alpha.md
  step 4 does the same for handoffs. That channel could carry alpha's
  account of its own work to beta. Turning off all `CLAUDE.md` files would
  also drop the repository's own instructions, which the roles need. So
  this is recorded as an operator-specific risk, not solved: an operator
  who wants strict isolation keeps cross-agent instructions out of the
  user-level file.
- **Override runs and the project view.** A dispatch whose `agent` override
  changes whether roles apply runs a pipeline the project-level workflow
  view doesn't show. The run's own phase records are the truth for that run.
- **Resume re-asks beta on unchanged code.** After a HOLD, the review's
  newest record is `failed`, so a plain resume runs a fresh beta again even
  if no code changed. This is accepted: the operator chose to resume, and a
  fresh critic is the point.
- **Agent phases after `test` are trusted (D2).** Edits made by a phase
  placed after the gates are not re-verified. The PR calls this out.
- **Operator fix outcome changes.** A fix cycle that corrects one repo
  while others in the run never ran now ends `failed`, saying to resume,
  instead of `passed`. The PR calls this out.
- **Resume, fix and plan approval can switch harness (pre-existing).** They
  rebuild from `proj.Defaults.Agent`, not from the agent the dispatch or
  proposal named. The preflight and the
  provenance follow the harness that actually ran, so nothing is recorded
  falsely. Storing the dispatched agent on the run is deferred.
- **`sgt run` behaviour change.** It now commits agent output, records
  delivery, refuses an empty DAG instead of passing it, and exits on the
  stored status. The PR calls each of these out.
- **Named harnesses change in one respect.** Every gate re-runs after a fix,
  for opencode, goose and other harnesses alike (decision 8). The PR calls
  this out.
- **`smith` and `tycho`.** These projects switch to claude with roles and
  review. The pins are `defaults.agent: opencode` and
  `defaults.gauntlet_roles: false`.

## Migration and rollback

No schema or store change. The new config key defaults to on, and the new
payload keys are absent on old rows, which render `— no gauntlet role`.

| Stage | Change | Rollback |
|---|---|---|
| Expand | Ceiling and notice detection, no inherited envelopes (task 1) | revert |
| Switch default | `DefaultAgent = "claude"` (task 2) | per project: `defaults.agent: opencode`; or revert |
| Switch roles | Roles and pipeline, preflight, verdict (tasks 3–5) | per project: `defaults.gauntlet_roles: false`; or revert |
| Truthfulness | Freshness, terminal guard, blocked reasons (task 6; depends on 3–5) | revert together with tasks 3–5; no data to undo |
| Operator HOLD fix, handoff detection, dashboard | tasks 7–8 (depend on 6) | per project: `defaults.gauntlet_roles: false`; or revert |
| CLI parity | `sgt run` through `executeRun` (task 9) | revert |
| Contract | none; nothing is removed | — |

Task 1 lands before task 2, in the same PR (PRD R3).

## Open questions

None that change specs, approach or tasks.

- *Deferred:* recording the dispatched agent on the run.
- *Deferred:* recording `ChangeID` on intents from explicit-repo dispatches.
- *Deferred:* gauntlet roles for other harnesses (gauntlet PRD 003).
- *Deferred:* a successor critic for beta's "incomplete".
- *In change 2:* the automatic HOLD correction and builder successors.
