# Tasks - Document the primary Sgt workflow

One repository, `sgt-v2`, so one task and no cross-repo merge order.

## Task 1 - Publish the intent-to-PR walkthrough

Repository: `sgt-v2`. Depends on: nothing.

- Add regression coverage requiring the shared manual to name dispatch,
  proposal approval, monitoring, PR creation, and the human merge boundary.
- Expand the day-to-day manual section with exact released commands and tools.
- Link the README and documentation index to the walkthrough.
- Reconcile AGENTS.md, architecture, and the core PRD with the released
  coordinator-driven CLI/API surface.

Verification: `go test ./internal/manual -count=1`, then the repository build,
vet, and internal test gates.
