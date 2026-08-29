Adds a "Fix" action, distinct from the existing plain Resume, for a
failed/blocked run whose worktree still exists: an operator triggers it
once, and Sgt dispatches a corrective agent phase into that same
worktree/branch, carrying the failing gate's own real (already redacted)
output as context, then automatically re-runs the gate. If it fails
again, the cycle repeats on its own — no re-triggering required — up to
a per-project/per-repo configurable bound (default 5), then falls back
to requiring a human exactly as today. Each cycle is recorded distinctly
so the dashboard can show "Attempt N of M" and the real retry loop
(e.g. `test` → `build` → `test`) as a connected cycle, not a flat list.
