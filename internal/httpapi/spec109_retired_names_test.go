package httpapi

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-m (FR-090, T144a, M5): the Terminology table's "Retired names"
// column, enforced mechanically. The scan looks at string literals and
// template text in the non-test sources of the Web UI, the macOS app and the
// CLI; comments and identifiers are skipped. Each hit is fixed or listed in
// testdata/spec109_retired_allow.json with a reason, and an allowlist entry
// whose literal no longer occurs fails the test, so the list cannot rot.

// p109RetiredExact are literals that are retired when they are the whole
// string (a button label, a nav label, a title). Equality keeps the false
// positive rate near zero.
var p109RetiredExact = []string{
	"Dashboard",
	"Awaiting approval",
	"Pending Approval",
	"MCP Sessions",
	"Repositories",
	"Connect Clients",
	"AI agents",
	"Configuration",
	"Unquarantine",
}

// p109RetiredAddToMCP matches the retired "Add to MCP" but not "Add to MCPProxy".
var p109RetiredAddToMCP = regexp.MustCompile(`\bAdd to MCP\b`)

// p109RetiredRoots are the non-test source trees the scan covers.
var p109RetiredRoots = []string{
	"frontend/src",
	"native/macos/MCPProxy/MCPProxy",
	"cmd/mcpproxy",
}

var (
	p109CommentBlock = regexp.MustCompile(`(?s)/\*.*?\*/|<!--.*?-->`)
	p109CommentLine  = regexp.MustCompile(`(?m)(^|[\s;{}(,])//[^\n]*`)
	p109DoubleQuoted = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"`)
	p109SingleQuoted = regexp.MustCompile(`'((?:[^'\\\n]|\\.)*)'`)
	p109Backticked   = regexp.MustCompile("(?s)`([^`]*)`")
	p109TemplateText = regexp.MustCompile(`>([^<>{}\n]+)<`)
)

type p109RetiredHit struct {
	Path    string `json:"path"`
	Literal string `json:"literal"`
	Line    int    `json:"-"`
}

type p109RetiredAllow struct {
	Path    string `json:"path"`
	Literal string `json:"literal"`
	Reason  string `json:"reason"`
}

// p109Interpolation matches the interpolation forms of the scanned languages
// (Swift \(x), JS ${x}, Vue {{ x }}, printf verbs), so `"Dashboard \(name)"`
// is still the retired label "Dashboard" with a value appended.
var p109Interpolation = regexp.MustCompile(`\\\([^)]*\)|\$\{[^}]*\}|\{\{[^}]*\}\}|%[sdv@]`)

func p109RetiredMatch(literal string) bool {
	trimmed := strings.TrimSpace(p109Interpolation.ReplaceAllString(literal, ""))
	for _, retired := range p109RetiredExact {
		if trimmed == retired {
			return true
		}
	}
	return p109RetiredAddToMCP.MatchString(literal)
}

// p109ScanRetiredSource returns every retired literal in one file's text.
func p109ScanRetiredSource(path, src string) []p109RetiredHit {
	// Blank the comments but keep newlines so line numbers survive.
	blank := func(m string) string {
		return strings.Repeat(" ", len(m)-strings.Count(m, "\n")) + strings.Repeat("\n", strings.Count(m, "\n"))
	}
	src = p109CommentBlock.ReplaceAllStringFunc(src, blank)
	src = p109CommentLine.ReplaceAllStringFunc(src, func(m string) string {
		if idx := strings.Index(m, "//"); idx >= 0 {
			return m[:idx] + strings.Repeat(" ", len(m)-idx)
		}
		return m
	})

	var hits []p109RetiredHit
	seen := map[string]bool{}
	add := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			literal := src[m[2]:m[3]]
			if !p109RetiredMatch(literal) {
				continue
			}
			key := literal + "@" + string(rune(m[2]))
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, p109RetiredHit{Path: path, Literal: strings.TrimSpace(literal), Line: 1 + strings.Count(src[:m[2]], "\n")})
		}
	}
	add(p109DoubleQuoted)
	if !strings.HasSuffix(path, ".go") {
		add(p109SingleQuoted)
	}
	add(p109Backticked)
	if strings.HasSuffix(path, ".vue") {
		add(p109TemplateText)
	}
	return hits
}

func p109ScanRetiredTree(t testing.TB, root string) []p109RetiredHit {
	t.Helper()
	var hits []p109RetiredHit
	for _, rel := range p109RetiredRoots {
		base := filepath.Join(root, rel)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		require.NoError(t, filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "tests" || d.Name() == "__tests__" || d.Name() == "MCPProxyTests" {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			switch {
			case strings.HasSuffix(name, "_test.go"), strings.HasSuffix(name, ".spec.ts"), strings.HasSuffix(name, ".test.ts"):
				return nil
			case strings.HasSuffix(name, ".go"), strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".vue"), strings.HasSuffix(name, ".swift"):
			default:
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(root, p)
			hits = append(hits, p109ScanRetiredSource(filepath.ToSlash(relPath), string(b))...)
			return nil
		}))
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	return hits
}

func p109LoadRetiredAllow(t testing.TB) []p109RetiredAllow {
	t.Helper()
	var allow struct {
		Entries []p109RetiredAllow `json:"allow"`
	}
	b, err := os.ReadFile(filepath.Join("testdata", "spec109_retired_allow.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &allow))
	return allow.Entries
}

// TestSpec109RetiredNames fails when a retired literal is added to a UI or CLI
// string, and when an allowlist entry goes stale.
func TestSpec109RetiredNames(t *testing.T) {
	hits := p109ScanRetiredTree(t, p109Root(t))
	allow := p109LoadRetiredAllow(t)

	for _, a := range allow {
		assert.NotEmpty(t, a.Reason, "allowlist entry %s %q needs a reason", a.Path, a.Literal)
	}
	used := make([]bool, len(allow))
	for _, h := range hits {
		matched := false
		for i, a := range allow {
			if a.Path == h.Path && a.Literal == h.Literal {
				used[i] = true
				matched = true
			}
		}
		assert.True(t, matched, "retired name %q in %s:%d: fix it or list it in internal/httpapi/testdata/spec109_retired_allow.json with a reason", h.Literal, h.Path, h.Line)
	}
	for i, a := range allow {
		assert.True(t, used[i], "stale allowlist entry %s %q: the literal no longer occurs, remove it", a.Path, a.Literal)
	}
}

// TestSpec109RetiredNamesScannerBites is the mutation check: a planted literal
// is found in each language, comments and "Add to MCPProxy" are not, and an
// unlisted hit is distinguishable from an allowlisted one.
func TestSpec109RetiredNamesScannerBites(t *testing.T) {
	cases := []struct {
		name, path, src string
		want            int
	}{
		{"go literal", "cmd/mcpproxy/x.go", `fmt.Println("Dashboard")`, 1},
		{"go comment", "cmd/mcpproxy/x.go", "// the \"Dashboard\" page\nx := 1", 0},
		{"ts literal", "frontend/src/a.ts", `const label = 'Awaiting approval'`, 1},
		{"vue template text", "frontend/src/a.vue", `<template><span>Pending Approval</span></template>`, 1},
		{"vue html comment", "frontend/src/a.vue", `<!-- Dashboard --><span>ok</span>`, 0},
		{"swift literal", "native/macos/MCPProxy/MCPProxy/A.swift", `Text("Connect Clients")`, 1},
		{"add to mcp", "frontend/src/a.ts", `const t = "Add to MCP"`, 1},
		{"add to mcpproxy", "frontend/src/a.ts", `const t = "Add to MCPProxy"`, 0},
		{"path literal", "frontend/src/a.ts", `redirect: '/dashboard'`, 0},
		{"longer sentence", "frontend/src/a.ts", `const t = "Open the Dashboard page"`, 0},
		{"swift interpolation", "native/macos/MCPProxy/MCPProxy/A.swift", `Text("Dashboard \(name)")`, 1},
		{"js template interpolation", "frontend/src/a.ts", "const t = `Repositories ${n}`", 1},
	}
	for _, c := range cases {
		assert.Len(t, p109ScanRetiredSource(c.path, c.src), c.want, c.name)
	}
}
