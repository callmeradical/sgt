# Store query performance

## ADDED Requirements

### Requirement: Hot list queries retain index-order plans

Frequently used ordered store queries SHALL use their intended composite index
and SHALL NOT sort through a temporary B-tree.

#### Scenario: Runs are listed for a project

- **WHEN** `ListRunsForProject` is planned
- **THEN** it uses `idx_runs_project_created_at` without a temporary B-tree

#### Scenario: Intents are listed for a project

- **WHEN** `ListIntentsForProject` is planned
- **THEN** it uses `idx_intents_project_created_at_id` without a temporary B-tree

#### Scenario: Intents are listed by status

- **WHEN** `ListIntentsByStatus` is planned
- **THEN** it uses `idx_intents_status_created_at_id` without a temporary B-tree
