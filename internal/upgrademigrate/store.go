package upgrademigrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/callmeradical/sgt/internal/store"
)

// storeMigrationResult records what migrateStore did, so the caller's
// verification pass knows whether there is a freshly migrated database to
// check at all.
type storeMigrationResult struct {
	// Migrated is true when oldDBPath was copied into newDBPath.
	Migrated bool
	// Conflict is "store" when newDBPath already existed; otherwise empty.
	Conflict string
	// SourceRuns/SourcePhases are the row counts read from the VACUUM INTO
	// snapshot before it was moved into place — captured here, not
	// recomputed later, so verify reads the exact same snapshot rather than
	// re-querying a live (and by then already schema-migrated) destination
	// against a description of the source that might have moved on.
	SourceRuns   int64
	SourcePhases int64
}

// migrateStore makes oldDBPath's data available at newDBPath, WAL-safely.
//
// oldDBPath is never copied or renamed directly: SQLite's WAL journal mode
// (internal/store/store.go's Open) means a committed write can live only in
// oldDBPath+"-wal" until the next checkpoint, so copying the main file alone
// can silently drop the most recent writes. Instead, SQLite's own
// VACUUM INTO is run against oldDBPath to produce one consistent,
// WAL-flushed snapshot file with no separate -wal/-shm siblings, and that
// snapshot is what gets moved into place.
//
// If newDBPath already exists, the whole step is skipped and reported as a
// single "store" conflict — no row-level merge of two independent run
// histories is attempted (see design.md's Non-Goals).
//
// The moved snapshot is then opened once through store.Open so its existing
// migrate() family brings its schema fully current, exactly as it would for
// any long-lived sgt.db — this package adds no schema-handling code of its
// own.
func migrateStore(p paths) (storeMigrationResult, error) {
	var result storeMigrationResult

	if !fileExists(p.oldDBPath) {
		return result, nil
	}
	if fileExists(p.newDBPath) {
		result.Conflict = "store"
		return result, nil
	}

	if err := os.MkdirAll(filepath.Dir(p.newDBPath), 0o755); err != nil {
		return result, fmt.Errorf("creating %s: %w", filepath.Dir(p.newDBPath), err)
	}

	// The snapshot is written into a fresh temp directory alongside the
	// destination (same filesystem, so the final move is an atomic rename)
	// rather than to a pre-chosen filename: VACUUM INTO refuses to write to
	// a path that already exists, and a fixed name could collide with a
	// half-cleaned-up previous attempt.
	tmpDir, err := os.MkdirTemp(filepath.Dir(p.newDBPath), "sgt-migrate-*")
	if err != nil {
		return result, fmt.Errorf("creating temp snapshot dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	snapshotPath := filepath.Join(tmpDir, "snapshot.db")

	if err := vacuumInto(p.oldDBPath, snapshotPath); err != nil {
		return result, fmt.Errorf("snapshotting %s: %w", p.oldDBPath, err)
	}

	// Row counts are read from the snapshot now, before it is moved and
	// before store.Open runs the schema-migration family against it, so
	// verify compares against the source's own state rather than a
	// description of the destination reflecting itself.
	result.SourceRuns, err = countRows(snapshotPath, "runs")
	if err != nil {
		return result, fmt.Errorf("counting runs in snapshot: %w", err)
	}
	result.SourcePhases, err = countRows(snapshotPath, "phases")
	if err != nil {
		return result, fmt.Errorf("counting phases in snapshot: %w", err)
	}

	if err := os.Rename(snapshotPath, p.newDBPath); err != nil {
		return result, fmt.Errorf("moving snapshot to %s: %w", p.newDBPath, err)
	}

	// Opening through store.Open exercises the existing migrate()/
	// migrateAddTables()/migrateAddColumns()/migrateAddIndexes() family so
	// the migrated database's schema is fully current, then closes it —
	// this package never queries it again with the connection still open.
	st, err := store.Open(p.newDBPath)
	if err != nil {
		return result, fmt.Errorf("opening migrated store %s: %w", p.newDBPath, err)
	}
	if err := st.Close(); err != nil {
		return result, fmt.Errorf("closing migrated store %s: %w", p.newDBPath, err)
	}

	result.Migrated = true
	return result, nil
}

// vacuumInto opens srcPath (a live sqlite database, possibly WAL-mode with
// pending writes in its -wal sidecar) and runs VACUUM INTO against it. This
// reads the database's current logical state — the base file merged with
// anything in its WAL — the same way any ordinary connection to it would,
// and writes that state out as one plain, fully checkpointed file at
// dstPath. dstPath must not already exist.
func vacuumInto(srcPath, dstPath string) error {
	db, err := sql.Open("sqlite", srcPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()

	// VACUUM INTO's target is a string literal in SQLite's grammar, not a
	// bindable parameter, so it is quoted by hand: single quotes doubled,
	// the same escaping SQL string literals always use.
	quoted := "'" + strings.ReplaceAll(dstPath, "'", "''") + "'"
	if _, err := db.Exec("VACUUM INTO " + quoted); err != nil {
		return err
	}
	return nil
}

// countRows returns COUNT(*) for table in the sqlite database at dbPath,
// opened as a fresh, independent connection — never the connection any
// migration step already had open — so this is a genuine re-read of what
// ended up on disk.
func countRows(dbPath, table string) (int64, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var n int64
	// table is always one of this package's own two literal names ("runs",
	// "phases"), never caller input, so building the query with fmt.Sprintf
	// is safe here — sqlite does not support binding identifiers.
	err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n)
	return n, err
}
