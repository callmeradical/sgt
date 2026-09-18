# Sgt's own OKF wiki

## ADDED Requirements

### Requirement: A completed run is rendered into the project's OKF wiki

When a run reaches a terminal status, Sgt SHALL render it as a concept
page in that project's own OKF wiki, distinct from the operator's
personal wiki.

#### Scenario: A terminal run gets its own concept page

- **WHEN** a run reaches a terminal status (passed/failed/cancelled/
  interrupted)
- **THEN** a concept page exists under that project's wiki root, dated
  by the day it completed, carrying at minimum the `type` frontmatter
  field and the run's brief, bullets, and outcome

#### Scenario: The wiki never writes to the operator's personal vault

- **WHEN** any wiki page is written by this feature
- **THEN** it is written under Sgt's own wiki root
  (`ProjectRoot(project)`), never under `~/wiki` or any path the
  operator's personal wiki convention owns

#### Scenario: Rendering never fails the run

- **WHEN** writing a run's wiki page encounters an I/O error
- **THEN** the error is logged and the run's own terminal status is
  recorded exactly as it would have been regardless — a wiki-write
  failure never changes a run's outcome or is surfaced as a run error

### Requirement: The wiki's structure and pages are OKF v0.2 conformant

Every file the wiki writes SHALL conform to the Open Knowledge Format
v0.2 specification's structural rules for index files, concept-page
frontmatter, and cross-links.

#### Scenario: The wiki root index carries okf_version

- **WHEN** a project's wiki root `index.md` is read
- **THEN** its frontmatter includes `okf_version: "0.2"`

#### Scenario: A dated index carries no frontmatter

- **WHEN** a dated folder's `index.md` is read
- **THEN** it carries no YAML frontmatter, per the spec's rule that only
  a bundle-root index may include one

#### Scenario: A concept page always names its type

- **WHEN** any run's concept page is read
- **THEN** its frontmatter includes a non-empty `type` field

#### Scenario: Links are bundle-relative

- **WHEN** a wiki page links to another page in the same wiki
- **THEN** the link uses the bundle-relative absolute form (a leading
  `/`, resolved from the wiki's own root), not a path outside the wiki

### Requirement: Wiki content is a deterministic rendering, never synthesized

Wiki page content SHALL be rendered deterministically from data already
durable in the store — it SHALL NOT be synthesized by a model call.

#### Scenario: No model call is made to produce wiki content

- **WHEN** a run's wiki page is generated
- **THEN** its content is built entirely from `store.RunRecord` and
  `store.BulletRecord` fields already durable at that point — no LLM or
  external API call is made

#### Scenario: A blocked run's page names the real reason

- **WHEN** a run's bullets moved to `blocked`
- **THEN** the run's concept page includes the same blocked reason
  `blockedReasonForRun` already resolved for that run — not a generic or
  placeholder message

### Requirement: Concurrent runs never corrupt shared wiki files

Sgt SHALL serialize writes to a project's shared wiki files so that two
runs completing near-simultaneously never lose one update to the other.

#### Scenario: Two runs completing near-simultaneously both appear

- **WHEN** two different runs in the same project reach a terminal
  status at nearly the same time
- **THEN** both runs' entries appear in that date's `index.md`/`log.md`
  and the wiki root `index.md` — neither update is lost to a race
