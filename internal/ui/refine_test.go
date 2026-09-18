package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// Helper to quickly build a mapping node
func makeMappingNode(keyValues ...string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(keyValues); i += 2 {
		n.Content = append(n.Content, scalarNode(keyValues[i]), scalarNode(keyValues[i+1]))
	}
	return n
}

func TestNodeGet(t *testing.T) {
	node := makeMappingNode("key1", "val1", "key2", "val2")
	if n := nodeGet(node, "key1"); n == nil || n.Value != "val1" {
		t.Errorf("expected val1, got %v", n)
	}
	if n := nodeGet(node, "missing"); n != nil {
		t.Errorf("expected nil for missing key, got %v", n)
	}
	if n := nodeGet(nil, "key"); n != nil {
		t.Errorf("expected nil for nil node, got %v", n)
	}
}

func TestNodeSet(t *testing.T) {
	node := makeMappingNode("key1", "val1")
	nodeSet(node, "key1", scalarNode("new1"))
	if n := nodeGet(node, "key1"); n == nil || n.Value != "new1" {
		t.Errorf("expected new1, got %v", n)
	}
	nodeSet(node, "key2", scalarNode("val2"))
	if n := nodeGet(node, "key2"); n == nil || n.Value != "val2" {
		t.Errorf("expected val2, got %v", n)
	}
}

func TestNodeDelete(t *testing.T) {
	node := makeMappingNode("key1", "val1", "key2", "val2")
	nodeDelete(node, "key1")
	if n := nodeGet(node, "key1"); n != nil {
		t.Errorf("expected key1 to be deleted")
	}
	if n := nodeGet(node, "key2"); n == nil || n.Value != "val2" {
		t.Errorf("expected key2 to remain")
	}
	nodeDelete(node, "missing") // should not panic
}

func TestPatchReposNode(t *testing.T) {
	// missing repos
	unknown := patchReposNode(nil, map[string]refineRepoPatch{"new-repo": {}})
	if len(unknown) != 1 || unknown[0] != "new-repo" {
		t.Errorf("expected unknown [new-repo], got %v", unknown)
	}

	// sequence node
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	repo1 := makeMappingNode("name", "repo1")
	seq.Content = append(seq.Content, repo1)

	role := "backend"
	unknown = patchReposNode(seq, map[string]refineRepoPatch{
		"repo1": {Role: &role},
		"repo2": {Role: &role},
	})
	if len(unknown) != 1 || unknown[0] != "repo2" {
		t.Errorf("expected unknown [repo2], got %v", unknown)
	}
	if n := nodeGet(repo1, "role"); n == nil || n.Value != "backend" {
		t.Errorf("expected repo1 role to be updated, got %v", n)
	}

	// mapping node
	mapping := makeMappingNode("repo1", "someval") // child should be a mapping for actual code, but for patching it just tries to patch
	mapping.Content[1] = makeMappingNode("role", "old")

	unknown = patchReposNode(mapping, map[string]refineRepoPatch{
		"repo1": {Role: &role},
		"repo3": {Role: &role},
	})
	if len(unknown) != 1 || unknown[0] != "repo3" {
		t.Errorf("expected unknown [repo3], got %v", unknown)
	}
	if n := nodeGet(mapping.Content[1], "role"); n == nil || n.Value != "backend" {
		t.Errorf("expected repo1 role to be updated, got %v", n)
	}
}

func TestHandleRefineProject_MethodNotAllowed(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("GET", "/api/refine-project", nil)
	w := httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRefineProject_InvalidPayload(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("POST", "/api/refine-project", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad json, got %d", w.Code)
	}

	payload := refinePayload{} // empty name
	body, _ := json.Marshal(payload)
	req = httptest.NewRequest("POST", "/api/refine-project", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty name, got %d", w.Code)
	}
}

func TestHandleRefineProject_InvalidName(t *testing.T) {
	srv := &Server{}
	payload := refinePayload{Name: "../etc/passwd"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/refine-project", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal in name, got %d", w.Code)
	}
}

func TestHandleRefineProject_InvalidExistingYAML(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("SGT_CONFIG", cfgDir)

	// Create unparseable YAML
	filePath := filepath.Join(cfgDir, "bad.yaml")
	os.WriteFile(filePath, []byte("bad: [yaml"), 0644)

	srv := &Server{}
	payload := refinePayload{Name: "bad"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/refine-project", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for unparseable existing YAML, got %d", w.Code)
	}

	// Create YAML that is not a mapping
	filePath = filepath.Join(cfgDir, "notmap.yaml")
	os.WriteFile(filePath, []byte("- item1\n- item2"), 0644)

	payload = refinePayload{Name: "notmap"}
	body, _ = json.Marshal(payload)
	req = httptest.NewRequest("POST", "/api/refine-project", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleRefineProject(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for non-mapping existing YAML, got %d", w.Code)
	}
}
