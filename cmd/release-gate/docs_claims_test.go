package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Spec 107 FR-035 / FR-036 docs guard.
//
// The server edition stores per-user brokered credentials but never injects
// them into an upstream call (research D1, D9–D11). The published docs still
// promise the opposite. This test fails while any of the FR-036
// injection/keying/exchange claims survives in the published docs tree (the
// docusaurus `include` list of website/docusaurus.config.js, plus CLAUDE.md,
// which FR-036 names explicitly), and while any of the four sidebar entries
// FR-036 keeps (`website/sidebars.js` lines 55, 129, 130, 263 at origin/main
// b39800a89) stops resolving to a real page. The rewrite is T023; this guard
// stays red until it lands and keeps the claims from creeping back.
//
// Sentences are matched case-sensitively on whitespace-collapsed text so a
// claim wrapped across a line break (`injects it at\ncall time`) is still
// caught. Table rows are matched on their first cell so that a later
// "removed keys" compatibility note that merely *mentions* `token_exchange`
// in prose is not flagged — only a row that documents the mode or the
// package as a live thing is.

// injectionClaimSentences are the FR-036 free-text claims, verbatim from
// tasks.md T013.
var injectionClaimSentences = []string{
	"injects it at call time",
	"Credential resolution",
	"Header injection",
	"Per-(user, server) connection keying",
	"JWT bearer token for MCP",
	"nothing re-validates at use time",
	"per-user connections",
	// The three FR-036 corrections T013's list left implicit (codex round 1
	// on PR-A): the activity-log row that named the never-emitted broker
	// events (`activity-log.md:28` at origin/main), the upstream-servers row
	// that called the block "per-user token brokering" (`:112`), and the
	// architecture line that listed the JWT as an MCP credential (`:53`).
	"acquire/refresh/inject",
	"per-user token brokering",
	"JWT bearer (MCP/API)",
}

// removedTableRowCells are the first-cell literals of the Markdown table rows
// FR-036 removes: the `token_exchange` and `entra_obo` mode rows in
// docs/features/auth-broker.md and the `internal/serveredition/workspace/`
// package row in docs/development/server-edition-multiuser-auth.md.
var removedTableRowCells = []string{
	"token_exchange",
	"entra_obo",
	"workspace/",
}

// keptSidebarEntries are the sidebar doc ids FR-036 says must keep resolving
// (tombstoned or rewritten, never deleted). Matched by id rather than by line
// number so an unrelated sidebar edit does not move the goalposts.
var keptSidebarEntries = []string{
	"cli/credential-commands",                   // sidebars.js:55
	"features/auth-broker",                      // sidebars.js:129
	"features/idp-token-storage",                // sidebars.js:130
	"development/server-edition-multiuser-auth", // sidebars.js:263
}

// repoRootFromTest is the repository root relative to the package directory;
// go test runs with cmd/release-gate as the working directory.
var repoRootFromTest = filepath.Join("..", "..")

var whitespaceRun = regexp.MustCompile(`\s+`)

func collapseWhitespace(s string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// TestDocsMakeNoInjectionClaims fails while any FR-036 sentence or table row
// survives in the published docs.
func TestDocsMakeNoInjectionClaims(t *testing.T) {
	files := publishedDocFiles(t)
	files = append(files, filepath.Join(repoRootFromTest, "CLAUDE.md"))

	var hits []string
	for _, path := range files {
		hits = append(hits, scanDocForClaims(t, path)...)
	}
	sort.Strings(hits)
	for _, h := range hits {
		t.Errorf("FR-036 claim survives: %s", h)
	}
	if len(hits) > 0 {
		t.Logf("%d FR-036 injection/keying/exchange claim(s) still published; T023 rewrites these pages (stored, not injected)", len(hits))
	}
}

// TestKeptSidebarEntriesResolve fails while any of the four FR-036 sidebar
// entries is missing from website/sidebars.js or names a docs page that does
// not exist.
func TestKeptSidebarEntriesResolve(t *testing.T) {
	sidebarPath := filepath.Join(repoRootFromTest, "website", "sidebars.js")
	data, err := os.ReadFile(sidebarPath)
	if err != nil {
		t.Fatalf("read %s: %v", sidebarPath, err)
	}
	sidebar := string(data)

	for _, id := range keptSidebarEntries {
		if !strings.Contains(sidebar, "'"+id+"'") {
			t.Errorf("website/sidebars.js no longer lists %q — FR-036 keeps this entry (tombstone or rewrite, never delete)", id)
			continue
		}
		if _, ok := resolveDocID(id); !ok {
			t.Errorf("website/sidebars.js entry %q names a page that does not exist (expected docs/%s.md or .mdx) — the docs build breaks", id, id)
		}
	}
}

// resolveDocID maps a docusaurus doc id to the repo docs/ source file.
func resolveDocID(id string) (string, bool) {
	for _, ext := range []string{".md", ".mdx"} {
		p := filepath.Join(repoRootFromTest, "docs", filepath.FromSlash(id)+ext)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// scanDocForClaims returns one "path:line: <claim>" entry per surviving
// FR-036 sentence or table row in the file.
func scanDocForClaims(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	rel, relErr := filepath.Rel(repoRootFromTest, path)
	if relErr != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)

	var hits []string
	report := func(lineNo int, claim string) {
		hits = append(hits, rel+":"+strconv.Itoa(lineNo)+": "+claim)
	}

	for i, raw := range lines {
		line := collapseWhitespace(raw)
		var joined string
		if i+1 < len(lines) {
			joined = collapseWhitespace(raw + " " + lines[i+1])
		}
		for _, s := range injectionClaimSentences {
			switch {
			case strings.Contains(line, s):
				report(i+1, "sentence "+strconv.Quote(s))
			case joined != "" && strings.Contains(joined, s) && !strings.Contains(collapseWhitespace(lines[i+1]), s):
				// the claim wraps across this line and the next
				report(i+1, "sentence "+strconv.Quote(s)+" (wrapped)")
			}
		}
		if cell, ok := firstTableCell(raw); ok {
			for _, c := range removedTableRowCells {
				if strings.Contains(cell, c) {
					report(i+1, "table row "+strconv.Quote(c))
				}
			}
		}
	}
	return hits
}

// firstTableCell returns the trimmed, backtick-stripped first cell of a
// Markdown table row, or false when the line is not a table row (or is the
// header separator).
func firstTableCell(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, "|")
	end := strings.Index(rest, "|")
	if end < 0 {
		return "", false
	}
	cell := strings.TrimSpace(rest[:end])
	cell = strings.Trim(cell, "`")
	if cell == "" || strings.Trim(cell, "-: ") == "" {
		return "", false
	}
	return cell, true
}

// publishedDocFiles returns every docs/ source file the docusaurus site
// publishes, derived from the `include`/`exclude` lists in
// website/docusaurus.config.js so a newly published directory is scanned
// without editing this test.
func publishedDocFiles(t *testing.T) []string {
	t.Helper()
	cfgPath := filepath.Join(repoRootFromTest, "website", "docusaurus.config.js")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	include := jsStringArray(t, string(data), "include")
	exclude := jsStringArray(t, string(data), "exclude")
	if len(include) == 0 {
		t.Fatalf("%s: no docs `include:` globs parsed — the docs-claims guard scans nothing", cfgPath)
	}

	excluded := map[string]bool{}
	for _, e := range exclude {
		excluded[filepath.ToSlash(e)] = true
	}

	docsRoot := filepath.Join(repoRootFromTest, "docs")
	seen := map[string]bool{}
	var files []string
	add := func(p string) {
		rel, err := filepath.Rel(docsRoot, p)
		if err != nil {
			return
		}
		rel = filepath.ToSlash(rel)
		if excluded[rel] || seen[rel] {
			return
		}
		seen[rel] = true
		files = append(files, p)
	}

	for _, glob := range include {
		glob = filepath.ToSlash(glob)
		if dir, ok := strings.CutSuffix(glob, "/**/*.{md,mdx}"); ok {
			root := filepath.Join(docsRoot, filepath.FromSlash(dir))
			err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				switch filepath.Ext(p) {
				case ".md", ".mdx":
					add(p)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk published docs dir %s (from include glob %q): %v", root, glob, err)
			}
			continue
		}
		p := filepath.Join(docsRoot, filepath.FromSlash(glob))
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			t.Errorf("%s include entry %q names a missing docs file %s", cfgPath, glob, p)
			continue
		}
		add(p)
	}
	sort.Strings(files)
	return files
}

// jsStringArray extracts the single-quoted string literals of the first
// `<key>: [ ... ]` array in a JS source, ignoring `//` line comments.
func jsStringArray(t *testing.T, src, key string) []string {
	t.Helper()
	start := strings.Index(src, key+": [")
	if start < 0 {
		return nil
	}
	body := src[start+len(key)+3:]
	end := strings.Index(body, "]")
	if end < 0 {
		t.Fatalf("docusaurus.config.js: unterminated `%s: [` array", key)
	}
	body = body[:end]

	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") {
			continue
		}
		for _, m := range jsSingleQuoted.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

var jsSingleQuoted = regexp.MustCompile(`'([^']+)'`)
