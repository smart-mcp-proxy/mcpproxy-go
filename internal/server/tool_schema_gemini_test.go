package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// findEmptyEnumMembers walks a decoded JSON value and returns the JSON paths of
// every empty-string member of an "enum" array. Google Gemini rejects such a
// function declaration with HTTP 400 ("enum[n]: cannot be empty"), which breaks
// every request of a client that lists the tool (#1576).
func findEmptyEnumMembers(v any, path string, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == "enum" {
				if arr, ok := child.([]any); ok {
					for i, m := range arr {
						if s, ok := m.(string); ok && s == "" {
							*out = append(*out, path+".enum["+strconv.Itoa(i)+"]")
						}
					}
				}
			}
			findEmptyEnumMembers(child, path+"."+k, out)
		}
	case []any:
		for i, child := range x {
			findEmptyEnumMembers(child, path+"["+strconv.Itoa(i)+"]", out)
		}
	}
}

// TestBuiltinToolSchemas_NoEmptyEnumString walks the schema of every built-in
// tool on every static tools/list surface (the frozen goldens are captured from
// the live registrations) plus the profiles tool built directly.
func TestBuiltinToolSchemas_NoEmptyEnumString(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", toolsListGoldenDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var doc any
		require.NoError(t, json.Unmarshal(raw, &doc))
		var bad []string
		findEmptyEnumMembers(doc, filepath.Base(f), &bad)
		require.Empty(t, bad, "empty-string enum member (Gemini rejects it)")
	}

	raw, err := json.Marshal(buildProfilesTool())
	require.NoError(t, err)
	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))
	var bad []string
	findEmptyEnumMembers(doc, "profiles", &bad)
	require.Empty(t, bad)
}
