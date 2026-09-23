// Package upgrademigrate migrates a pre-rebrand "v2" installation of this
// tool (config YAML under ~/.config/sergeant, its SQLite store at
// ~/.local/share/sergeant/sergeant.db, and isolated fleet worktrees under
// ~/.local/share/sergeant-v2/fleet) into today's rebranded layout
// (~/.config/sgt, ~/.local/share/sgt/sgt.db, ~/.local/share/sgt-v2/fleet),
// per docs/prd-upgrade-migration.md and issue #15.
//
// v1's actual fleet, ~/.local/share/sergeant/fleet, is never computed or
// referenced anywhere in this package. That directory belongs to the
// historical v1 fleet layout (per-repo metadata, not a git worktree) and is
// explicitly out of scope — see internal/dag/engine.go's FleetRoot doc
// comment for the definitive statement of why the two layouts, despite
// sharing a directory prefix, are not the same thing.
package upgrademigrate

import (
	"fmt"
	"os"
	"path/filepath"
)

// paths is resolved once per call (never cached at package init) so tests
// can point every path at a fixture tree via t.Setenv, the same way
// internal/config and internal/dag already do for their own paths.
type paths struct {
	oldConfigDir string // ~/.config/sergeant
	oldDBPath    string // ~/.local/share/sergeant/sergeant.db
	oldFleetRoot string // ~/.local/share/sergeant-v2/fleet

	newConfigDir string // ~/.config/sgt
	newDBPath    string // ~/.local/share/sgt/sgt.db
	newFleetRoot string // ~/.local/share/sgt-v2/fleet

	sentinelPath string // ~/.config/sgt/.migration-from-sergeant.json
}

// sentinelFileName is the sentinel's basename inside newConfigDir.
const sentinelFileName = ".migration-from-sergeant.json"

// resolvePaths mirrors the exact os.UserHomeDir()-based resolution
// internal/config/config.go, internal/config/list.go, internal/store/store.go,
// and internal/dag/engine.go already use for the destination paths, honoring
// the same override env vars those packages already recognize
// (SGT_CONFIG, SGT_FLEET_DIR) so this package can never disagree with them
// about where the current (post-rebrand) config/fleet roots are — a
// migration that wrote config files where config.ListProjects() does not
// look would be worse than not migrating at all.
//
// SGT_DB_PATH is new: internal/store.Open takes an explicit path from each
// caller in cmd/sgt/main.go rather than resolving one itself, so there is no
// existing override to mirror for the destination database. It exists
// purely as a test seam for this package's own unit tests; production
// callers never set it, so newDBPath still resolves to the same
// ~/.local/share/sgt/sgt.db every other entrypoint hard-codes.
//
// SGT_OLD_CONFIG_DIR, SGT_OLD_DB_PATH, and SGT_OLD_FLEET_ROOT are also new:
// the pre-rebrand paths never had an override before this package existed
// (nothing else in the codebase ever reads them). They exist so tests never
// need to fake $HOME to point this package at a fixture tree, following the
// same "narrow env var per path" pattern SGT_CONFIG/SGT_FLEET_DIR already
// established rather than introducing a different mechanism.
func resolvePaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, fmt.Errorf("resolving home directory: %w", err)
	}

	p := paths{
		oldConfigDir: envOr("SGT_OLD_CONFIG_DIR", filepath.Join(home, ".config", "sergeant")),
		oldDBPath:    envOr("SGT_OLD_DB_PATH", filepath.Join(home, ".local", "share", "sergeant", "sergeant.db")),
		oldFleetRoot: envOr("SGT_OLD_FLEET_ROOT", filepath.Join(home, ".local", "share", "sergeant-v2", "fleet")),

		newConfigDir: envOr("SGT_CONFIG", filepath.Join(home, ".config", "sgt")),
		newDBPath:    envOr("SGT_DB_PATH", filepath.Join(home, ".local", "share", "sgt", "sgt.db")),
		newFleetRoot: envOr("SGT_FLEET_DIR", filepath.Join(home, ".local", "share", "sgt-v2", "fleet")),
	}
	p.sentinelPath = filepath.Join(p.newConfigDir, sentinelFileName)
	return p, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
