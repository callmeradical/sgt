package repopolicy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMiseInstallLinksWikiDigestAndBuildsSgt runs mise.toml's real
// [tasks.install] script (extracted, not reimplemented). It builds the real
// bin/sgt binary in this checkout as a side effect -- that is the
// behavior under test, not an accident of the harness, so it is preserved
// rather than redirected into a throwaway copy.
func TestMiseInstallLinksWikiDigestAndBuildsSgt(t *testing.T) {
	root := repoRoot(t)
	installScript := writeExecutableScript(t, t.TempDir(), "install.sh", extractMiseTaskScript(t, root, "[tasks.install]"))

	testRoot := t.TempDir()
	binDir := filepath.Join(testRoot, "bin")
	pluginsDir := filepath.Join(testRoot, "home", ".config", "opencode", "plugins")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pluginsDir, err)
	}

	// Stale oc-inject symlinks (deleted in GH #179) that mise run install
	// must remove, referencing paths that no longer exist in the repo.
	if err := os.Symlink("/tmp/nonexistent/oc-inject", filepath.Join(binDir, "oc-inject")); err != nil {
		t.Fatalf("creating stale oc-inject symlink: %v", err)
	}
	if err := os.Symlink("/tmp/nonexistent/oc-inject.js", filepath.Join(pluginsDir, "oc-inject.js")); err != nil {
		t.Fatalf("creating stale oc-inject.js symlink: %v", err)
	}

	// go build (invoked by the install script below) resolves GOMODCACHE from
	// $HOME when unset, which this test overrides to a throwaway dir — so
	// without this, a cold cache downloads modules into t.TempDir()'s own
	// tree. The go toolchain extracts a module's cache entry read-only,
	// directories included, which t.TempDir()'s own cleanup (plain
	// os.RemoveAll) cannot remove: unlinking an entry needs write permission
	// on its *parent* directory, and the extracted module directories don't
	// have it. `go clean -modcache` knows how to fix that; registering it as
	// a cleanup — after testRoot's own t.TempDir() call above, so it runs
	// first (t.Cleanup is LIFO) — clears the tree before testRoot's cleanup
	// ever has to touch it. GOMODCACHE stays pinned to this throwaway dir
	// throughout, so the operator's real module cache is never touched.
	goModCache := filepath.Join(testRoot, "gomodcache")
	t.Cleanup(func() {
		cleanCmd := exec.Command("go", "clean", "-modcache")
		cleanCmd.Env = append(os.Environ(), "GOMODCACHE="+goModCache)
		if out, err := cleanCmd.CombinedOutput(); err != nil {
			t.Logf("cleaning isolated GOMODCACHE %s: %v\n%s", goModCache, err, out)
		}
	})

	cmd := exec.Command("bash", installScript)
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(testRoot, "home"),
		"MISE_PROJECT_ROOT="+root,
		"MISE_ORIGINAL_CWD="+root,
		"SGT_INSTALL_DIR="+binDir,
		"GOMODCACHE="+goModCache,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mise run install failed: %v\n%s", err, out)
	}

	if _, err := os.Lstat(filepath.Join(binDir, "wiki-daily-digest")); err != nil {
		t.Errorf("wiki-daily-digest was not installed by mise run install: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "sgt-dispatch")); err == nil {
		t.Error("v1 script sgt-dispatch was installed by mise run install; v1 is removed on this branch")
	}
	if _, err := os.Lstat(filepath.Join(binDir, "oc-inject")); err == nil {
		t.Error("stale oc-inject symlink was not removed by mise run install")
	}
	if _, err := os.Lstat(filepath.Join(pluginsDir, "oc-inject.js")); err == nil {
		t.Error("stale oc-inject.js symlink was not removed by mise run install")
	}

	info, err := os.Stat(filepath.Join(root, "bin", "sgt"))
	if err != nil {
		t.Fatalf("mise run install did not build bin/sgt: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("bin/sgt was built but is not executable")
	}
}
