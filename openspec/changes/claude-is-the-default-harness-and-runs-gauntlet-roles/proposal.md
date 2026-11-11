# Proposal — Claude Code is the default harness and runs gauntlet roles

## Repository

One repository, `sgt` (this one), which owns every part of the change. The
gauntlet plugin (`callmeradical/gauntlet`) is used as installed and is not
changed.

## Why

PRD: `docs/prd-claude-code-default-and-gauntlet-roles.md`. This is change 1
of 2. Change 2 is
`held-reviews-self-correct-and-builder-handoffs-continue` (PRD R10, R11). It
was split out on 2026-11-11 so this part can be gated and built first.

- **The default harness is wrong.** Every project the operator configured
  names `claude`, but a project that names none silently runs `opencode`.
- **A cut-off claude phase is recorded `passed`.** When claude is still
  waiting on background work after 600s, the CLI terminates and exits 0.
  This happened in 8 of 10 runs on 2026-10-02 and is still present. It
  breaks **R2.6**. Making claude the default without fixing it would spread
  the false pass to every run that names no agent.
- **Old gate passes count as current.** Any past pass of a gate counts, even
  after code has changed since. That breaks the AGENTS.md rule never to
  claim a passing gate without evidence, for every harness.
- **Build and review use generic prompts.** The operator's gauntlet roles
  are alpha (test-first builder) and beta (fresh-context critic), and those
  are what the build and review phases should run as.
- **`sgt run` bypasses the shared execution path.** It never commits its
  output, writes `passed` itself, and passes an empty DAG with no work done.

Decisions this builds on:

- **D2:** the workflow is trusted. Roles run only in phases the factory
  configured, and agent phases placed after `test` are not re-verified.
- **D3:** gates stay first.
- **D5:** adds no new interruption. A HOLD blocks through the existing path.
- **D7:** no v1.
- **D8:** the view and the execution share one pipeline.
- **O3:** the preflight sits next to change resolution.
- **R4.6:** provenance.

## What changes

1. **No false pass.** sgt disables claude's own background-wait limit, and
   fails any claude attempt that ends with the CLI's termination notice.
2. **No inherited envelopes.** The runner clears `.sgt/envelope.json` before
   each attempt.
3. **BREAKING (default):** a project or dispatch that names no harness runs
   `claude`. A missing harness fails with a reason naming it. On this
   machine, `smith` and `tycho` are affected.
4. **Fresh verification, for every harness.**
   - A gate or review counts as passed only if it passed after the repo's
     last `build` or `fix`.
   - Work phases count when their newest record passed.
   - No run is recorded `passed` unless every required phase passes by that
     rule.
   - **BREAKING (fix cycles):** after a fix, every gate in that repo
     re-runs, not only the one that failed.
   - **BREAKING (review):** a review with a blocking finding is recorded
     `failed`, for every harness, so resume can no longer skip it.
5. **Gauntlet roles, on by default for claude.** `build` and `fix` run as
   `gauntlet:alpha`, and `review` runs as `gauntlet:beta`. A project opts out
   with `defaults.gauntlet_roles: false`.
6. **Plugin preflight at every entry point.** Dispatch, plan approval,
   resume, run-fix and `sgt run` are refused before any write if claude
   doesn't report an enabled gauntlet plugin. A missing `claude` gets the
   harness message instead.
7. **Beta's verdict.**
   - The verdict is read from beta's full output.
   - A HOLD becomes a failed review with a blocking finding.
   - A missing verdict is never a pass.
   - Beta's prompt carries the canonical intent brief and one output
     contract.
8. **Operator fix on a HOLD.** `run-fix` gives alpha the intent brief and the
   gap, never beta's output, and keeps that basis across a failed fix.
   Automatic correction arrives in change 2.
9. **Builder handoff is never a pass.** sgt detects alpha's handoff and
   records it `failed`, naming the handoff. Change 2 adds successors.
10. **Review in default pipelines.** When roles apply, a repo with no listed
    pipeline gets `review` after `test`. A repo with an explicit pipeline is
    untouched.
11. **BREAKING (`sgt run`):** it runs through the same execution path as a
    dispatch. It now commits its output, records delivery, applies the
    preflight, the roles and the final check, refuses a project with no DAG
    stages, and exits on the stored status.
12. **Provenance on screen.** Each phase records its `role` and
    `plugin_version`, and the dashboard shows them.
13. **Docs.** Updated for all of the above.

### Relationship to `a-green-bullet-awaits-independent-review`

That change is implemented but not archived, and its findings payload is
reused unchanged. It says a pipeline that doesn't configure review is
unaffected. That still holds with roles off. With roles on, a *default*
pipeline gets review. An explicit pipeline is untouched either way.

## Capabilities

- **agent-harness** (new): the default harness, the false-pass fix, the
  missing-harness reason, and no inherited envelopes.
- **gauntlet-roles** (new):
  - role selection and the opt-out;
  - the preflight;
  - the verdict and the critic's bar;
  - the operator HOLD fix;
  - the rule that a handoff is never a pass;
  - review in default pipelines;
  - provenance.
- **verification-freshness** (new): the freshness rule and the terminal
  guard.
- **cli-factory-run** (new): `sgt run` uses the shared execution path.

## Impact

- **`internal/runner`:**
  - default harness, `PATH` check and claude environment;
  - notice detection and clearing envelopes;
  - roles, provenance, verdict parsing and handoff detection.
- **`internal/config`:** the `GauntletRoles` field and its resolver.
- **`internal/dag`:**
  - `PhasePassed`, `RunVerified`, and role-aware `PipelineFor`;
  - new `Engine` fields;
  - `reviewPrompt`.
- **`internal/ui`:**
  - the preflight in four handlers;
  - the guard in `executeRun`;
  - the `runFixCycles` basis, the HOLD prompt and the guard;
  - `RunProjectCLI`;
  - `passedPhaseNames`, the workflow view, and `index.html`.
- **`cmd/sgt/main.go`:** `runProject` calls `ui.RunProjectCLI`.
- **Docs:** `README.md`, `internal/manual/manual.md`,
  `.agents/skills/sgt-setup/SKILL.md`.
- **Out of scope:**
  - automatic HOLD correction and builder successors (change 2);
  - gauntlet's epsilon, delta and gamma;
  - roles for harnesses other than claude;
  - any change to the gauntlet plugin.
