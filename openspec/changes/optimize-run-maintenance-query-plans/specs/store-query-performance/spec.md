# Store query performance

## ADDED Requirements

### Requirement: Run-maintenance queries read in index order

Run-maintenance queries SHALL use a suitable index and SHALL NOT sort through a
temporary B-tree.

#### Scenario: Startup reconciliation reads running runs in creation order

- **WHEN** the startup reconciliation query is planned
- **THEN** it uses the status/created-at index without a temporary B-tree

#### Scenario: Startup reconciliation groups running phases without a global sort

- **WHEN** reconciliation fetches running phases for several orphaned runs
- **THEN** it uses the run/created-at index without a temporary B-tree because
  no global phase order is required

#### Scenario: Fleet cleanup reads eligible runs in update order

- **WHEN** the cleanup eligibility query is planned
- **THEN** it uses the updated-at/status index without a temporary B-tree

#### Scenario: Retention rotation reuses the project index

- **WHEN** the project rotation query requests ascending creation order
- **THEN** it uses the existing project/created-at index without creating a
  direction-only duplicate, and migration removes that duplicate if present

### Requirement: Maintenance indexes deploy idempotently

Opening a database SHALL create every required maintenance index if absent and
SHALL succeed on subsequent opens.

#### Scenario: An older database is reopened twice

- **WHEN** a database predating the maintenance indexes is opened twice
- **THEN** both required indexes are present after each open
