package config

// Tests for specs/gauntlet-roles/spec.md (claude-is-the-default-harness-and-
// runs-gauntlet-roles): the defaults.gauntlet_roles opt-out and the rule that
// decides whether gauntlet roles apply to a project at all.

import (
	"os"
	"path/filepath"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// Roles apply to a claude project unless it opts out, and never to any other
// harness, whatever the opt-out says.
func TestGauntletRolesApplyOnlyToClaudeUnlessOptedOut(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		roles *bool
		want  bool
	}{
		{"claude with no setting", "claude", nil, true},
		{"claude by absolute path", "/opt/tools/bin/claude", nil, true},
		{"claude explicitly on", "claude", boolPtr(true), true},
		{"claude opted out", "claude", boolPtr(false), false},
		{"goose with no setting", "goose", nil, false},
		{"goose explicitly on", "goose", boolPtr(true), false},
		{"opencode with no setting", "opencode", nil, false},
		{"a path whose base is not claude", "/opt/claude/bin/goose", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Project{Defaults: ProjectDefaults{Agent: tc.agent, GauntletRoles: tc.roles}}
			if got := p.GauntletRolesApply(); got != tc.want {
				t.Errorf("GauntletRolesApply() for agent %q, gauntlet_roles %v = %v, want %v",
					tc.agent, describePtr(tc.roles), got, tc.want)
			}
		})
	}
}

// A project that names no agent resolves to the default harness, so whether
// roles apply to it is decided exactly as for a project naming that harness.
func TestGauntletRolesApplyResolvesAnUnnamedAgentToTheDefault(t *testing.T) {
	for _, roles := range []*bool{nil, boolPtr(true), boolPtr(false)} {
		unnamed := &Project{Defaults: ProjectDefaults{GauntletRoles: roles}}
		named := &Project{Defaults: ProjectDefaults{Agent: DefaultAgent, GauntletRoles: roles}}
		if unnamed.GauntletRolesApply() != named.GauntletRolesApply() {
			t.Errorf("gauntlet_roles %s: unnamed agent = %v, agent %q = %v; an empty agent must resolve to the default",
				describePtr(roles), unnamed.GauntletRolesApply(), DefaultAgent, named.GauntletRolesApply())
		}
	}
}

// The YAML key is tri-state: absent is nil (on), and an explicit false is
// kept as false rather than collapsing into "unset".
func TestGauntletRolesKeyLoadsAsATriState(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	absent, err := LoadProject(write("absent", "name: absent\ndefaults:\n  agent: claude\nrepos: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if absent.Defaults.GauntletRoles != nil {
		t.Errorf("absent gauntlet_roles loaded as %v, want nil", *absent.Defaults.GauntletRoles)
	}
	if !absent.GauntletRolesApply() {
		t.Error("a claude project with no gauntlet_roles key must have roles on")
	}

	off, err := LoadProject(write("off", "name: off\ndefaults:\n  agent: claude\n  gauntlet_roles: false\nrepos: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if off.Defaults.GauntletRoles == nil || *off.Defaults.GauntletRoles {
		t.Errorf("gauntlet_roles: false loaded as %s, want a pointer to false", describePtr(off.Defaults.GauntletRoles))
	}
	if off.GauntletRolesApply() {
		t.Error("gauntlet_roles: false must turn roles off")
	}
}

func describePtr(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}
