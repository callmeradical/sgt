# Tasks — `docs/schema.md` describes v2, not deleted v1 binaries

One repository, `sgt`, so one task.

## Task 1 — investigate, correct the doc, add the regression test

Repository: `sgt`. Depends on: nothing. Read
`openspec/changes/schema-doc-describes-v2-not-v1/{proposal,design}.md`
and `specs/documentation/spec.md` first — they are binding. Read
`AGENTS.md`. Work test-first per decision D3: write the regression test
against the *current* (stale) `docs/schema.md` first and confirm it
fails, then fix the doc and confirm it passes.

- Complete the investigation design.md's "Investigation before writing
  any prose" section lists: confirm whether `name`'s format constraint is
  enforced anywhere in `internal/graphify`/`internal/config`; confirm
  `config.Repo`'s actual field list; read `internal/graphify/
  graphify.go`'s current merge/publish behavior; read
  `internal/ui/dispatch.go`'s `"review"` stage handling.
- Rewrite `docs/schema.md`'s `name`, `url`, `output`, `exclude_patterns`
  rows and the review-routing sentence per proposal.md's per-field
  instructions, citing real, current source.
- Add the regression test described in design.md (e.g.
  `internal/config/schema_doc_test.go`): fails on the literal strings
  `sgt-graphify`/`sgt-sync`/`sgt-dispatch` appearing in `docs/schema.md`,
  and fails if the doc still describes a `url` field on `config.Repo`
  that does not exist (a reflection check against the real struct, not
  just a substring match, so the test also catches the same mistake
  recurring in different words).
- Do not touch any other file. Do not add a general-purpose doc linter.

Verification: `go build ./... && go vet ./... && go test ./internal/... -count=1`.
The new test must fail against the original `docs/schema.md` content and
pass after the fix — demonstrate this during implementation (revert the
doc fix, confirm the test fails; restore it, confirm the test passes;
this is the change's own mutation test, since the "mutation" here is the
literal difference between stale and corrected prose). Exit status
decides the outcome.
