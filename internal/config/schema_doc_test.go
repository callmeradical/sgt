package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// schemaDocRepoRoot locates the repository from this file rather than the
// working directory, so this test reads the same docs/schema.md regardless
// of which package `go test` was invoked from — the same pattern
// internal/mcp/config_test.go and internal/repopolicy already use.
func schemaDocRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed, cannot locate the repository")
	}
	// <root>/internal/config/schema_doc_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

func readSchemaDoc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(schemaDocRepoRoot(t), "docs", "schema.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// TestSchemaDocNamesNoDeletedV1Binary guards the canonical v2 schema
// reference against citing a binary that no longer exists on this branch
// (AGENTS.md, "v1 is not a dependency") as the thing that implements a
// field's current behavior.
func TestSchemaDocNamesNoDeletedV1Binary(t *testing.T) {
	doc := readSchemaDoc(t)
	for _, deleted := range []string{"sgt-graphify", "sgt-sync", "sgt-dispatch"} {
		if strings.Contains(doc, deleted) {
			t.Errorf("docs/schema.md names deleted v1 binary %q as a current mechanism; it must name the real v2 code that owns this behavior instead", deleted)
		}
	}
}

// schemaDocRepoFieldTableHasRow reports whether docs/schema.md's `repos[]`
// fields table documents a row for field. It is scoped to that one table so
// it cannot false-positive on other `repos[]` fields (e.g. agent_instructions,
// identity) that are out of scope for this test.
func schemaDocRepoFieldTableHasRow(doc, field string) bool {
	marker := "`" + field + "`"
	inTable := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## `repos[]` fields"):
			inTable = true
			continue
		case inTable && strings.HasPrefix(trimmed, "## "):
			return false // left the section without finding the row
		}
		if inTable && strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

// TestSchemaDocDoesNotDocumentNonexistentRepoURLField guards against the
// specific fictional-field mistake this change fixes: docs/schema.md's
// `repos[]` table documenting a `url` field that config.Repo does not
// actually have. It uses reflection against the real Repo struct, not a
// substring match on any particular wording, so it also catches the same
// mistake recurring in different prose.
func TestSchemaDocDoesNotDocumentNonexistentRepoURLField(t *testing.T) {
	repoType := reflect.TypeOf(Repo{})
	_, hasURL := repoType.FieldByName("URL")
	_, hasUrl := repoType.FieldByName("Url")
	repoHasURLField := hasURL || hasUrl

	doc := readSchemaDoc(t)
	docDocumentsURLField := schemaDocRepoFieldTableHasRow(doc, "url")

	if docDocumentsURLField && !repoHasURLField {
		t.Errorf("docs/schema.md's `repos[]` table documents a `url` field, but config.Repo (internal/config/config.go) has no URL field")
	}
	if repoHasURLField && !docDocumentsURLField {
		t.Errorf("config.Repo now has a URL field, but docs/schema.md's `repos[]` table does not document it")
	}
}
