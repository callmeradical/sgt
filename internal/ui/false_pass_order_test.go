package ui

// Test for slice false-pass-order of change claude-is-the-default-harness-
// and-runs-gauntlet-roles: specs/gauntlet-roles/spec.md, "A roles-off
// blocking review keeps its own findings", through a real dispatch.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/store"
)

// Scenario: A roles-off blocking review keeps its own findings.
func TestGooseBlockingReviewFailsTheReviewAndNamesItsFindingAsTheBlockedReason(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SGT_CONFIG", cfgDir)
	t.Setenv("SGT_FLEET_DIR", filepath.Join(base, "fleet"))
	t.Setenv("SGT_WIKI_ROOT", filepath.Join(base, "wiki"))
	t.Setenv("SGT_ARTIFACTS_ROOT", filepath.Join(base, "artifacts"))
	t.Setenv("HOME", filepath.Join(base, "home"))

	repoPath := filepath.Join(base, "svc")
	initGitRepo(t, repoPath)
	const changeID = "review-blocks"
	if err := os.MkdirAll(filepath.Join(repoPath, "openspec", "changes", changeID), 0o755); err != nil {
		t.Fatal(err)
	}

	const finding = "S-retry-path-untested"
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(binDir, "goose")
	script := "#!/bin/sh\nmkdir -p .sgt\ncat > .sgt/envelope.json <<'EOF'\n" +
		`{"task_id":"t","repo":"svc","stage":"review","summary":"reviewed","payload":{"findings":[{"axis":"spec","severity":"error","summary":"` + finding + `","disposition":"add a test"}]}}` +
		"\nEOF\nexit 0\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	projYAML := "name: fpo\n" +
		"defaults:\n" +
		"  agent: " + agent + "\n" +
		"  retries: 2\n" +
		"repos:\n" +
		"  - name: svc\n" +
		"    path: " + repoPath + "\n" +
		"    factory:\n" +
		"      pipeline: [review]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "fpo.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(base, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mux := NewServer(st, 0).Handler()

	w := postDispatch(t, mux, dispatchBody(t, map[string]interface{}{
		"project": "fpo", "brief": "add retries", "change_id": changeID,
		"repos": []string{"svc"}, "type": "feat",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	waitForTerminalRun(t, st, resp.TaskID)
	if got := runStatus(t, st, resp.TaskID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}

	phases, err := st.ListPhasesForRun(resp.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var reviews []store.PhaseRecord
	for _, p := range phases {
		if p.Name == "review" && p.Kind == "agent" {
			reviews = append(reviews, p)
		}
	}
	if len(reviews) != 1 {
		t.Fatalf("review records = %+v, want exactly one (retries: 2, not retried)", reviews)
	}
	if reviews[0].Status != "failed" || !strings.Contains(reviews[0].Error, finding) {
		t.Errorf("review record status/error = %q/%q, want failed naming %s", reviews[0].Status, reviews[0].Error, finding)
	}

	envs, err := st.ListEnvelopesForRun(resp.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range envs {
		if e.Stage == "review" {
			for _, f := range handoff.ReviewFindings(e.Data) {
				found = found || f.Summary == finding
			}
		}
	}
	if !found {
		t.Errorf("no recorded review envelope still carries the finding %s", finding)
	}

	run, err := st.GetRun(resp.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if reason := srvBlockedReason(t, mux, st, run.IntentID); !strings.Contains(reason, finding) {
		t.Errorf("bullet blocked_reason = %q, want it to contain %s", reason, finding)
	}
}
