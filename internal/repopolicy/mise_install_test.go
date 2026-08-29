package repopolicy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	cmd := exec.Command("bash", installScript)
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(testRoot, "home"),
		"MISE_PROJECT_ROOT="+root,
		"MISE_ORIGINAL_CWD="+root,
		"SGT_INSTALL_DIR="+binDir,
		// The install script's own `go build` resolves GOPATH/GOMODCACHE/
		// GOCACHE from HOME by default. Overriding HOME above (so this test
		// doesn't touch the real ~/.local/bin) would otherwise make that
		// build download and extract the entire module graph fresh into a
		// throwaway location under the fake HOME on every run. Go marks
		// extracted module directories read-only, which t.TempDir()'s
		// automatic os.RemoveAll cleanup cannot remove -- the recurring
		// flake this pins down. Pointing these at the real, shared cache
		// instead means the build reuses what's already there and leaves
		// nothing new for cleanup to fail on.
		"GOPATH="+goEnv(t, "GOPATH"),
		"GOMODCACHE="+goEnv(t, "GOMODCACHE"),
		"GOCACHE="+goEnv(t, "GOCACHE"),
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

// goEnv reads the real, ambient value of a go env var (e.g. GOMODCACHE) from
// the environment this test process itself runs in -- not the fake HOME the
// install script's subprocess runs under -- so that subprocess's own `go
// build` can be pointed at the same, already-populated shared cache.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}
