package upgrademigrate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// configMigrationResult records what migrateConfig actually did, so the
// caller's verification pass can check exactly the files it copied (or
// found already-migrated) without re-deriving that set from scratch.
type configMigrationResult struct {
	// Migrated holds the basenames that now exist at the destination because
	// of this run: either freshly copied, or already byte-identical from an
	// earlier partial attempt.
	Migrated []string
	// Conflicts holds "config:<filename>" entries, one per destination file
	// that already existed and differed from the source.
	Conflicts []string
}

// migrateConfig copies every *.yaml/*.yml directly inside oldConfigDir to
// newConfigDir under the same filename.
//
//   - No file of that name at the destination: copy it, preserving mode.
//   - A destination file that is byte-identical: treat it as already
//     migrated (an earlier partial run got this one right), not a conflict.
//   - A destination file that differs: record "config:<filename>" as a
//     conflict and leave both source and destination untouched.
//
// A missing oldConfigDir is not an error: there is simply nothing to
// migrate.
func migrateConfig(p paths) (configMigrationResult, error) {
	var result configMigrationResult

	names, err := configYAMLFiles(p.oldConfigDir)
	if err != nil {
		return result, fmt.Errorf("listing %s: %w", p.oldConfigDir, err)
	}
	if len(names) == 0 {
		return result, nil
	}

	if err := os.MkdirAll(p.newConfigDir, 0o755); err != nil {
		return result, fmt.Errorf("creating %s: %w", p.newConfigDir, err)
	}

	for _, name := range names {
		srcPath := filepath.Join(p.oldConfigDir, name)
		dstPath := filepath.Join(p.newConfigDir, name)

		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			return result, fmt.Errorf("reading %s: %w", srcPath, err)
		}

		dstData, err := os.ReadFile(dstPath)
		switch {
		case os.IsNotExist(err):
			if err := copyFile(srcPath, dstPath); err != nil {
				return result, fmt.Errorf("copying %s to %s: %w", srcPath, dstPath, err)
			}
			result.Migrated = append(result.Migrated, name)
		case err != nil:
			return result, fmt.Errorf("reading %s: %w", dstPath, err)
		case bytes.Equal(srcData, dstData):
			// Already migrated by an earlier attempt; not a conflict.
			result.Migrated = append(result.Migrated, name)
		default:
			result.Conflicts = append(result.Conflicts, "config:"+name)
		}
	}
	return result, nil
}

// copyFile copies src to dst, preserving src's file mode. dst must not
// already exist as a directory; if a regular file is already there it is
// overwritten (callers only reach this branch after confirming no
// conflicting destination file exists).
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}
