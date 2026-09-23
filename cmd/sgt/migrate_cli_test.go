package main

// Tests for Task 2 of openspec/changes/upgrade-migration-pre-rebrand-v2-state:
// wiring internal/upgrademigrate.Run() into run/status/ui/mcp, and the new
// `sgt migrate` subcommand. Every test here runs the real compiled sgt
// binary (sgtBinary(t), help_exit_test.go's helper) as a real subprocess
// with a fake $HOME (matching internal/repopolicy's
// TestMiseInstallLinksWikiDigestAndBuildsSgt precedent for faking a
// subprocess's home directory) — never a direct in-process call to the
// command's own handler function, since the property under test is that
// main()'s dispatch itself wires the call in, for each of the four
// subcommands independently.
import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHomeWithOldState builds a minimal, real pre-rebrand v2 installation
// (just a config YAML — enough alone to make upgrademigrate.Detected()
// true) under a fresh temp dir standing in for $HOME.
func fakeHomeWithOldState(t *testing.T) (home, sentinelPath string) {
	t.Helper()
	home = t.TempDir()
	oldConfigDir := filepath.Join(home, ".config", "sergeant")
	if err := os.MkdirAll(oldConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldConfigDir, "fooproj.yaml"), []byte("repos:\n  - name: r1\n    path: /tmp/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinelPath = filepath.Join(home, ".config", "sgt", ".migration-from-sergeant.json")
	return home, sentinelPath
}

// runAndWaitForSentinel runs bin with args against a fake $HOME, and waits
// until either the sentinel file appears or the process exits on its own —
// whichever comes first. run/status/mcp (with stdin at EOF) all complete on
// their own quickly; ui blocks forever on its HTTP server, so it is killed
// once the sentinel has appeared, which is the only thing this test needs
// to prove for that subcommand.
func runAndWaitForSentinel(t *testing.T, bin, home, sentinelPath string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sgt %v: %v", args, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sentinelPath); err == nil {
			_ = cmd.Process.Kill()
			<-done
			return
		}
		select {
		case err := <-done:
			if _, statErr := os.Stat(sentinelPath); statErr == nil {
				return
			}
			t.Fatalf("sgt %v exited (err=%v) without writing the migration sentinel; stdout=%s stderr=%s", args, err, outBuf.String(), errBuf.String())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("sgt %v: sentinel %s was never written within the deadline; stdout=%s stderr=%s", args, sentinelPath, outBuf.String(), errBuf.String())
		}
	}
}

// TestAutoMigrationRunsForEveryPathResolvingSubcommand exercises Decision 2
// (docs/prd-upgrade-migration.md): every one of run/status/ui/mcp must
// trigger migration, proven independently for each rather than assumed to
// hold for all four because it held for one.
func TestAutoMigrationRunsForEveryPathResolvingSubcommand(t *testing.T) {
	bin := sgtBinary(t)

	cases := []struct {
		name string
		args []string
	}{
		// "run" is given a project name that does not exist: runProject
		// fails and exits nonzero after migration has already run — proving
		// migration happens before, not conditionally on, the rest of the
		// command succeeding.
		{"run", []string{"run", "does-not-exist"}},
		{"status", []string{"status"}},
		{"ui", []string{"ui"}},
		{"mcp", []string{"mcp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, sentinelPath := fakeHomeWithOldState(t)
			runAndWaitForSentinel(t, bin, home, sentinelPath, tc.args...)

			data, err := os.ReadFile(sentinelPath)
			if err != nil {
				t.Fatalf("sgt %v: reading sentinel: %v", tc.args, err)
			}
			if !strings.Contains(string(data), `"status"`) {
				t.Errorf("sgt %v: sentinel content %q does not look like a Sentinel", tc.args, data)
			}
			migratedProject := filepath.Join(home, ".config", "sgt", "fooproj.yaml")
			if _, err := os.Stat(migratedProject); err != nil {
				t.Errorf("sgt %v: fooproj.yaml was not migrated: %v", tc.args, err)
			}
		})
	}
}

// TestMigrateSubcommandNothingToMigrate covers the spec scenario "Running
// `sgt migrate` with nothing to migrate": no pre-rebrand v2 state, no prior
// sentinel -> prints a "nothing to migrate" message and exits 0.
func TestMigrateSubcommandNothingToMigrate(t *testing.T) {
	bin := sgtBinary(t)
	home := t.TempDir()

	cmd := exec.Command(bin, "migrate")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sgt migrate: expected exit 0, got %v; output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "nothing to migrate") {
		t.Errorf("sgt migrate output = %q, want it to report nothing to migrate", out)
	}
}

// TestMigrateSubcommandSurfacesConflictAndExitsNonzero covers "Running `sgt
// migrate` surfaces conflicts and exits non-zero": a pre-existing,
// differing destination config file must be named in the output, and the
// process must exit nonzero.
func TestMigrateSubcommandSurfacesConflictAndExitsNonzero(t *testing.T) {
	bin := sgtBinary(t)
	home, _ := fakeHomeWithOldState(t)

	newConfigDir := filepath.Join(home, ".config", "sgt")
	if err := os.MkdirAll(newConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newConfigDir, "fooproj.yaml"), []byte("repos:\n  - name: r1\n    path: /tmp/DIFFERENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "migrate")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("sgt migrate: expected nonzero exit for a conflicting destination; output:\n%s", out)
	}
	if !strings.Contains(string(out), "config:fooproj.yaml") {
		t.Errorf("sgt migrate output = %q, want it to name the conflicting file", out)
	}
}

// TestHelpMentionsMigrateSubcommand matches the existing pattern used for
// the dispatch/runs/run-details/create-pr manual entries
// (TestHelpListsAllFourNewSubcommands, dispatch_cli_test.go).
func TestHelpMentionsMigrateSubcommand(t *testing.T) {
	bin := sgtBinary(t)

	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("sgt --help: exit error %v, output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "sgt migrate") {
		t.Errorf("sgt --help output does not mention %q:\n%s", "sgt migrate", out)
	}
}
