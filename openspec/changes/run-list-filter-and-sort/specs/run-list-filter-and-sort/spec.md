## ADDED Requirements

### Requirement: Filter runs by status
The dashboard's runs panel SHALL let an operator filter the displayed
run list to only runs whose status matches one selected value from
`running`, `passed`, `failed`, `blocked`, `cancelled`. The filter SHALL
operate on the `/api/runs` payload already loaded, without an
additional network request.

#### Scenario: Selecting a status shows only matching runs
- **WHEN** an operator selects `failed` in the status filter while the
  runs panel holds runs of mixed status
- **THEN** only runs whose status is `failed` are displayed

#### Scenario: Clearing the status filter restores the full list
- **WHEN** an operator clears a previously selected status filter
- **THEN** every run from the loaded `/api/runs` payload is displayed
  again, subject to any other active filters

### Requirement: Filter runs by repo
The dashboard's runs panel SHALL let an operator filter the displayed
run list to only runs touching one selected repo name.

#### Scenario: Selecting a repo shows only matching runs
- **WHEN** an operator selects a repo name in the repo filter while the
  runs panel holds runs against multiple repos
- **THEN** only runs whose repo list includes the selected repo are
  displayed

### Requirement: Filter runs by work type
The dashboard's runs panel SHALL let an operator filter the displayed
run list to only runs whose declared work type (decision O2: `feat`,
`fix`, `refactor`, `docs`, `chore`, `test`) matches one selected value.

#### Scenario: Selecting a work type shows only matching runs
- **WHEN** an operator selects `fix` in the work-type filter while the
  runs panel holds runs of mixed type
- **THEN** only runs whose type is `fix` are displayed

### Requirement: Filters and free-text search compose
The status, repo, and work-type filters SHALL compose with the
existing free-text search box using AND semantics: a run SHALL be
displayed only if it matches every currently active filter and search
term simultaneously.

#### Scenario: An active filter and a search term both narrow the list
- **WHEN** an operator has selected `passed` in the status filter and
  also typed a search term that matches only some passed runs
- **THEN** only runs that are both status `passed` and matched by the
  search term are displayed

### Requirement: Sort runs by recency or duration
The dashboard's runs panel SHALL let an operator sort the displayed run
list by creation time (newest-first, the existing default, or
oldest-first) or by run duration.

#### Scenario: Sorting by oldest-first reorders the list
- **WHEN** an operator selects oldest-first sort
- **THEN** the displayed runs are ordered by ascending creation time

#### Scenario: Sorting by duration reorders the list
- **WHEN** an operator selects duration sort
- **THEN** the displayed runs are ordered by run duration, longest or
  shortest first as selected

### Requirement: Filter and sort state is reflected in the URL
The dashboard SHALL encode the active status filter, repo filter,
work-type filter, and sort selection as URL query parameters, so a
filtered/sorted view can be shared or bookmarked and restored exactly
on reload.

#### Scenario: Reloading a URL with filter parameters restores the view
- **WHEN** an operator loads a dashboard URL that includes status,
  repo, work-type, and sort query parameters
- **THEN** the runs panel applies those same filters and sort order on
  initial render, without requiring the operator to reselect them

#### Scenario: Changing a filter updates the URL without a full reload
- **WHEN** an operator changes any filter or sort control
- **THEN** the browser URL updates to reflect the new selection without
  a full page navigation
