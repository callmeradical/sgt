package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sgtBinary builds the CLI once per test run and returns the path to the
// resulting executable, so each case below can exec it and inspect the real
// process exit code.
func sgtBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sgt")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building sgt: %v\n%s", err, out)
	}
	return bin
}

// --help and -h must print usage and exit 0, matching version/status.
func TestHelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	bin := sgtBinary(t)

	for _, arg := range []string{"--help", "-h"} {
		out, err := exec.Command(bin, arg).CombinedOutput()
		if err != nil {
			t.Fatalf("sgt %s: exit error %v, output:\n%s", arg, err, out)
		}
		if !strings.Contains(string(out), "Usage:") {
			t.Fatalf("sgt %s: output %q, want it to contain usage text", arg, out)
		}
	}
}

// An unknown/invalid command must still print usage and exit nonzero, so
// scripts can tell a real invocation error apart from an explicit --help.
func TestUnknownCommandPrintsUsageAndExitsNonzero(t *testing.T) {
	bin := sgtBinary(t)

	out, err := exec.Command(bin, "bogus").CombinedOutput()
	if err == nil {
		t.Fatalf("sgt bogus: expected nonzero exit, got success. output:\n%s", out)
	}
	if !strings.Contains(string(out), "Usage:") {
		t.Fatalf("sgt bogus: output %q, want it to contain usage text", out)
	}
}

// No arguments at all must also still print usage and exit nonzero.
func TestNoArgsPrintsUsageAndExitsNonzero(t *testing.T) {
	bin := sgtBinary(t)

	out, err := exec.Command(bin).CombinedOutput()
	if err == nil {
		t.Fatalf("sgt: expected nonzero exit, got success. output:\n%s", out)
	}
	if !strings.Contains(string(out), "Usage:") {
		t.Fatalf("sgt: output %q, want it to contain usage text", out)
	}
}
