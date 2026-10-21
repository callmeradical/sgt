# Proposal - Document the primary Sgt workflow

## Why

The manual names Sgt's Intent and Bullet model and lists the two equal entry
paths, but it stops before showing an operator how work moves from a bounded
intent through dispatch, approval, isolated execution, recovery, PR creation,
and human merge. Operators must reconstruct the normal workflow from API,
architecture, and skill documents.

## What Changes

- Expand the embedded manual's day-to-day section into an end-to-end workflow.
- Show equivalent CLI, MCP, and HTTP actions at each operator boundary, and
  identify the dashboard's current observation/recovery role.
- Explain explicit-repo dispatch versus proposed-plan approval, red-to-green
  evidence, recovery, PR sealing, recorded-but-not-enforced merge order, and the
  human merge boundary.
- Point the README and documentation index directly to the walkthrough.

## Capabilities

### Modified Capabilities

- `documentation`: the shared manual provides an executable, current workflow
  instead of only a conceptual summary.

## Impact

- Documentation and its regression test only; runtime behavior does not change.
- Reconcile the core PRD and architecture wording with the released
  coordinator-driven CLI/API surface; this clarifies D1 without changing its
  two-path, one-record-set product decision.
