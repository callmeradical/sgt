package repopolicy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMiseCheckValidatesV2EnginePrerequisites runs mise.toml's real
// [tasks.check] script (extracted, not reimplemented) against a controlled
// PATH of fake stub binaries, so the assertions exercise the actual
// automation an operator runs via `mise run check`.
func TestMiseCheckValidatesV2EnginePrerequisites(t *testing.T) {
	root := repoRoot(t)
	checkScript := writeExecutableScript(t, t.TempDir(), "check.sh", extractMiseTaskScript(t, root, "[tasks.check]"))

	writeRequiredStubs := func(dir string) {
		for _, cmd := range []string{"git", "gh", "openspec"} {
			writeStub(t, dir, cmd, cmd+" version 1.0")
		}
	}

	// runWith runs the check with the required tools plus each named stub
	// on PATH. setup receives the stub dir so a stub can be written there.
	runWith := func(setup func(dir string)) (string, error) {
		dir := t.TempDir()
		writeRequiredStubs(dir)
		if setup != nil {
			setup(dir)
		}
		cmd := exec.Command(checkScript)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+":/usr/bin:/bin:/usr/sbin:/sbin",
			"MISE_PROJECT_ROOT="+root,
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	runCheck := func(agent string) (string, error) {
		return runWith(func(dir string) {
			switch agent {
			case "opencode":
				writeStub(t, dir, "opencode", "OpenCode 1.0")
			case "claude":
				writeClaudeStub(t, dir, usableGauntletPluginList(t))
			}
		})
	}

	t.Run("no supported agent harness fails", func(t *testing.T) {
		out, err := runCheck("none")
		if err == nil {
			t.Errorf("dependency check did not fail when no supported agent harness was present:\n%s", out)
		}
		if !strings.Contains(out, "MISSING") || !strings.Contains(out, "claude opencode oc goose codex pi") {
			t.Errorf("dependency check did not fail when no supported agent harness was present:\n%s", out)
		}
	})

	t.Run("opencode present passes", func(t *testing.T) {
		out, err := runCheck("opencode")
		if err != nil || !strings.Contains(out, "agent") || !strings.Contains(out, "ok   opencode") || !strings.Contains(out, "All v2 prerequisites present.") {
			t.Errorf("dependency check did not pass with a supported agent harness:\n%s (err=%v)", out, err)
		}
	})

	t.Run("a harness other than claude says claude is the default", func(t *testing.T) {
		out, err := runCheck("opencode")
		if err != nil {
			t.Fatalf("dependency check failed with opencode present:\n%s (err=%v)", out, err)
		}
		if !strings.Contains(out, "claude is the default harness") || !strings.Contains(out, "defaults.agent") {
			t.Errorf("dependency check did not say claude is the default and how to pin another harness:\n%s", out)
		}
	})

	t.Run("claude present passes", func(t *testing.T) {
		out, err := runCheck("claude")
		if err != nil || !strings.Contains(out, "ok   claude") || !strings.Contains(out, "All v2 prerequisites present.") {
			t.Errorf("dependency check did not accept Claude:\n%s (err=%v)", out, err)
		}
		if strings.Contains(out, "WARNING") {
			t.Errorf("dependency check warned although claude reports a usable gauntlet plugin:\n%s", out)
		}
		if !strings.Contains(out, "gauntlet") || !strings.Contains(out, "0.1.0") {
			t.Errorf("dependency check did not report the gauntlet plugin version:\n%s", out)
		}
	})

	t.Run("claude is checked first", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeStub(t, dir, "opencode", "OpenCode 1.0")
			writeClaudeStub(t, dir, usableGauntletPluginList(t))
		})
		if err != nil || !strings.Contains(out, "ok   claude") {
			t.Errorf("dependency check did not pick claude ahead of opencode:\n%s (err=%v)", out, err)
		}
		if strings.Contains(out, "ok   opencode") {
			t.Errorf("dependency check reported opencode although claude is present:\n%s", out)
		}
	})

	assertPluginWarning := func(t *testing.T, out string, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Errorf("a gauntlet plugin problem failed the check; it must only warn:\n%s (err=%v)", out, err)
		}
		if !strings.Contains(out, "All v2 prerequisites present.") {
			t.Errorf("a gauntlet plugin problem kept the check from passing:\n%s", out)
		}
		for _, w := range append([]string{"WARNING", "gauntlet_roles: false"}, want...) {
			if !strings.Contains(out, w) {
				t.Errorf("gauntlet plugin warning does not contain %q:\n%s", w, out)
			}
		}
	}

	t.Run("claude with no gauntlet plugin warns without failing", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeClaudeStub(t, dir, `[{"id":"other@market","version":"1.0.0","scope":"user","enabled":true,"installPath":"/nowhere"}]`)
		})
		assertPluginWarning(t, out, err, "no enabled gauntlet plugin", "/plugin install gauntlet@gauntlet")
	})

	t.Run("a disabled gauntlet plugin warns", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeClaudeStub(t, dir, gauntletPluginList(t, "user", false, true))
		})
		assertPluginWarning(t, out, err, "no enabled gauntlet plugin")
	})

	t.Run("a project-scope gauntlet install warns", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeClaudeStub(t, dir, gauntletPluginList(t, "project", true, true))
		})
		assertPluginWarning(t, out, err, "no enabled gauntlet plugin")
	})

	t.Run("a gauntlet install without both roles warns", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeClaudeStub(t, dir, gauntletPluginList(t, "user", true, false))
		})
		assertPluginWarning(t, out, err, "no enabled gauntlet plugin")
	})

	t.Run("an unreadable plugin list warns without failing", func(t *testing.T) {
		out, err := runWith(func(dir string) {
			writeExecutableScript(t, dir, "claude",
				"#!/usr/bin/env bash\nif [[ \"$1\" == plugin ]]; then echo 'not logged in' >&2; exit 1; fi\necho 'Claude Code 1.0'\n")
		})
		assertPluginWarning(t, out, err, "could not read claude's plugin list")
	})

	t.Run("missing openspec fails even with an agent present", func(t *testing.T) {
		dir := t.TempDir()
		for _, cmd := range []string{"git", "gh"} {
			writeStub(t, dir, cmd, cmd+" version 1.0")
		}
		writeStub(t, dir, "opencode", "OpenCode 1.0")
		cmd := exec.Command(checkScript)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+":/usr/bin:/bin:/usr/sbin:/sbin",
			"MISE_PROJECT_ROOT="+root,
		)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "openspec") || !strings.Contains(string(out), "MISSING") {
			t.Errorf("dependency check did not fail when openspec was missing:\n%s (err=%v)", out, err)
		}
	})
}

// writeClaudeStub writes a fake claude that answers `plugin list --json`
// with pluginList and prints a version line for anything else.
func writeClaudeStub(t *testing.T, dir, pluginList string) {
	t.Helper()
	listFile := filepath.Join(t.TempDir(), "plugin-list.json")
	if err := os.WriteFile(listFile, []byte(pluginList), 0o644); err != nil {
		t.Fatalf("writing plugin list: %v", err)
	}
	script := fmt.Sprintf(`#!/usr/bin/env bash
if [[ "$1" == plugin && "$2" == list && "$3" == --json ]]; then
  cat %q
  exit 0
fi
echo 'Claude Code 1.0'
`, listFile)
	writeExecutableScript(t, dir, "claude", script)
}

// usableGauntletPluginList is a plugin list with an enabled, user-scope
// gauntlet 0.1.0 install that carries both role files.
func usableGauntletPluginList(t *testing.T) string {
	return gauntletPluginList(t, "user", true, true)
}

// gauntletPluginList builds a `claude plugin list --json` answer with one
// gauntlet@gauntlet 0.1.0 entry, installed in a temp dir that holds
// agents/alpha.md, and agents/beta.md only when bothRoles is set.
func gauntletPluginList(t *testing.T, scope string, enabled, bothRoles bool) string {
	t.Helper()
	install := t.TempDir()
	if err := os.MkdirAll(filepath.Join(install, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	roles := []string{"alpha.md"}
	if bothRoles {
		roles = append(roles, "beta.md")
	}
	for _, r := range roles {
		if err := os.WriteFile(filepath.Join(install, "agents", r), []byte("role\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf(`[{"id":"gauntlet@gauntlet","version":"0.1.0","scope":%q,"enabled":%t,"installPath":%q}]`,
		scope, enabled, install)
}
