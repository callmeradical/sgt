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
	if !isDetected && prior == nil {
		return nil, nil
	}

	return runMigration(p, prior)
}

// runMigration performs the three copy steps and verifies the result,
// writing and returning the sentinel. prior is the previous attempt's
// sentinel, or nil on a first attempt — it is consulted only to tell "this
// destination item already exists because we put it there" apart from "this
// destination item already exists and is a genuine, still-unresolved
// conflict", so a failed→retry cycle (Decision 6) does not relitigate every
// conflict it already reported as a fresh one, nor treat its own prior
// output as something to skip verifying again.
func runMigration(p paths, prior *Sentinel) (*Sentinel, error) {
	hasPrior := prior != nil
	priorConflicts := map[string]bool{}
	if prior != nil {
		for _, c := range prior.Conflicts {
			priorConflicts[c] = true
		}
	}

	var conflicts []string
	var mismatches []string

	cfgResult, err := migrateConfig(p)
	if err != nil {
		return nil, fmt.Errorf("migrating config: %w", err)
	}
	conflicts = append(conflicts, cfgResult.Conflicts...)

	storeResult, err := migrateStoreRetryAware(p, hasPrior, priorConflicts)
	if err != nil {
		return nil, fmt.Errorf("migrating store: %w", err)
	}
	if storeResult.Conflict != "" {
		conflicts = append(conflicts, storeResult.Conflict)
	}

	fleetResult, err := migrateFleetRetryAware(p, hasPrior, priorConflicts)
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
		Status:      status,
		SourcePaths: []string{p.oldConfigDir, p.oldDBPath, p.oldFleetRoot},
		Timestamp:   time.Now().UTC(),
		Conflicts:   conflicts,
		Mismatches:  mismatches,
	}
	if err := writeSentinel(p.sentinelPath, sentinel); err != nil {
		return nil, fmt.Errorf("writing sentinel: %w", err)
	}
	return sentinel, nil
}

// migrateStoreRetryAware wraps migrateStore with the "is this destination
// already ours" check a failed→retry cycle needs: if newDBPath exists but
// the prior attempt did not report "store" as a conflict, that file is this
// package's own earlier output (the prior attempt's verification failed for
// an unrelated reason, e.g. fleet), not a genuine conflict — so it is left
// in place, treated as already migrated, and its source counts are
// recomputed fresh (the source is read-only, so re-vacuuming it into a
// throwaway snapshot purely to count rows is always safe) so verifyStore
// still has something to check on a retry.
func migrateStoreRetryAware(p paths, hasPrior bool, priorConflicts map[string]bool) (storeMigrationResult, error) {
	if hasPrior && fileExists(p.oldDBPath) && fileExists(p.newDBPath) && !priorConflicts["store"] {
		counts, err := sourceRowCountsOnly(p.oldDBPath)
		if err != nil {
			return storeMigrationResult{}, err
		}
		counts.Migrated = true
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

// migrateFleetRetryAware wraps migrateFleet with the same "is this ours
// already" check migrateStoreRetryAware makes: a <task>/<repo> directory
// that already exists at the destination but was not reported as a conflict
// by the prior attempt is this package's own earlier copy, so it is
// resubmitted for verification rather than reported as a fresh conflict or
// silently ignored.
func migrateFleetRetryAware(p paths, hasPrior bool, priorConflicts map[string]bool) (fleetMigrationResult, error) {
	if !dirExists(p.oldFleetRoot) {
		return fleetMigrationResult{}, nil
	}

	var result fleetMigrationResult
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
			srcDir := filepath.Join(taskDir, repo)
			dstDir := filepath.Join(p.newFleetRoot, task, repo)

			if dirExists(dstDir) {
				if hasPrior && !priorConflicts["fleet:"+item.String()] {
					// Already copied by an earlier attempt of ours;
					// resubmit for verification rather than re-copying.
					result.Migrated = append(result.Migrated, item)
					continue
				}
				result.Conflicts = append(result.Conflicts, "fleet:"+item.String())
				continue
			}

			if err := copyTree(srcDir, dstDir); err != nil {
				return result, fmt.Errorf("copying worktree %s to %s: %w", srcDir, dstDir, err)
			}
			result.Migrated = append(result.Migrated, item)
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
