package audit

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// fieldPointerPattern is the schema's change-path grammar, built from the
// classifier tables (FR-015b): a fixed root, one key under env/headers, a
// fixed child under oauth/rate_limit, a profile name and its fixed field, or
// the document root.
func fieldPointerPattern() string {
	roots := map[string]bool{}
	for k := range literalFields {
		if !strings.Contains(k, "/") {
			roots[k] = true
		}
	}
	for _, m := range []map[string]bool{secretPrefixes, nestedRoots, opaqueRoots, knownFieldRoots} {
		for k := range m {
			roots[k] = true
		}
	}
	keys := func(m map[string]bool) string {
		var out []string
		for k := range m {
			out = append(out, regexp.QuoteMeta(k))
		}
		sort.Strings(out)
		return strings.Join(out, "|")
	}
	var mapRoots []string
	for r, d := range maxDepth {
		if _, fixed := fixedChildren[r]; !fixed && d == 2 {
			mapRoots = append(mapRoots, r)
		}
	}
	sort.Strings(mapRoots)
	const seg = `([^~/]|~[01])+`
	return `^(/(` + keys(roots) + `)` +
		`|/(` + strings.Join(mapRoots, "|") + `)/` + seg +
		`|/oauth/(` + keys(fixedChildren["oauth"]) + `)` +
		`|/rate_limit/(` + keys(fixedChildren["rate_limit"]) + `)` +
		`|/profiles/` + seg + `(/(` + keys(profileFields) + `))?)?$`
}

// The published schema spells exactly the grammar the classifier renders
// (round 27): a path the builder would collapse is one the schema refuses.
func TestSchemaFieldPatternMatchesTheClassifier(t *testing.T) {
	for _, p := range []string{"../../docs/schemas/audit-line-v1.schema.json", "../../specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		want := fieldPointerPattern()
		found := false
		var walk func(v interface{})
		walk = func(v interface{}) {
			switch x := v.(type) {
			case map[string]interface{}:
				if f, ok := x["field"].(map[string]interface{}); ok && f["maxLength"] == float64(maxPointerLength) {
					if pat, ok := f["pattern"].(string); ok {
						found = true
						if pat != want {
							t.Errorf("%s: field grammar\n got %s\nwant %s", p, pat, want)
						}
					}
				}
				for _, c := range x {
					walk(c)
				}
			case []interface{}:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(doc)
		if !found {
			t.Errorf("%s: no admin field grammar", p)
		}
	}
}
