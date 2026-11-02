package outputvalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileURL turns an absolute path into a file:// URL on every OS.
func fileURL(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return "file://" + s
}

// An output schema comes from the upstream server. A $ref to a file:// URL
// must not make the proxy open a local file and compile it into the schema.
func TestCompile_RefusesFileRef(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local.json")
	if err := os.WriteFile(local, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, _ := json.Marshal(map[string]string{"$ref": fileURL(local)})

	if entry := compile(string(ref)); !entry.sentinel {
		t.Fatalf("schema with $ref %s compiled; the local file was loaded", fileURL(local))
	}

	v := New(0, 0, nil)
	if verdict := v.Validate("srv:tool", string(ref), map[string]any{"id": 1}); verdict.IsViolation() {
		t.Fatalf("verdict was driven by the local file's contents: %s", verdict.Reason)
	}
}

func TestCompile_LocalRefsAndMetaSchemaStillWork(t *testing.T) {
	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$defs": {"id": {"type": "integer"}},
		"type": "object",
		"properties": {"id": {"$ref": "#/$defs/id"}}
	}`
	if entry := compile(schema); entry.sentinel {
		t.Fatal("schema with an in-document $ref and the standard $schema failed to compile")
	}

	v := New(0, 0, nil)
	if verdict := v.Validate("srv:tool", schema, map[string]any{"id": "x"}); !verdict.IsViolation() {
		t.Fatal("expected a violation for a string id")
	}
}
