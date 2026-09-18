`docs/schema.md` — cited elsewhere as the canonical v2 project-YAML
reference — describes the `name`, `url`, `output`, `exclude_patterns`
fields and review routing as driven by deleted v1 binaries
(`sgt-graphify`, `sgt-sync`, `sgt-dispatch`). One of those fields (`url`)
does not even exist in `config.Repo`. This change corrects the doc to
name the real, current v2 mechanism behind each fact, or to say honestly
when no current mechanism enforces what the doc claims.
