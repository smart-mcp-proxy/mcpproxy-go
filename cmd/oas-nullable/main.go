// Command oas-nullable post-processes the swag-generated OpenAPI 3.1
// artifacts (oas/swagger.yaml and oas/docs.go). swag v2 cannot emit a
// JSON Schema type union, so a struct field that accepts an explicit JSON
// null is tagged `extensions:"x-nullable"`; this tool rewrites every such
// schema's `type: X` into the OAS 3.1 union `type: [X, "null"]` and drops
// the marker. It fails if a marker cannot be converted, so a generator
// change surfaces at `make swagger` instead of silently publishing a
// contract that rejects null (Spec 117 PR-2, #1566: expected_hashes).
//
// Usage: go run ./cmd/oas-nullable oas/swagger.yaml oas/docs.go
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const marker = "x-nullable"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: oas-nullable <swagger.yaml|docs.go>...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		if err := rewriteFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "oas-nullable: %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func rewriteFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out string
	if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
		out, err = rewriteYAML(string(raw))
	} else {
		out, err = rewriteJSON(string(raw))
	}
	if err != nil {
		return err
	}
	if out == string(raw) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), info.Mode().Perm())
}

var yamlMarkerLine = regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(marker) + `: true$`)

// rewriteYAML converts each `x-nullable: true` line into a type union on the
// sibling `type:` line of the same mapping.
func rewriteYAML(src string) (string, error) {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		m := yamlMarkerLine.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		indent := m[1]
		typePrefix := indent + "type: "
		found := false
		for j := len(out) - 1; j >= 0; j-- {
			prev := out[j]
			if strings.TrimSpace(prev) == "" {
				continue
			}
			if leadingSpaces(prev) < len(indent) {
				break // left the mapping
			}
			if leadingSpaces(prev) == len(indent) && strings.HasPrefix(prev, typePrefix) {
				typ := strings.TrimPrefix(prev, typePrefix)
				if strings.ContainsAny(typ, "[]-\"") {
					return "", fmt.Errorf("line %d: sibling type %q is not a single type", i+1, typ)
				}
				out[j] = indent + "type:\n" + indent + "- " + typ + "\n" + indent + `- "null"`
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("line %d: %s marker without a sibling single `type:`", i+1, marker)
		}
	}
	res := strings.Join(out, "\n")
	if strings.Contains(res, marker+":") {
		return "", fmt.Errorf("unconverted %s marker remains", marker)
	}
	return res, nil
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

// swag emits sorted keys, so the marker follows `type` (only `uniqueItems`
// can sort between them).
var jsonMarker = regexp.MustCompile(`"type":"([a-z]+)"((?:,"uniqueItems":(?:true|false))?),"` + regexp.QuoteMeta(marker) + `":true`)

func rewriteJSON(src string) (string, error) {
	res := jsonMarker.ReplaceAllString(src, `"type":["$1","null"]$2`)
	if strings.Contains(res, `"`+marker+`"`) {
		return "", fmt.Errorf("unconverted %s marker remains", marker)
	}
	return res, nil
}
