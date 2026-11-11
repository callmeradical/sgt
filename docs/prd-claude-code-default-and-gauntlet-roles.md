# Product Requirements: Claude Code is the default harness, and its phases run as gauntlet roles

Status: Grilled; all open questions decided 2026-11-11. Awaiting gate and build approval.

Extends: `docs/prd-sgt.md` — D2 ("Trust the workflow, review the inference"),
D3 ("TDD is enforced, not assumed"), D5 ("Three interruptions only"), D7 ("v1
is not a dependency"), R2.6 (a process exit cannot falsely mark a phase as
passed), R4.6 (phase provenance); `docs/prd-independent-review-phase.md`
(the review phase this PRD supplies a reviewer for).

Related, outside this repository: the gauntlet plugin
(`callmeradical/gauntlet`), whose PRD 003 makes its roles client-neutral. This
PRD does not depend on 003 landing.

## Summary

Two linked changes to how Sgt runs a coordinator-driven phase.

1. When a project names no agent harness, Sgt runs Claude Code. Today the
   unnamed default is opencode.
2. When Sgt runs Claude Code, the build phase runs as the gauntlet's builder
   role (alpha) and the review phase runs as the gauntlet's critic role
   (beta), started fresh. Sgt stays the orchestrator and the system of
   record: runs, phases, intents, bullets, gates and findings are Sgt's own
   rows, exactly as today.

The first change is gated on fixing a known false pass in how Sgt runs Claude
Code, because making Claude Code the default would otherwise spread that false
pass to every run that does not name an agent.

## Problem

- **The default harness is not the one the operator uses.** Every project the
  operator has configured to name a harness names Claude Code. A project that
  omits the field gets opencode, which the operator does not run as a
  dispatched harness. The default is a trap, not a convenience.
- **A Claude Code phase can be recorded `passed` with no work done.** When a
  headless Claude Code session is still waiting on background work after ten
  minutes, the CLI terminates it and exits successfully. Sgt records the phase
  `passed`. This was observed in 8 of 10 dispatched runs on 2026-10-02 and is
  still present. It contradicts R2.6 and AGENTS.md's "a failed or timed-out
  agent phase records `failed`, never `passed`." Today it affects only runs
  that opt into Claude Code; after change (1) it would affect every run that
  names no agent.
- **Sgt's build and review prompts are hand-rolled, and the operator already
  has better ones.** The gauntlet plugin's alpha (test-first builder that
  never sees the reviewer's bar) and beta (fresh-context critic that returns
  PASS or HOLD with the single biggest gap) are the roles the operator uses
  interactively. Sgt's review phase exists but is driven by a generic prompt;
  its build phase is a generic "execute build phase" prompt. The same
  discipline is maintained in two places and drifts.

## Goals

- A project that names no harness runs Claude Code, and nothing about that
  run can be recorded `passed` when Claude Code was cut off.
- A Claude Code build phase is performed by the gauntlet builder role, and a
  Claude Code review phase by the gauntlet critic role in a fresh session that
  has never seen the build phase's reasoning.
- Every phase record says which harness and which role ran it, so the
  dashboard can show it from stored state.
- A gauntlet verdict becomes Sgt evidence through Sgt's existing review and
  blocked-bullet paths. No new bullet status and no new interruption type
  (D5).

## Non-goals

- Gauntlet orchestrating Sgt, or Sgt bundling into the gauntlet plugin. The
  operator chose "Sgt uses gauntlet" over "gauntlet ships Sgt" and over both.
- Gauntlet's orchestrator (epsilon), slicer (delta), and progress reporter
  (gamma). Sgt already decomposes intents into bullets (D4), orders merges
  (D6), and is the dashboard (D8). Running gauntlet's equivalents inside a
  phase would create a second, divergent execution model, which AGENTS.md
  names a bug.
- Gauntlet roles for harnesses other than Claude Code. That depends on the
  gauntlet's own client-neutral work (its PRD 003).
- Gauntlet's own loop, worktree and merge-lock state. Sgt owns the worktree
  (internal isolation rule) and the records.
- Changing what an explicitly named harness does, beyond the truthfulness
  rules that apply to every run whatever its harness: R11a, R9a, R11b and
  R11c. A project
  that names `opencode`, `goose`, `codex`, `pi` or `copilot` otherwise runs
  as it does today.
  *Amended 2026-11-11: the operator chose to apply fresh verification to
  every harness, because a stale gate pass is false whichever agent changed
  the code.*

## Users and scenarios

- *"I register a new project and dispatch without setting an agent. It runs
  in Claude Code, which is installed and logged in, not in opencode, which
  isn't."*
- *"A dispatched build starts a background subagent and the CLI gives up on
  it. The phase shows `failed` with the reason, not a green check on an
  empty diff."*
- *"I open a bullet and see build ran as `claude · gauntlet:alpha` and review
  as `claude · gauntlet:beta`, and the review's HOLD names the gap."*
- *"I improve beta's bar in the gauntlet plugin and the next dispatched
  review uses it, with no change to Sgt."*

## Requirements

### R-A — No false pass from a cut-off Claude Code session (prerequisite)

1. A Claude Code phase that the CLI terminates for still-running background
   work is recorded `failed`, with a reason naming that termination, never
   `passed`.
2. Claude Code is not cut off by a CLI-side ceiling shorter than the phase's
   own agent budget. Sgt's configured agent budget, unbounded by default, is
   the only clock.
3. This requirement ships before, or together with, R-B. R-B must not ship
   alone.

### R-B — Claude Code is the default harness

4. A project or dispatch that names no harness runs Claude Code.
5. When Claude Code is not installed where Sgt can run it, the failure names
   the missing harness and how to choose another, rather than an opaque error.
6. Operator-facing docs and setup list Claude Code as the default.

### R-C — Claude Code phases run as gauntlet roles

7. A Claude Code build phase runs as the gauntlet builder role.
8. A Claude Code review phase runs as the gauntlet critic role, in a session
   that starts with no build-phase reasoning — the independence the review
   phase already requires.
9. The builder is never given the critic's bar or rubric. The critic is
   given the same canonical intent the builder worked from, plus the diff,
   so it judges against what was asked rather than its own assumptions.
9a. For every run, whatever its harness, the reviewer sees the run's whole
    change since it began, both committed and uncommitted work, not just
    what happens to be uncommitted.
10. When the builder stops early by its own handoff protocol, Sgt continues
    the work in a fresh builder that starts from that handoff, in the same
    worktree, as its own recorded phase. The number of successive handoffs
    per phase is bounded. Past the bound, or when a handoff can't be found or
    read, the phase is `failed` with a reason naming the handoff, never
    `passed`. A phase that handed off is never itself recorded as passed.
11. A critic PASS is recorded as a non-blocking review outcome. A critic HOLD
    is recorded as a blocking finding whose summary is the HOLD's named gap.
    The HOLD then starts a corrective cycle automatically: a fresh builder
    receives the gap as its next task in the same worktree, the gates run
    again, and a fresh critic reviews again. The cycle is bounded by the same
    corrective budget as a failed gate's fix cycle. When that budget is spent,
    the run fails and its bullets become `blocked` through the existing
    blocked path (D5(b)), with the repository and the last HOLD's gap as the
    reason. Each cycle's phases are their own records (attempt N of M).
    When the correction starts automatically, a corrected HOLD never
    concludes the run by itself: every other repository and stage in the run
    still runs, and the run's outcome is decided only when all of them have.
    When the operator starts a correction by hand, it stays within its own
    repository. If other repositories still have to run, the run ends saying
    which ones, and never claims to have passed.
11a. For every run, whatever its harness, a gate or review counts toward
    the outcome only if it passed after the last code change that precedes
    it in the workflow. A step the factory deliberately places after the
    checks is trusted, under D2, and does not make them stale. A review that
    reported a blocking finding never counts as passed. A run never
    ends `passed` while any required check or work phase lacks a passing
    result, in any repository the run is responsible for, including one it
    has not run yet. A correction attempt that failed and was later made good by
    fresh checks does not count against the run.
11b. For every run, whatever its harness, a blocked bullet's reason says
    why its run failed. It is never a generic statement that is untrue for
    that failure, and never the agent's raw output.
11c. For every run, whatever its harness, Sgt never resets progress an
    agent has recorded, or reverts edits it has made, when it returns to a
    repository during the same run.
12. A critic run whose output carries no recognisable PASS or HOLD verdict
    records the review phase `failed`, never a pass.
13. A phase never presents another phase's result envelope as its own.
14. Each phase record stores the harness, the role that ran it, and the
    plugin version when known; the dashboard shows them, and an em dash with
    the missing item named when absent.
15. Gauntlet roles apply to every Claude Code phase by default. A project can
    opt out with one setting, which restores today's prompts for that project.
16. When gauntlet roles apply and the gauntlet plugin is not available to
    Claude Code, any way of starting or restarting work is refused before any
    run record or worktree exists, and before a resumed run restarts. That
    covers a dispatch, a plan approval, a resume, a fix, and a command-line
    factory run. The refusal names the missing plugin and the opt-out.
16a. Every way of starting work, including the command-line factory run,
    follows the same execution path. It gets the same roles, the same checks
    before `passed`, and the same commit and delivery record. The
    command-line factory run never records `passed` when it ran nothing.
17. When gauntlet roles apply and a project's pipeline is the default (not
    explicitly listed), a review phase runs after the test gates. A pipeline
    the factory lists explicitly runs exactly as written.

### Migration

18. Projects that name no harness today (`smith`, `tycho`) move to Claude Code
    with gauntlet roles and a review phase. The change's release notes say so
    and name how to pin the previous harness.

## Delivery

Two OpenSpec changes, built in order. The operator split them on 2026-11-11
so the first can be gated and built while the second settles.

1. `claude-is-the-default-harness-and-runs-gauntlet-roles` delivers R1–R9,
   R9a, R11 up to the HOLD being recorded, R11a, R11b, R12–R18, and R16a.
   - In the interim, a HOLD fails the run and blocks its bullets. The
     operator starts the correction.
   - A builder handoff is recorded `failed`, never `passed`.
2. `held-reviews-self-correct-and-builder-handoffs-continue` delivers R10
   (successors), R11c, and the rest of R11 (automatic, bounded correction, and the
   run continuing afterwards).

## Accepted risks

- *Session fan-out (accepted 2026-11-11).* Roles on by default, review added
  to default pipelines, and HOLD auto-correction together allow roughly
  plan + build + review + `fix_retries` × (fix + review) Claude Code sessions
  per bullet. That is about 13 at the default budget of 5, with no
  per-session deadline. Builder handoffs (up to 3 successors per builder
  phase) raise the worst case to about 31. That figure is per stage a
  repository appears in. A repository in several stages can be corrected
  once in each. The operator judged this acceptable because the cost
  depends on the hardware and the software being built. The corrective budget
  and the agent timeout remain the controls. No separate HOLD budget.

## Open questions

1. When do gauntlet roles apply to a Claude Code phase?
   *Decided (2026-11-11): on by default, per-project opt-out (R15).*
2. When gauntlet roles apply but the plugin is not installed, what happens?
   *Decided (2026-11-11): refuse the dispatch before any run or worktree
   exists, naming the plugin and the opt-out (R16).*
3. Does a HOLD block the bullet immediately, or correct first?
   *Decided (2026-11-11): correct first, automatically, through a bounded
   corrective cycle (fresh builder, gates, fresh critic); block when the
   budget is spent (R11). Note: today's gate-fix cycle is operator-started;
   a HOLD-driven cycle starts on its own. This removes an interruption and
   adds none (D5).*
4. Is a review phase added to the pipeline by default when roles apply?
   *Decided (2026-11-11): yes, for default pipelines only; explicit
   pipelines are untouched (R17).*
5. Projects that name no harness today switch from opencode. Accept or pin?
   *Decided (2026-11-11): accept and document (R18).*
6. When the builder hands off mid-phase, what does Sgt do?
   *Decided (2026-11-11): start a successor builder from the handoff, in the
   same worktree, bounded; past the bound the phase fails (R10). This follows
   the gauntlet's own handoff protocol, with Sgt as the orchestrator.*
7. How does the command-line factory run (`sgt run`) behave when roles apply?
   *Decided (2026-11-11): full parity. It follows the same execution path as
   a dispatch (R16, R16a).*
8. Does fresh verification apply only to gauntlet runs, or to every run?
   *Decided (2026-11-11): every run. The named-harness non-goal is amended
   (R11a).*
9. One change or two?
   *Decided (2026-11-11): two, delivered in the order listed under Delivery.*
10. Two truthfulness fixes found during the gate also reach every harness:
    the reviewer's diff, and the blocked reason. Are they in scope?
    *Recorded (2026-11-11): yes, as R9a and R11b, under the operator's
    earlier choice (question 8) to apply truthfulness rules to every
    harness.*
