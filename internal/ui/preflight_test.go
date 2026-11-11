package ui

// Tests for the gauntlet plugin preflight's own pieces (design decision 5):
// parsing `claude plugin list --json`, and the default probe that runs it.
// The request-level scenarios are in gauntlet_dispatch_test.go.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmeradical/sgt/internal/config"
)

// testdata/claude_plugin_list.json has the shape of claude 2.1.322's
// `claude plugin list --json` (id, version, scope, enabled, installPath, and
// projectPath/projectEnabled on a project-scoped install), with operator
// home paths replaced by /home/operator.
func readPluginListFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude_plugin_list.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePluginListReadsTheCapturedFixture(t *testing.T) {
	entries, err := parsePluginList(readPluginListFixture(t))
	if err != nil {
		t.Fatalf("parsePluginList: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("parsed %d entries, want 3: %+v", len(entries), entries)
	}
	want := []pluginListEntry{
		{ID: "builder-skills@builder-skills", Version: "2b77ff068357", Scope: "project", Enabled: true,
			InstallPath: "/home/operator/.claude/plugins/cache/builder-skills/builder-skills/2b77ff068357"},
		{ID: "rust-analyzer-lsp@claude-plugins-official", Version: "1.0.0", Scope: "user", Enabled: true,
			InstallPath: "/home/operator/.claude/plugins/cache/claude-plugins-official/rust-analyzer-lsp/1.0.0"},
		{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true,
			InstallPath: "/home/operator/.claude/plugins/cache/gauntlet/gauntlet/0.1.0"},
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestParsePluginListRejectsOutputThatIsNotAPluginList(t *testing.T) {
	for _, raw := range []string{"", "not json", `{"id":"gauntlet@gauntlet"}`, `"a string"`} {
		if _, err := parsePluginList([]byte(raw)); err == nil {
			t.Errorf("parsePluginList(%q) accepted output that is not a JSON plugin list", raw)
		}
	}
}

// The captured fixture, with its gauntlet install made real on disk, passes
// the preflight and yields the version claude reported.
func TestThePreflightAcceptsTheCapturedPluginList(t *testing.T) {
	home := t.TempDir()
	raw := strings.ReplaceAll(string(readPluginListFixture(t)), "/home/operator", home)
	if err := writeRoleFiles(filepath.Join(home, ".claude/plugins/cache/gauntlet/gauntlet/0.1.0"), "alpha", "beta"); err != nil {
		t.Fatal(err)
	}
	probe := &recordingProbe{out: []byte(raw)}
	srv := &Server{GauntletProbe: probe.probe}

	roles, version, err := srv.gauntletPreflight(context.Background(), &config.Project{
		Name: "p", Defaults: config.ProjectDefaults{Agent: "claude"},
	})
	if err != nil {
		t.Fatalf("gauntletPreflight: %v", err)
	}
	if !roles || version != "0.1.0" {
		t.Errorf("gauntletPreflight = roles %v, version %q; want true, 0.1.0", roles, version)
	}
}

// The first enabled, user- or managed-scope gauntlet@ entry with both role
// files supplies the version.
func TestThePreflightTakesTheFirstUsableGauntletEntry(t *testing.T) {
	both := gauntletInstall(t, "alpha", "beta")
	probe := &recordingProbe{out: pluginListJSON(t,
		pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.0.9", Scope: "user", Enabled: false, InstallPath: both},
		pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.0.8", Scope: "project", Enabled: true, InstallPath: both},
		pluginEntryJSON{ID: "gauntlet@mirror", Version: "0.2.0", Scope: "managed", Enabled: true, InstallPath: both},
		pluginEntryJSON{ID: "gauntlet@gauntlet", Version: "0.1.0", Scope: "user", Enabled: true, InstallPath: both},
	)}
	srv := &Server{GauntletProbe: probe.probe}
	_, version, err := srv.gauntletPreflight(context.Background(), &config.Project{
		Name: "p", Defaults: config.ProjectDefaults{Agent: "claude"},
	})
	if err != nil {
		t.Fatalf("gauntletPreflight: %v", err)
	}
	if version != "0.2.0" {
		t.Errorf("version = %q, want 0.2.0 (the first enabled user- or managed-scope gauntlet@ entry)", version)
	}
}

// A project whose roles do not apply is answered without probing.
func TestThePreflightDoesNotProbeWhenRolesDoNotApply(t *testing.T) {
	probe := &recordingProbe{err: errors.New("must not be called")}
	srv := &Server{GauntletProbe: probe.probe}
	off := false
	for _, proj := range []*config.Project{
		{Name: "goose", Defaults: config.ProjectDefaults{Agent: "goose"}},
		{Name: "opted-out", Defaults: config.ProjectDefaults{Agent: "claude", GauntletRoles: &off}},
	} {
		roles, version, err := srv.gauntletPreflight(context.Background(), proj)
		if err != nil || roles || version != "" {
			t.Errorf("%s: gauntletPreflight = %v, %q, %v; want false, \"\", nil", proj.Name, roles, version, err)
		}
	}
	if probe.count() != 0 {
		t.Errorf("probe invoked %d times for projects without roles, want 0", probe.count())
	}
}

// The default probe runs `claude plugin list --json` and returns its stdout.
// A stub named claude on PATH stands in for the real binary.
func TestTheDefaultProbeRunsClaudePluginListJSON(t *testing.T) {
	bin := t.TempDir()
	argsLog := filepath.Join(bin, "args")
	script := "#!/bin/sh\necho \"$@\" > " + argsLog + "\necho '[{\"id\":\"gauntlet@gauntlet\"}]'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	out, err := claudePluginList(context.Background())
	if err != nil {
		t.Fatalf("claudePluginList: %v", err)
	}
	if strings.TrimSpace(string(out)) != `[{"id":"gauntlet@gauntlet"}]` {
		t.Errorf("stdout = %q", out)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "plugin list --json" {
		t.Errorf("claude invoked with %q, want \"plugin list --json\"", strings.TrimSpace(string(args)))
	}
}

func TestTheDefaultProbeReportsAMissingClaudeAsNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := claudePluginList(context.Background())
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error = %v, want one wrapping exec.ErrNotFound", err)
	}
}

func TestTheDefaultProbeFailsOnANonZeroExit(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := claudePluginList(context.Background())
	if err == nil {
		t.Fatal("a non-zero exit was accepted")
	}
	if errors.Is(err, exec.ErrNotFound) {
		t.Errorf("a failing claude was reported as missing: %v", err)
	}
}

// A hung claude cannot hold the request: the probe gives up when its
// context ends (the default probe adds its own 10s bound on top).
func TestTheDefaultProbeGivesUpOnAHungClaude(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := claudePluginList(ctx)
	if err == nil {
		t.Fatal("a hung claude was reported as success")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("probe took %s to give up on a hung claude", elapsed)
	}
}
