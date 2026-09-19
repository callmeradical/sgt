package upgrademigrate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/callmeradical/sgt/internal/config"
)

// verifyHook lets tests force an extra verification mismatch onto whatever
// runMigration's own checks found, without corrupting real git or sqlite
// state to manufacture one — the same kind of swappable-function test seam
// internal/manual's toolsFn already uses. Production code never overrides
// it: the identity function here is exactly "trust what verification
// found."
var verifyHook = func(mismatches []string) []string { return mismatches }

// Run performs detection, migration, and verification, writing the
// sentinel. It is safe to call on every subcommand invocation:
//
//   - no pre-rebrand v2 state and no sentinel -> no-op, returns nil, nil
//   - sentinel already "verified" -> no-op (no re-scan of source paths)
//   - sentinel "failed", or absent with state detected -> runs migration
//
// Returns the resulting Sentinel (nil if there was nothing to do) so every
// caller — the automatic check in cmd/sgt/main.go, the `sgt migrate`
// subcommand, and internal/ui's status surface (via LastResult, not Run) —
// reads the same shape.
func Run() (*Sentinel, error) {
	p, err := resolvePaths()
	if err != nil {
		return nil, err
	}

	prior, err := readSentinel(p.sentinelPath)
	if err != nil {
		return nil, fmt.Errorf("reading sentinel: %w", err)
	}
	if prior != nil && prior.Status == StatusVerified {
		// Decision 6: a verified sentinel is a hard skip. No source path is
		// scanned, no file is re-copied, not even Detected() runs.
		return prior, nil
	}

	isDetected, err := detected(p)
	if err != nil {
		return nil, fmt.Errorf("detecting pre-rebrand v2 state: %w", err)
	}
	if !isDetected {
		if prior == nil {
			// Nothing to migrate and no prior attempt to retry: a genuine
			// no-op.
			return nil, nil
		}
		// A prior (failed) sentinel exists, but every pre-rebrand v2 source
		// path has since disappeared from disk entirely. Falling through to
		// runMigration here would be a hollow no-op: every migrate step
		// would find nothing to read, so nothing would mismatch, and a
		// failed sentinel would flip to "verified" without ever actually
		// re-comparing anything against source data — contradicting
		// Decision 5/6's intent that verification be meaningful. Treat this
		// exactly like the "nothing to migrate" no-op case: leave the prior
		// sentinel exactly as it is, on disk, untouched.
		return prior, nil
	}

	return runMigration(p, prior)
}

// runMigration performs the three copy steps and verifies the result,
// writing and returning the sentinel. prior is the previous attempt's
// sentinel, or nil on a first attempt. It is no longer consulted merely for
// which items its own Conflicts list happened to name last time (a purely
// historical, content-blind proxy) — store and fleet migration instead
// re-check the destination's actual current content/identity against a
// marker prior itself recorded when it produced that content
// (Sentinel.StoreSnapshotHash / Sentinel.FleetItemState), the same
// self-healing, per-call re-check migrateConfig already performs via
// bytes.Equal. This means a failed→retry cycle (Decision 6) still doesn't
// relitigate or re-copy its own genuinely unchanged prior output, but a
// destination item that has organically diverged since (real use, or
// something else writing to it) is now correctly reported as a fresh
// conflict instead of being silently treated as still-ours.
func runMigration(p paths, prior *Sentinel) (*Sentinel, error) {
	var conflicts []string
	var mismatches []string

	cfgResult, err := migrateConfig(p)
	if err != nil {
		return nil, fmt.Errorf("migrating config: %w", err)
	}
	conflicts = append(conflicts, cfgResult.Conflicts...)

	storeResult, err := migrateStoreRetryAware(p, prior)
	if err != nil {
		return nil, fmt.Errorf("migrating store: %w", err)
	}
	if storeResult.Conflict != "" {
		conflicts = append(conflicts, storeResult.Conflict)
	}

	fleetResult, err := migrateFleetRetryAware(p, prior)
	if err != nil {
		return nil, fmt.Errorf("migrating fleet: %w", err)
	}
	conflicts = append(conflicts, fleetResult.Conflicts...)

	mismatches = append(mismatches, verifyConfig(p, cfgResult.Migrated)...)
	mismatches = append(mismatches, verifyStore(p, storeResult)...)
	mismatches = append(mismatches, verifyFleet(p, fleetResult.Migrated)...)
	mismatches = verifyHook(mismatches)

	status := StatusVerified
	if len(mismatches) > 0 {
		status = StatusFailed
	}

	sentinel := &Sentinel{
		Status:            status,
		SourcePaths:       []string{p.oldConfigDir, p.oldDBPath, p.oldFleetRoot},
		Timestamp:         time.Now().UTC(),
		Conflicts:         conflicts,
		Mismatches:        mismatches,
		StoreSnapshotHash: storeResult.StoreSnapshotHash,
		FleetItemState:    fleetResult.ItemState,
	}
	if err := writeSentinel(p.sentinelPath, sentinel); err != nil {
		return nil, fmt.Errorf("writing sentinel: %w", err)
	}
	return sentinel, nil
}

// migrateStoreRetryAware wraps migrateStore with a content-based "is this
// destination unchanged since we produced it" check for the failed→retry
// cycle: if newDBPath exists and prior recorded a StoreSnapshotHash (meaning
// this package itself successfully produced newDBPath on some earlier
// attempt), the destination's *current* hash is recomputed and compared
// against that recorded value — not against whether the prior sentinel's
// Conflicts list happened to name "store".
//
//   - Hash still matches: genuinely unchanged since we produced it. Left in
//     place, treated as already migrated, and its source counts are
//     recomputed fresh (the source is read-only, so re-vacuuming it into a
//     throwaway snapshot purely to count rows is always safe) so verifyStore
//     still has something to check.
//   - Hash no longer matches: the destination has organically diverged since
//     migration ran (real use, or something else writing to it) and is no
//     longer safely "ours" to recount against a frozen source snapshot —
//     report a fresh "store" conflict and leave the destination completely
//     untouched, exactly like any other never-ours conflicting destination.
//   - No recorded hash at all (first attempt, or an old sentinel from before
//     this marker existed): falls through to plain migrateStore, which
//     fails closed with a "store" conflict if newDBPath already exists.
func migrateStoreRetryAware(p paths, prior *Sentinel) (storeMigrationResult, error) {
	if prior != nil && prior.StoreSnapshotHash != "" && fileExists(p.newDBPath) {
		currentHash, err := hashFile(p.newDBPath)
		if err != nil {
			return storeMigrationResult{}, fmt.Errorf("hashing existing %s: %w", p.newDBPath, err)
		}
		if currentHash != prior.StoreSnapshotHash {
			// Organically diverged since we produced it: no longer ours.
			// Do not touch it, do not recount it — just report it as a
			// fresh, genuine conflict.
			return storeMigrationResult{Conflict: "store"}, nil
		}

		if !fileExists(p.oldDBPath) {
			// The source has vanished since the original copy (see Gap 3 /
			// Run()'s own vanished-source guard, which normally catches the
			// case where *every* source path is gone; this handles the
			// narrower case where only the store's source specifically is
			// gone while config/fleet source still exists). There is
			// nothing left to meaningfully recompute against — comparing a
			// real, unchanged destination against an empty, freshly
			// auto-created database at a nonexistent path would fabricate a
			// phantom mismatch, not a genuine one. Compare the destination
			// against itself instead: nothing to verify, so nothing fails.
			destRuns, err := countRows(p.newDBPath, "runs")
			if err != nil {
				return storeMigrationResult{}, fmt.Errorf("counting destination runs: %w", err)
			}
			destPhases, err := countRows(p.newDBPath, "phases")
			if err != nil {
				return storeMigrationResult{}, fmt.Errorf("counting destination phases: %w", err)
			}
			return storeMigrationResult{
				Migrated:          true,
				SourceRuns:        destRuns,
				SourcePhases:      destPhases,
				StoreSnapshotHash: currentHash,
			}, nil
		}

		counts, err := sourceRowCountsOnly(p.oldDBPath)
		if err != nil {
			return storeMigrationResult{}, err
		}
		counts.Migrated = true
		counts.StoreSnapshotHash = currentHash
		return counts, nil
	}
	return migrateStore(p)
}

// sourceRowCountsOnly VACUUMs oldDBPath into a throwaway temp snapshot
// purely to count its runs/phases rows WAL-safely, without touching
// newDBPath at all. Used only on a retry where newDBPath already holds this
// package's own prior output.
func sourceRowCountsOnly(oldDBPath string) (storeMigrationResult, error) {
	var result storeMigrationResult
	tmpDir, err := os.MkdirTemp("", "sgt-migrate-count-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmpDir)
	snapshotPath := filepath.Join(tmpDir, "snapshot.db")
	if err := vacuumInto(oldDBPath, snapshotPath); err != nil {
		return result, fmt.Errorf("snapshotting %s for recount: %w", oldDBPath, err)
	}
	result.SourceRuns, err = countRows(snapshotPath, "runs")
	if err != nil {
		return result, err
	}
	result.SourcePhases, err = countRows(snapshotPath, "phases")
	if err != nil {
		return result, err
	}
	return result, nil
}

// migrateFleetRetryAware wraps migrateFleet with a content-based "is this
// destination unchanged since we copied it" check for the failed→retry
// cycle: a <task>/<repo> directory that already exists at the destination is
// compared against prior's recorded FleetItemState marker for that exact
// item (the git HEAD/status captured immediately after this package copied
// it), not against whether the prior sentinel's Conflicts list happened to
// name it.
//
//   - A recorded marker exists and still matches the destination's current
//     HEAD/status: genuinely unchanged since we copied it. Resubmitted for
//     verification rather than reported as a fresh conflict or re-copied.
//   - A recorded marker exists but no longer matches (or the destination's
//     git state can't even be read): the worktree has organically diverged
//     since the copy (a new commit, local edits) — report a fresh
//     "fleet:<task>/<repo>" conflict and leave it completely untouched.
//   - No recorded marker at all (first attempt, or an old sentinel from
//     before this marker existed): fails closed with a fresh conflict,
//     exactly as a genuinely pre-existing, never-ours destination would.
//
// Markers for items not visited this call (their source directory is gone,
// or oldFleetRoot itself is gone) are carried forward unchanged from prior
// rather than dropped, so a transient absence of the source doesn't erase
// this package's memory of what it once produced.
func migrateFleetRetryAware(p paths, prior *Sentinel) (fleetMigrationResult, error) {
	priorState := map[string]FleetItemMarker{}
	if prior != nil {
		for k, v := range prior.FleetItemState {
			priorState[k] = v
		}
	}

	if !dirExists(p.oldFleetRoot) {
		return fleetMigrationResult{ItemState: priorState}, nil
	}

	result := fleetMigrationResult{ItemState: map[string]FleetItemMarker{}}
	for k, v := range priorState {
		result.ItemState[k] = v
	}

	taskEntries, err := os.ReadDir(p.oldFleetRoot)
	if err != nil {
		return result, fmt.Errorf("listing %s: %w", p.oldFleetRoot, err)
	}
	for _, taskEntry := range taskEntries {
		if !taskEntry.IsDir() {
			continue
		}
		task := taskEntry.Name()
		taskDir := filepath.Join(p.oldFleetRoot, task)
		repoEntries, err := os.ReadDir(taskDir)
		if err != nil {
			return result, fmt.Errorf("listing %s: %w", taskDir, err)
		}
		for _, repoEntry := range repoEntries {
			if !repoEntry.IsDir() {
				continue
			}
			repo := repoEntry.Name()
			item := fleetItem{Task: task, Repo: repo}
			key := item.String()
			srcDir := filepath.Join(taskDir, repo)
			dstDir := filepath.Join(p.newFleetRoot, task, repo)

			if dirExists(dstDir) {
				if marker, known := priorState[key]; known {
					curHead, curStatus, gerr := gitStatePorcelain(dstDir)
					if gerr == nil && curHead == marker.Head && curStatus == marker.Status {
						// Genuinely unchanged since we copied it; resubmit
						// for verification rather than re-copying.
						result.Migrated = append(result.Migrated, item)
						result.ItemState[key] = marker
						continue
					}
				}
				// Either no marker was ever recorded for this item (never
				// ours), or it no longer matches what we recorded: report a
				// fresh conflict and leave the destination completely
				// untouched either way.
				result.Conflicts = append(result.Conflicts, "fleet:"+key)
				delete(result.ItemState, key)
				continue
			}

			if err := copyTree(srcDir, dstDir); err != nil {
				return result, fmt.Errorf("copying worktree %s to %s: %w", srcDir, dstDir, err)
			}
			head, status, gerr := gitStatePorcelain(dstDir)
			if gerr != nil {
				return result, fmt.Errorf("capturing post-copy git state for %s: %w", dstDir, gerr)
			}
			result.Migrated = append(result.Migrated, item)
			result.ItemState[key] = FleetItemMarker{Head: head, Status: status}
		}
	}
	return result, nil
}

// verifyConfig independently re-reads (never reusing bytes migrateConfig
// already held in memory) every file migrateConfig reported as migrated,
// confirming source and destination are still byte-identical, and confirms
// each migrated project is actually visible through config.ListProjects()
// reading the real destination — catching a parse-level regression a bytewise
// compare alone would miss.
func verifyConfig(p paths, migrated []string) []string {
	var mismatches []string
	for _, name := range migrated {
		srcData, errS := os.ReadFile(filepath.Join(p.oldConfigDir, name))
		dstData, errD := os.ReadFile(filepath.Join(p.newConfigDir, name))
		if errS != nil || errD != nil || !bytes.Equal(srcData, dstData) {
			mismatches = append(mismatches, fmt.Sprintf("config:%s content mismatch after migration", name))
		}
	}
	if len(migrated) == 0 {
		return mismatches
	}

	projects, err := config.ListProjects()
	if err != nil {
		mismatches = append(mismatches, fmt.Sprintf("config: listing projects at destination: %v", err))
		return mismatches
	}
	present := map[string]bool{}
	for _, proj := range projects {
		present[proj.Name] = true
	}
	for _, name := range migrated {
		if name == config.GlobalConfigFileName {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !present[stem] {
			mismatches = append(mismatches, fmt.Sprintf("config:%s not visible via ListProjects after migration", name))
		}
	}
	return mismatches
}

// verifyStore independently re-opens newDBPath (a fresh connection, never
// the one migrateStore/store.Open already used) and compares its run/phase
// row counts against the source snapshot's own counts, captured before the
// snapshot was moved into place.
func verifyStore(p paths, sr storeMigrationResult) []string {
	if !sr.Migrated {
		return nil
	}
	var mismatches []string
	destRuns, err := countRows(p.newDBPath, "runs")
	if err != nil {
		return []string{fmt.Sprintf("store: counting destination runs: %v", err)}
	}
	destPhases, err := countRows(p.newDBPath, "phases")
	if err != nil {
		return append(mismatches, fmt.Sprintf("store: counting destination phases: %v", err))
	}
	if destRuns != sr.SourceRuns {
		mismatches = append(mismatches, fmt.Sprintf("store: run count mismatch: source=%d destination=%d", sr.SourceRuns, destRuns))
	}
	if destPhases != sr.SourcePhases {
		mismatches = append(mismatches, fmt.Sprintf("store: phase count mismatch: source=%d destination=%d", sr.SourcePhases, destPhases))
	}
	return mismatches
}

// verifyFleet independently re-runs `git status --porcelain` and
// `git rev-parse HEAD` (fresh subprocess invocations, not anything
// migrateFleet/copyTree observed) against both the source and the copied
// destination for every migrated worktree, and reports any difference.
func verifyFleet(p paths, migrated []fleetItem) []string {
	var mismatches []string
	for _, item := range migrated {
		srcDir := filepath.Join(p.oldFleetRoot, item.Task, item.Repo)
		dstDir := filepath.Join(p.newFleetRoot, item.Task, item.Repo)

		srcStatus, srcHead, errS := gitStatePorcelain(srcDir)
		dstStatus, dstHead, errD := gitStatePorcelain(dstDir)
		if errS != nil || errD != nil {
			mismatches = append(mismatches, fmt.Sprintf("fleet:%s: git status error: src=%v dst=%v", item, errS, errD))
			continue
		}
		if srcStatus != dstStatus || srcHead != dstHead {
			mismatches = append(mismatches, fmt.Sprintf("fleet:%s: git state mismatch after migration", item))
		}
	}
	return mismatches
}
