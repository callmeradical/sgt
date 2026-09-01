# Documentation accuracy

## ADDED Requirements

### Requirement: `docs/schema.md` names only mechanisms that currently exist

`docs/schema.md`, the canonical v2 project-YAML reference, SHALL describe
each field's behavior in terms of the real, current v2 mechanism that
implements it, never a deleted v1 binary.

#### Scenario: No deleted v1 binary is named as a current mechanism

- **WHEN** `docs/schema.md` describes any field's behavior
- **THEN** it does not name `sgt-graphify`, `sgt-sync`, or `sgt-dispatch`
  as the thing that implements that behavior

#### Scenario: A documented field actually exists

- **WHEN** `docs/schema.md` documents a project-YAML field as part of
  `config.Repo`
- **THEN** that field actually exists on `config.Repo`

#### Scenario: An unenforced constraint is described honestly

- **WHEN** `docs/schema.md` describes a validation constraint on a field
- **THEN** the description matches whether that constraint is actually
  enforced in the current codebase, attributing it to real code if
  enforced, or stating plainly that it is not currently enforced if not
