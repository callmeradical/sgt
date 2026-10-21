# Design - Document the primary Sgt workflow

## One lifecycle, two equal entry paths

The walkthrough calls the intent-to-PR lifecycle primary, not either interface.
This preserves D1: an operator may enter through an MCP-connected agent or the
coordinator API/CLI, while both produce the same stored records. The embedded
dashboard is documented as the observation/recovery surface the current
frontend actually implements, not as a dispatch or plan-approval form.

## Source of truth

The walkthrough uses only released interfaces verified in code:

- CLI flags from `cmd/sgt/dispatch_cli.go`.
- MCP tools and schemas from `internal/mcp.Tools()`.
- plan approval, resume, dispatch, and PR routes from `Server.Handler()`.
- lifecycle and safety decisions D1-D6 from `docs/prd-sgt.md`.

Where a settled decision is not yet enforced by released code, the walkthrough
states that gap explicitly. In particular, bullet position records merge order,
but current PR creation checks only that the target bullet is green; the
operator still sequences merges.

The command and MCP reference tables remain generated live. The walkthrough is
hand-written because it explains ordering and operator decisions rather than
enumerating interfaces; a manual test pins its critical action names.

## Documentation boundary

The walkthrough lives in `internal/manual/manual.md`, so `sgt help`,
`GET /api/manual`, and the dashboard manual drawer render the same content.
README files link to it rather than copying the procedure.

The core PRD's D1 wording is updated from "dispatch from the UI" to the released
coordinator-driven server surface (`sgt dispatch` / `POST /api/dispatch`). This
does not change D1's product behavior; it removes an obsolete frontend claim.
