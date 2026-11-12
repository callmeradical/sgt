package config

// Tests for specs/agent-harness/spec.md "An unnamed harness resolves to claude"
// (claude-is-the-default-harness-and-runs-gauntlet-roles, design decision 2):
// a project that names no agent is a claude project, so roles apply to it
// unless it opts out.

import "testing"

func TestAnUnnamedAgentIsAClaudeProjectForGauntletRoles(t *testing.T) {
	if DefaultAgent != "claude" {
		t.Fatalf("DefaultAgent = %q, want claude", DefaultAgent)
	}
	cases := []struct {
		name  string
		roles *bool
		want  bool
	}{
		{"no setting", nil, true},
		{"explicitly on", boolPtr(true), true},
		{"opted out", boolPtr(false), false},
	}
	for _, tc := range cases {
		p := &Project{Defaults: ProjectDefaults{GauntletRoles: tc.roles}}
		if got := p.GauntletRolesApply(); got != tc.want {
			t.Errorf("%s: GauntletRolesApply() for an unnamed agent = %v, want %v", tc.name, got, tc.want)
		}
	}
}
