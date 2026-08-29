# Product Requirements: A Failed Gate Can Be Corrected In Place, Not Just Retried

Status: Draft, awaiting explicit human PRD approval

Extends: `docs/prd-sgt.md`, R6 (recovery and failure handling)

## Summary

Today, when a run fails because a deterministic gate (build, test, vet)
genuinely fails — not a flake — the only recovery paths are `POST
/api/run-resume` (re-runs the exact same command verbatim; only helps
when the failure was transient) or a brand-new `POST /api/dispatch`
(starts an entirely fresh worktree and branch, discarding whatever the
failed run's own worktree already held). Neither actually fixes the
underlying problem. This PRD adds a real "fix" action: dispatch a
corrective agent phase into the failed run's existing worktree and
branch, informed by that gate's own real failure output, to make an
actual code change and then re-run the gate — instead of requiring a
human to either read the failure and patch it by hand, or discard the
run's progress and start over.

## Problem

A deterministic gate (`RunCodeGate`) runs exactly once with no retry
budget of its own — unlike an agent phase (`RunAgentPhase`), which
already retries within its own turn before the run concludes. Once a
gate fails for a real reason (not a flake), the run is done: `blocked`
or `failed`, its retry budget already exhausted by the time it got
there. Today, actually fixing that failure requires a human to open the
run's worktree by hand and fix it directly — `skills/dispatch/
SKILL.md`'s own troubleshooting table says exactly this: "fix the
underlying issue, then `POST /api/run-resume`." Sgt has no way to
dispatch an agent back into that exact worktree, carrying the gate's own
real output as its starting context, to do that fix itself.

This is a real, common cost: every gate failure this session that
turned out to need an actual fix (as opposed to being an unrelated
flake) required a human to read the failure, understand it, and either
fix it by hand or draft a fresh corrective dispatch from scratch —
losing the original run's existing worktree, branch, and any partial
progress in the process.

## Proposal

- A new action, distinct from Resume, that dispatches a corrective agent
  phase into a failed run's *existing* worktree and branch — never a
  fresh one — carrying that run's actual failing-gate output as the
  agent's starting context. This output must pass through the same
  redaction choke point every other captured phase output already does
  (R4.4) before it ever reaches the agent — never raw, unredacted
  output handed to a new agent session.
- After the corrective phase concludes, the failing gate is re-run
  automatically, the same way an ordinary dispatch's own pipeline
  already re-runs a gate following an agent phase — a human should not
  have to separately remember to trigger the retry themselves.
- A bounded number of corrective attempts, so a genuinely unfixable
  failure still reaches a human rather than looping forever. The exact
  bound is an implementation decision for `design.md`; this PRD only
  requires that some bound exists.
- This is an explicit, operator-triggered action for a first version —
  it does not fire automatically the instant any gate fails. Matches D5's
  "three interruptions only" philosophy: silently escalating how much
  unattended agent activity a project incurs, without an operator having
  asked for it, is a bigger decision than this PRD makes on its own.

## Out of scope

- **Automatically triggering a corrective attempt the moment any gate
  fails**, with no explicit operator action. Rejected for a first
  version, per the Proposal above.
- **A failure whose worktree no longer exists** (already reclaimed by
  `automated-fleet-cleanup` or otherwise removed). That case already has
  its answer: a fresh dispatch, same as today. This PRD only changes
  what's possible while the original worktree still exists.
- **Any change to the existing plain "Resume" action**, which remains
  exactly as-is for the transient-failure case this PRD does not
  replace.
- **Choosing or overriding which agent CLI drives the corrective
  phase.** Reuses whatever the original run's own phases already used —
  the same implicit reuse Resume already relies on by re-entering the
  existing worktree/branch.
- **Any change to redaction itself.** This PRD requires the existing
  redaction choke point be used, not that a new one be built.

## Open questions

- Exact bound on corrective attempts before falling back to `blocked`
  with a human required — a `design.md` decision informed by whatever
  default this codebase already uses for agent-phase retries
  (`RunAgentPhase`'s existing `retries` parameter).
- Should a corrective attempt's own phase records be visually
  distinguished in the dashboard from the original run's phases (so an
  operator can tell "this passed on the second, corrective attempt" from
  "this passed the first time")? Left open, not required for a first
  version.
