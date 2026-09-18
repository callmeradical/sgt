# Responsive layout

## ADDED Requirements

### Requirement: The pipeline runs list is reachable below the desktop breakpoint

Below the `lg` breakpoint, an operator SHALL have a visible control that
reveals the pipeline runs list (`#master-rail`), and a way to dismiss it
back to the workflow view. At `lg` and above, the list SHALL remain
visible exactly as it is today, with no new toggle needed.

#### Scenario: A phone-width operator can open and use the runs list

- **WHEN** the dashboard is viewed below the `lg` breakpoint
- **THEN** a visible control reveals `#master-rail`'s run list, and
  selecting a run from it both selects that run and returns to the
  workflow view

#### Scenario: The desktop layout is unchanged

- **WHEN** the dashboard is viewed at `lg` or above
- **THEN** `#master-rail` renders visible exactly as before, and the new
  mobile toggle control is not shown

### Requirement: Icon-only actions carry a visible label below the desktop breakpoint

Below the `lg` breakpoint, interactive icon-only controls that today rely
solely on a `title=` hover tooltip for their visible meaning SHALL also
show a visible text label. Above `lg`, today's icon-only-plus-tooltip
presentation SHALL be unchanged.

#### Scenario: A touch user can identify an icon-only action without hovering

- **WHEN** the dashboard is viewed below the `lg` breakpoint
- **THEN** each affected control shows a visible text label alongside its
  icon, not only a `title=` attribute

#### Scenario: Desktop presentation is unchanged

- **WHEN** the dashboard is viewed at `lg` or above
- **THEN** the affected controls render icon-only with their existing
  `title=` tooltip, exactly as before

### Requirement: The workflow graph remains readable at a phone width

`#workflow-graph`'s rendered content SHALL remain reachable and readable
when the dashboard is viewed at a phone-class viewport width, whether
through the existing scroll behavior or a narrow-width-specific
treatment.

#### Scenario: Every lane's content is reachable at a phone width

- **WHEN** a run's workflow graph is rendered at a phone-class viewport
  width
- **THEN** every lane and its phase nodes can be reached (by scrolling,
  a stacked layout, or an equivalent mechanism) and read without
  horizontal content being permanently cut off with no way to reach it

### Requirement: No regression to existing desktop behavior or capability

Every action and view reachable on the existing desktop dashboard SHALL
remain reachable, unchanged, at `lg` and above. No backend endpoint,
frontend framework, build step, or third-party JS dependency SHALL be
added.

#### Scenario: The dashboard's desktop behavior is bit-for-bit unchanged above `lg`

- **WHEN** the dashboard is viewed at `lg` or above
- **THEN** every element, control, and interaction behaves exactly as it
  did before this change
