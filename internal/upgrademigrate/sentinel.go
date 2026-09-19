package upgrademigrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Sentinel is the on-disk record of the last migration attempt, written to
// <newConfigDir>/.migration-from-sergeant.json (Decision 6).
type Sentinel struct {
	// Status is "verified" or "failed". There is no third value: a migration
	// that found nothing to do never writes a sentinel at all (Run returns
	// nil, nil), so a sentinel's mere existence already means an attempt ran.
	Status      string    `json:"status"`
	SourcePaths []string  `json:"source_paths"`
	Timestamp   time.Time `json:"timestamp"`
	// Conflicts names each per-item skip: "config:<filename>", "store",
	// "fleet:<task>/<repo>". A non-empty Conflicts is not itself a failure —
	// it can coexist with Status == "verified".
	Conflicts []string `json:"conflicts,omitempty"`
	// Mismatches is only ever set when Status == "failed": the specific
	// post-migration verification checks that did not match the source.
	Mismatches []string `json:"mismatches,omitempty"`
}

// StatusVerified and StatusFailed are the two Sentinel.Status values this
// package ever writes.
const (
	StatusVerified = "verified"
	StatusFailed   = "failed"
)

// LastResult reads the sentinel file without triggering migration and
// without scanning any pre-rebrand v2 source path. internal/ui's dashboard
// status surface calls this, never Run, so that loading the dashboard can
// never itself perform a filesystem copy.
func LastResult() (*Sentinel, error) {
	p, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	return readSentinel(p.sentinelPath)
}

// readSentinel returns nil, nil when no sentinel file exists yet.
func readSentinel(path string) (*Sentinel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s Sentinel
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// writeSentinel writes s to path atomically (write to a temp file in the
// same directory, then rename) so a reader (LastResult, the dashboard) never
// observes a half-written sentinel.
func writeSentinel(path string, s *Sentinel) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sentinel-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// Detected reports whether any recognized pre-rebrand v2 state exists on
// disk: config YAML under oldConfigDir, a database at oldDBPath, or worktree
// directories under oldFleetRoot. It is never true because of v1's actual
// fleet (~/.local/share/sergeant/fleet) alone — that path is not part of
// paths and is never consulted here.
func Detected() (bool, error) {
	p, err := resolvePaths()
	if err != nil {
		return false, err
	}
	return detected(p)
}

func detected(p paths) (bool, error) {
	if names, err := configYAMLFiles(p.oldConfigDir); err == nil && len(names) > 0 {
		return true, nil
	}
	if fileExists(p.oldDBPath) {
		return true, nil
	}
	if dirExists(p.oldFleetRoot) {
		return true, nil
	}
	return false, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// configYAMLFiles lists the *.yaml/*.yml basenames directly inside dir, in
// no particular order. A missing dir is not an error: it just has no files.
func configYAMLFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			names = append(names, name)
		}
	}
	return names, nil
}
