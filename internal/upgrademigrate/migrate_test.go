package upgrademigrate

// Tests for internal/upgrademigrate, matching design.md's "Test shape"
// section: every fixture here is real (real YAML files, a real sqlite db
// built through internal/store's own schema, a real git worktree built with
// `git init`/`git worktree add`), and every assertion independently
// re-reads the destination from disk rather than trusting Run()'s own
// returned Sentinel — the Sentinel is checked too, but never as the only
// evidence.

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/store"
)

// fixture is one full pre-rebrand v2 installation, rooted under a
// t.TempDir() fake $HOME equivalent: every path this package resolves is
// pointed here via the SGT_OLD_*/SGT_CONFIG/SGT_DB_PATH/SGT_FLEET_DIR
// overrides paths.go recognizes, so nothing under the real home directory
// is ever touched.
type fixture struct {
	oldConfigDir string
	oldDBPath    string
	oldFleetRoot string
	newConfigDir string
	newDBPath    string
	newFleetRoot string
}

// newFixture points every path this package resolves at a fresh temp tree,
// without creating any of the directories themselves — callers create
// exactly the pieces their scenario needs, so "the source config dir
// doesn't exist at all" remains a reachable, distinct case from "it exists
// and is empty".
func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		oldConfigDir: filepath.Join(root, "old-config"),
		oldDBPath:    filepath.Join(root, "old-share", "sergeant", "sergeant.db"),
		oldFleetRoot: filepath.Join(root, "old-share", "sergeant-v2", "fleet"),
		newConfigDir: filepath.Join(root, "new-config"),
		newDBPath:    filepath.Join(root, "new-share", "sgt", "sgt.db"),
		newFleetRoot: filepath.Join(root, "new-share", "sgt-v2", "fleet"),
	}
	t.Setenv("SGT_OLD_CONFIG_DIR", f.oldConfigDir)
	t.Setenv("SGT_OLD_DB_PATH", f.oldDBPath)
	t.Setenv("SGT_OLD_FLEET_ROOT", f.oldFleetRoot)
	t.Setenv("SGT_CONFIG", f.newConfigDir)
	t.Setenv("SGT_DB_PATH", f.newDBPath)
	t.Setenv("SGT_FLEET_DIR", f.newFleetRoot)
	return f
}

func (f fixture) writeConfigFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(f.oldConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.oldConfigDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildOldStore builds a real sergeant.db at f.oldDBPath through
// internal/store's own schema (the rename that created this gap was
// schema-neutral, so pre-rebrand v2's schema is exactly today's) and writes
// one run and one phase into it.
func (f fixture) buildOldStore(t *testing.T) {
	t.Helper()
	st, err := store.Open(f.oldDBPath)
	if err != nil {
		t.Fatalf("building fixture store: %v", err)
	}
	if err := st.CreateRun(&store.RunRecord{ID: "run-1", Project: "fooproj", TaskID: "run-1", Status: "passed"}); err != nil {
		t.Fatalf("seeding fixture run: %v", err)
	}
	if err := st.RecordPhase(&store.PhaseRecord{ID: "phase-1", RunID: "run-1", Repo: "r1", Name: "build", Kind: "code", Status: "passed"}); err != nil {
		t.Fatalf("seeding fixture phase: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("closing fixture store: %v", err)
	}
}

// buildFleetWorktree creates a real origin git repo (outside the fleet
// root entirely, standing in for the operator's real checkout) and a real
// linked `git worktree add` checkout of it at
// <f.oldFleetRoot>/<task>/<repo>, matching internal/dag.FleetDir's own
// layout and creation method exactly.
func (f fixture) buildFleetWorktree(t *testing.T, task, repo string) (originDir, worktreeDir string) {
	t.Helper()
	originDir = filepath.Join(t.TempDir(), "origin-"+repo)
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "init", "-q")
	runGit(t, originDir, "config", "user.email", "t@example.com")
	runGit(t, originDir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(originDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "add", "README.md")
	runGit(t, originDir, "commit", "-q", "-m", "init")

	worktreeDir = filepath.Join(f.oldFleetRoot, task, repo)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "worktree", "add", "-b", "feature/"+task+"-"+repo, worktreeDir, "HEAD")
	return originDir, worktreeDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// mtime returns path's modification time, failing the test if it cannot be
// stat'd.
func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

// --- Scenario: full migration, independently re-read ---

func TestRunMigratesFullFixtureIndependentlyVerifiable(t *testing.T) {
	f := newFixture(t)
	f.writeConfigFile(t, "fooproj.yaml", "repos:\n  - name: r1\n    path: /tmp/does-not-need-to-exist\n")
	f.buildOldStore(t)
	originDir, _ := f.buildFleetWorktree(t, "run-1", "r1")

	sentinel, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sentinel == nil {
		t.Fatal("Run returned nil sentinel for a fixture with real pre-rebrand v2 state")
	}
	if sentinel.Status != StatusVerified {
		t.Fatalf("Status = %q, want %q; conflicts=%v mismatches=%v", sentinel.Status, StatusVerified, sentinel.Conflicts, sentinel.Mismatches)
	}
	if len(sentinel.Conflicts) != 0 {
		t.Errorf("unexpected conflicts on a clean fixture: %v", sentinel.Conflicts)
	}

	// Independently re-read the config file.
	srcData, _ := os.ReadFile(filepath.Join(f.oldConfigDir, "fooproj.yaml"))
	dstData, err := os.ReadFile(filepath.Join(f.newConfigDir, "fooproj.yaml"))
	if err != nil {
		t.Fatalf("reading migrated config: %v", err)
	}
	if !bytes.Equal(srcData, dstData) {
		t.Errorf("migrated config content differs from source")
	}

	// Independently re-open the migrated store — a brand new *store.Store,
	// not anything Run() used internally.
	st, err := store.Open(f.newDBPath)
	if err != nil {
		t.Fatalf("opening migrated store: %v", err)
	}
	defer st.Close()
	run, err := st.GetRun("run-1")
	if err != nil {
		t.Fatalf("migrated store missing run-1: %v", err)
	}
	if run.Project != "fooproj" || run.Status != "passed" {
		t.Errorf("migrated run = %+v, want project=fooproj status=passed", run)
	}
	phases, err := st.ListPhasesForRun("run-1")
	if err != nil || len(phases) != 1 || phases[0].ID != "phase-1" {
		t.Errorf("migrated phases = %+v, err=%v, want exactly phase-1", phases, err)
	}

	// Independently re-check the fleet worktree's git state.
	dstWorktree := filepath.Join(f.newFleetRoot, "run-1", "r1")
	wantHead := gitOutput(t, originDir, "rev-parse", "HEAD")
	gotHead := gitOutput(t, dstWorktree, "rev-parse", "HEAD")
	if wantHead != gotHead {
		t.Errorf("migrated worktree HEAD = %q, want %q", gotHead, wantHead)
	}
	gotStatus := gitOutput(t, dstWorktree, "status", "--porcelain")
	if gotStatus != "" {
		t.Errorf("migrated worktree has unexpected status --porcelain output: %q", gotStatus)
	}
}

// --- Scenario: idempotency ---

func TestRunIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.writeConfigFile(t, "fooproj.yaml", "repos:\n  - name: r1\n    path: /tmp/x\n")
	f.buildOldStore(t)
	f.buildFleetWorktree(t, "run-1", "r1")

	first, err := Run()
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Status != StatusVerified {
		t.Fatalf("first Run status = %q, want verified: %+v", first.Status, first)
	}

	cfgMtime := mtime(t, filepath.Join(f.newConfigDir, "fooproj.yaml"))
	dbMtime := mtime(t, f.newDBPath)
	fleetMtime := mtime(t, filepath.Join(f.newFleetRoot, "run-1", "r1", "README.md"))

	second, err := Run()
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second == nil {
		t.Fatal("second Run returned nil, want the verified sentinel unchanged")
	}
	if !second.Timestamp.Equal(first.Timestamp) || second.Status != first.Status {
		t.Errorf("second Run() = %+v, want the identical prior sentinel %+v (no re-write)", second, first)
	}

	if got := mtime(t, filepath.Join(f.newConfigDir, "fooproj.yaml")); !got.Equal(cfgMtime) {
		t.Errorf("config file was rewritten on the second, already-verified Run() call")
	}
	if got := mtime(t, f.newDBPath); !got.Equal(dbMtime) {
		t.Errorf("db file was rewritten on the second, already-verified Run() call")
	}
	if got := mtime(t, filepath.Join(f.newFleetRoot, "run-1", "r1", "README.md")); !got.Equal(fleetMtime) {
		t.Errorf("fleet worktree file was rewritten on the second, already-verified Run() call")
	}
}

// --- Scenario: v1 fleet is never touched ---

func TestV1FleetNeverMigratedOrTouched(t *testing.T) {
	f := newFixture(t)

	// v1's actual fleet: NOT part of paths, built directly next to (but
	// outside of) the paths this package resolves, at the literal
	// ~/.local/share/sergeant/fleet shape (same parent as oldDBPath).
	v1FleetRoot := filepath.Join(filepath.Dir(filepath.Dir(f.oldDBPath)), "sergeant", "fleet")
	v1Item := filepath.Join(v1FleetRoot, "v1task", "v1repo")
	if err := os.MkdirAll(v1Item, 0o755); err != nil {
		t.Fatal(err)
	}
	v1Marker := filepath.Join(v1Item, "v1-metadata.json")
	if err := os.WriteFile(v1Marker, []byte(`{"v1":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v1MarkerData, _ := os.ReadFile(v1Marker)
	v1MarkerMtimeBefore := mtime(t, v1Marker)

	// Pre-rebrand v2's fleet: in scope, must be migrated.
	f.buildFleetWorktree(t, "v2task", "v2repo")

	sentinel, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sentinel == nil || sentinel.Status != StatusVerified {
		t.Fatalf("Run() = %+v, want a verified sentinel", sentinel)
	}

	// The v2 item must be present at the destination.
	if !dirExists(filepath.Join(f.newFleetRoot, "v2task", "v2repo")) {
		t.Errorf("pre-rebrand v2 worktree was not migrated")
	}

	// The v1 item must NOT appear anywhere under the new fleet root.
	if dirExists(filepath.Join(f.newFleetRoot, "v1task")) {
		t.Errorf("v1 fleet state leaked into the new fleet root")
	}

	// v1's source must be byte-for-byte untouched.
	gotData, err := os.ReadFile(v1Marker)
	if err != nil {
		t.Fatalf("v1 marker file disappeared: %v", err)
	}
	if !bytes.Equal(gotData, v1MarkerData) {
		t.Errorf("v1 marker file content changed")
	}
	if !mtime(t, v1Marker).Equal(v1MarkerMtimeBefore) {
		t.Errorf("v1 marker file was written to")
	}
}

// --- Scenario: per-item conflicts ---

func TestConflictsAreReportedAndLeaveBothSidesUntouched(t *testing.T) {
	f := newFixture(t)

	// Config conflict: destination already has a differing foo.yaml.
	f.writeConfigFile(t, "foo.yaml", "repos:\n  - name: r1\n    path: /tmp/src\n")
	f.writeConfigFile(t, "bar.yaml", "repos:\n  - name: r1\n    path: /tmp/src2\n") // unrelated, non-conflicting
	if err := os.MkdirAll(f.newConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conflictingDest := []byte("repos:\n  - name: r1\n    path: /tmp/DIFFERENT\n")
	if err := os.WriteFile(filepath.Join(f.newConfigDir, "foo.yaml"), conflictingDest, 0o644); err != nil {
		t.Fatal(err)
	}

	// Store conflict: destination db already exists (a real, distinct db).
	f.buildOldStore(t)
	if err := os.MkdirAll(filepath.Dir(f.newDBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexistingStore, err := store.Open(f.newDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := preexistingStore.CreateRun(&store.RunRecord{ID: "preexisting-run", Project: "other", TaskID: "preexisting-run", Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	if err := preexistingStore.Close(); err != nil {
		t.Fatal(err)
	}

	// Fleet conflict: destination worktree dir already exists.
	f.buildFleetWorktree(t, "run-1", "r1")
	conflictingWorktree := filepath.Join(f.newFleetRoot, "run-1", "r1")
	if err := os.MkdirAll(conflictingWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictingWorktree, "marker"), []byte("preexisting"), 0o644); err != nil {
		t.Fatal(err)
	}

	sentinel, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantConflicts := map[string]bool{"config:foo.yaml": true, "store": true, "fleet:run-1/r1": true}
	if len(sentinel.Conflicts) != len(wantConflicts) {
		t.Fatalf("Conflicts = %v, want exactly %v", sentinel.Conflicts, wantConflicts)
	}
	for _, c := range sentinel.Conflicts {
		if !wantConflicts[c] {
			t.Errorf("unexpected conflict %q", c)
		}
	}

	// The conflicting destination items must be untouched.
	gotFoo, _ := os.ReadFile(filepath.Join(f.newConfigDir, "foo.yaml"))
	if !bytes.Equal(gotFoo, conflictingDest) {
		t.Errorf("conflicting config file was modified")
	}
	stillThere, err := store.Open(f.newDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stillThere.Close()
	if _, err := stillThere.GetRun("preexisting-run"); err != nil {
		t.Errorf("preexisting destination run was lost: %v", err)
	}
	if _, err := stillThere.GetRun("run-1"); err == nil {
		t.Errorf("source run leaked into a conflicting destination store")
	}
	if _, err := os.Stat(filepath.Join(conflictingWorktree, "marker")); err != nil {
		t.Errorf("conflicting worktree marker file disappeared: %v", err)
	}

	// The unrelated, non-conflicting config file must still have migrated.
	if !fileExists(filepath.Join(f.newConfigDir, "bar.yaml")) {
		t.Errorf("unrelated non-conflicting config file did not migrate")
	}
}

// --- Scenario: WAL-safety ---

func TestWALOnlyWriteSurvivesMigration(t *testing.T) {
	f := newFixture(t)
	f.buildOldStore(t) // creates schema + one checkpointed baseline row

	// A second, independent connection with autocheckpoint disabled and no
	// forced checkpoint: this insert lives only in sergeant.db-wal for as
	// long as this connection (or any connection) stays open on the file.
	dsn := f.oldDBPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"
	walDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening raw wal connection: %v", err)
	}
	defer walDB.Close()

	now := time.Now().UTC()
	_, err = walDB.Exec(
		`INSERT INTO runs (id, project, task_id, status, brief, change_id, type, intent_id, slug, request_id, base_branch, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', '', '', '', ?, NULL, '', ?, ?)`,
		"wal-only-run", "fooproj", "wal-only-run", "running", "wal-only-run", now, now,
	)
	if err != nil {
		t.Fatalf("inserting wal-only row: %v", err)
	}
	if _, err := os.Stat(f.oldDBPath + "-wal"); err != nil {
		t.Fatalf("expected a -wal sidecar file to exist before migration: %v", err)
	}

	// walDB is deliberately still open here: closing it could trigger
	// sqlite's own final-checkpoint-on-last-close behavior, which would
	// defeat the point of this test by folding the write into the main
	// file before migration ever runs.
	sentinel, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sentinel.Status != StatusVerified {
		t.Fatalf("Status = %q, want verified: conflicts=%v mismatches=%v", sentinel.Status, sentinel.Conflicts, sentinel.Mismatches)
	}

	st, err := store.Open(f.newDBPath)
	if err != nil {
		t.Fatalf("opening migrated store: %v", err)
	}
	defer st.Close()
	if _, err := st.GetRun("wal-only-run"); err != nil {
		t.Errorf("the WAL-only write did not survive migration: %v", err)
	}
	if _, err := st.GetRun("run-1"); err != nil {
		t.Errorf("the already-checkpointed baseline row did not survive migration: %v", err)
	}
}

// --- Scenario: failed verification leaves the destination in place, then a
// subsequent Run() reaches verified once the fault clears ---

func TestFailedVerificationThenRetryReachesVerified(t *testing.T) {
	f := newFixture(t)
	f.writeConfigFile(t, "fooproj.yaml", "repos:\n  - name: r1\n    path: /tmp/x\n")
	f.buildOldStore(t)
	f.buildFleetWorktree(t, "run-1", "r1")

	// Force a verification mismatch on the first attempt without corrupting
	// any real git/sqlite state, via the package's own test seam.
	verifyHook = func(mismatches []string) []string {
		return append(mismatches, "induced-test-mismatch: forcing a failure")
	}
	first, err := Run()
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first == nil || first.Status != StatusFailed {
		t.Fatalf("first Run() = %+v, want Status failed", first)
	}
	foundInduced := false
	for _, m := range first.Mismatches {
		if m == "induced-test-mismatch: forcing a failure" {
			foundInduced = true
		}
	}
	if !foundInduced {
		t.Fatalf("Mismatches = %v, want the induced mismatch named", first.Mismatches)
	}

	// The destination is left in place (no rollback): the config file and
	// store and fleet worktree copies made during the failed attempt must
	// still be there.
	if !fileExists(filepath.Join(f.newConfigDir, "fooproj.yaml")) {
		t.Errorf("destination config was removed after a failed migration")
	}
	if !fileExists(f.newDBPath) {
		t.Errorf("destination db was removed after a failed migration")
	}
	if !dirExists(filepath.Join(f.newFleetRoot, "run-1", "r1")) {
		t.Errorf("destination worktree was removed after a failed migration")
	}

	// Clear the induced fault and retry: the automatic-retry contract
	// (Decision 6) says a failed sentinel gets retried on the very next
	// call, and this fixture's underlying state was never actually broken,
	// so it must now reach verified.
	verifyHook = func(mismatches []string) []string { return mismatches }
	t.Cleanup(func() { verifyHook = func(mismatches []string) []string { return mismatches } })

	second, err := Run()
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second == nil || second.Status != StatusVerified {
		t.Fatalf("second Run() = %+v, want Status verified once the fault is cleared", second)
	}
	if len(second.Mismatches) != 0 {
		t.Errorf("Mismatches on the successful retry = %v, want none", second.Mismatches)
	}
}

// --- Sentinel / detection basics ---

func TestRunNoOpWhenNothingToMigrate(t *testing.T) {
	newFixture(t) // nothing written under any old path

	sentinel, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sentinel != nil {
		t.Errorf("Run() = %+v, want nil when no pre-rebrand v2 state and no sentinel exist", sentinel)
	}
}

func TestLastResultNeverTriggersMigration(t *testing.T) {
	f := newFixture(t)
	f.writeConfigFile(t, "fooproj.yaml", "repos:\n  - name: r1\n    path: /tmp/x\n")

	sentinel, err := LastResult()
	if err != nil {
		t.Fatalf("LastResult: %v", err)
	}
	if sentinel != nil {
		t.Fatalf("LastResult() = %+v, want nil before Run() has ever executed", sentinel)
	}
	if fileExists(filepath.Join(f.newConfigDir, "fooproj.yaml")) {
		t.Errorf("LastResult() migrated a config file; it must never write anything")
	}
}
