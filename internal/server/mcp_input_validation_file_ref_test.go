package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tool's input schema comes from the upstream server. A $ref to a file://
// URL must not make the proxy open a local file and compile it into the schema.
func TestCompileInputSchema_RefusesFileRef(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local.json")
	if err := os.WriteFile(local, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	u := filepath.ToSlash(local)
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	ref, _ := json.Marshal(map[string]string{"$ref": "file://" + u})

	if entry := compileInputSchema(string(ref)); !entry.sentinel {
		t.Fatalf("input schema with $ref file://%s compiled; the local file was loaded", u)
	}
}

func TestCompileInputSchema_LocalRefStillWorks(t *testing.T) {
	schema := `{"$defs":{"q":{"type":"string"}},"type":"object","properties":{"q":{"$ref":"#/$defs/q"}}}`
	if entry := compileInputSchema(schema); entry.sentinel {
		t.Fatal("input schema with an in-document $ref failed to compile")
	}
}
