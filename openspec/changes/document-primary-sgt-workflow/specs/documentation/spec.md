# Documentation

## ADDED Requirements

### Requirement: The manual documents the complete primary delivery workflow

The shared Sgt manual SHALL explain how an operator takes a bounded intent to
reviewed pull requests using current released interfaces, without making either
the agent-driven or coordinator-driven entry path primary.

#### Scenario: An operator dispatches known repository scope

- **WHEN** the operator knows which repositories own the intent
- **THEN** the manual shows equivalent CLI and MCP dispatch actions with explicit
  repositories in merge order, work type, and OpenSpec change

#### Scenario: Repository scope requires review

- **WHEN** a dispatch omits repositories and creates a proposed intent
- **THEN** the manual explains that no run starts until the plan is explicitly
  approved, that the proposed bullets span every project repository in
  alphabetical order, and names the approval and rejection routes

#### Scenario: An operator follows and recovers work

- **WHEN** dispatched work is running or blocked
- **THEN** the manual names the released monitoring interfaces and the
  fix-then-resume path

#### Scenario: Green bullets become pull requests

- **WHEN** a bullet has passing gate evidence
- **THEN** the manual explains PR creation, identifies intent merge order as
  recorded but not currently enforced, and states that a human, never Sgt,
  performs the merge
