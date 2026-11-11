package ui

// Tests for specs/gauntlet-roles/spec.md, "A default pipeline gains a review
// phase when roles apply": the workflow definition the dashboard is served
// uses the same effective pipeline the engine runs.

import (
	"net/http"
	"reflect"
	"testing"
)

// Scenario: The workflow view matches execution.
func TestTheWorkflowViewShowsReviewWhenRolesApplyToADefaultPipeline(t *testing.T) {
	cases := []struct {
		name     string
		defaults string
		factory  string
		want     []string
	}{
		{"claude, roles on, no pipeline", "  agent: claude\n", "", []string{"plan", "build", "test", "review"}},
		{"claude, opted out, no pipeline", "  agent: claude\n  gauntlet_roles: false\n", "", []string{"plan", "build", "test"}},
		{"goose, no pipeline", "  agent: goose\n", "", []string{"plan", "build", "test"}},
		{"claude, roles on, explicit pipeline", "  agent: claude\n",
			"    factory:\n      pipeline: [build, test]\n", []string{"build", "test"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := workflowFixture(t, "wf.yaml", "name: wf\ndefaults:\n"+tc.defaults+
				"repos:\n  - name: svc\n    path: /tmp/svc\n"+tc.factory)
			w, graph := getWorkflow(t, mux, "project=wf&repo=svc")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
			}
			if got := labelsOfKind(graph.Nodes, NodeKindStage); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("workflow stages = %v, want %v", got, tc.want)
			}
		})
	}
}
