package toolsig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 085 US1 T020 — FR-003 / SC-004: the never-elide-required invariant,
// checked property-style over a frozen 45-schema corpus: EVERY name in a
// schema's `required` array appears in the rendered signature marked `*`,
// unconditionally — including names with nested/unsupported/absent schemas.
// Also re-asserts the Lossy ⟺ contains(sig,"~") biconditional and
// determinism over the whole corpus.
//
// NOTE on the corpus source: tasks.md points at
// specs/083-discovery-profiler/datasets/corpus_v2.tools.json, which lives on
// the 083 branch (PR #851) and is NOT present in this tree yet (see T040
// sequencing). The spec-065 corpus_v1 that IS present carries no input
// schemas at all, so it cannot exercise this invariant. Until the 085 rebase
// brings corpus_v2 in, the invariant runs over the frozen in-test corpus
// below: 45 schemas modeled on the same 7 reference servers plus adversarial
// shapes (long enums, unions, $ref, metachar names, required-without-
// property, malformed JSON). When corpus_v2 lands, extend this test to also
// iterate that file (T040/T043 re-baseline).

// corpusSchema is one frozen corpus entry: a tool input schema as stored in
// ToolMetadata.ParamsJSON.
type corpusSchema struct {
	id     string
	params string
}

var frozenSchemaCorpus = []corpusSchema{
	// --- filesystem (reference server shapes) ---
	{"filesystem:read_file", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`},
	{"filesystem:read_text_file", `{"type":"object","properties":{"path":{"type":"string"},"head":{"type":"number"},"tail":{"type":"number"}},"required":["path"]}`},
	{"filesystem:read_multiple_files", `{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"}}},"required":["paths"]}`},
	{"filesystem:write_file", `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`},
	{"filesystem:edit_file", `{"type":"object","properties":{"path":{"type":"string"},"edits":{"type":"array","items":{"type":"object","properties":{"oldText":{"type":"string"},"newText":{"type":"string"}}}},"dryRun":{"type":"boolean","default":false}},"required":["path","edits"]}`},
	{"filesystem:create_directory", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`},
	{"filesystem:list_directory", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`},
	{"filesystem:directory_tree", `{"type":"object","properties":{"path":{"type":"string"},"depth":{"type":"integer","default":3}},"required":["path"]}`},
	{"filesystem:move_file", `{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"}},"required":["source","destination"]}`},
	{"filesystem:search_files", `{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"},"excludePatterns":{"type":"array","items":{"type":"string"},"default":[]}},"required":["path","pattern"]}`},
	{"filesystem:get_file_info", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`},
	{"filesystem:list_allowed_directories", `{"type":"object","properties":{}}`},
	// --- git ---
	{"git:git_status", `{"type":"object","properties":{"repo_path":{"type":"string"}},"required":["repo_path"]}`},
	{"git:git_diff", `{"type":"object","properties":{"repo_path":{"type":"string"},"target":{"type":"string"}},"required":["repo_path","target"]}`},
	{"git:git_commit", `{"type":"object","properties":{"repo_path":{"type":"string"},"message":{"type":"string"}},"required":["repo_path","message"]}`},
	{"git:git_add", `{"type":"object","properties":{"repo_path":{"type":"string"},"files":{"type":"array","items":{"type":"string"}}},"required":["repo_path","files"]}`},
	{"git:git_log", `{"type":"object","properties":{"repo_path":{"type":"string"},"max_count":{"type":"integer","default":10}},"required":["repo_path"]}`},
	{"git:git_checkout", `{"type":"object","properties":{"repo_path":{"type":"string"},"branch_name":{"type":"string"}},"required":["repo_path","branch_name"]}`},
	// --- fetch / time ---
	{"fetch:fetch", `{"type":"object","properties":{"url":{"type":"string"},"max_length":{"type":"integer","default":5000},"start_index":{"type":"integer","default":0},"raw":{"type":"boolean","default":false}},"required":["url"]}`},
	{"time:get_current_time", `{"type":"object","properties":{"timezone":{"type":"string"}},"required":["timezone"]}`},
	{"time:convert_time", `{"type":"object","properties":{"source_timezone":{"type":"string"},"time":{"type":"string"},"target_timezone":{"type":"string"}},"required":["source_timezone","time","target_timezone"]}`},
	// --- sqlite ---
	{"sqlite:read_query", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
	{"sqlite:write_query", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
	{"sqlite:create_table", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
	{"sqlite:list_tables", `{"type":"object","properties":{}}`},
	{"sqlite:describe_table", `{"type":"object","properties":{"table_name":{"type":"string"}},"required":["table_name"]}`},
	{"sqlite:append_insight", `{"type":"object","properties":{"insight":{"type":"string"}},"required":["insight"]}`},
	// --- memory (nested/graph shapes) ---
	{"memory:create_entities", `{"type":"object","properties":{"entities":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"entityType":{"type":"string"},"observations":{"type":"array","items":{"type":"string"}}}}}},"required":["entities"]}`},
	{"memory:create_relations", `{"type":"object","properties":{"relations":{"type":"array","items":{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"relationType":{"type":"string"}}}}},"required":["relations"]}`},
	{"memory:add_observations", `{"type":"object","properties":{"observations":{"type":"array","items":{"type":"object"}}},"required":["observations"]}`},
	{"memory:delete_entities", `{"type":"object","properties":{"entityNames":{"type":"array","items":{"type":"string"}}},"required":["entityNames"]}`},
	{"memory:read_graph", `{"type":"object","properties":{}}`},
	{"memory:search_nodes", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
	{"memory:open_nodes", `{"type":"object","properties":{"names":{"type":"array","items":{"type":"string"}}},"required":["names"]}`},
	// --- sequential-thinking ---
	{"sequential-thinking:sequentialthinking", `{"type":"object","properties":{"thought":{"type":"string"},"nextThoughtNeeded":{"type":"boolean"},"thoughtNumber":{"type":"integer"},"totalThoughts":{"type":"integer"},"isRevision":{"type":"boolean"},"revisesThought":{"type":"integer"},"branchFromThought":{"type":"integer"},"branchId":{"type":"string"},"needsMoreThoughts":{"type":"boolean"}},"required":["thought","nextThoughtNeeded","thoughtNumber","totalThoughts"]}`},
	// --- adversarial shapes (grammar edge coverage) ---
	{"adv:required_nested_object", `{"type":"object","properties":{"name":{"type":"string"},"account":{"type":"object","properties":{"id":{"type":"string"}}}},"required":["name","account"]}`},
	{"adv:required_missing_property", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path","token"]}`},
	{"adv:all_required_missing", `{"type":"object","required":["alpha","beta","gamma"]}`},
	{"adv:long_enum_required", `{"type":"object","properties":{"region":{"type":"string","enum":["nyc1","nyc3","sfo3","ams3","sgp1","lon1","fra1","tor1","blr1"]}},"required":["region"]}`},
	{"adv:short_enum_required", `{"type":"object","properties":{"type":{"enum":["full","partial"]}},"required":["type"]}`},
	{"adv:union_required", `{"type":"object","properties":{"id":{"type":["string","integer"]}},"required":["id"]}`},
	{"adv:metachar_name_required", `{"type":"object","properties":{"filter:key":{"enum":["a|b","c"]}},"required":["filter:key"]}`},
	{"adv:ref_required", `{"type":"object","properties":{"payload":{"$ref":"#/definitions/payload"}},"required":["payload"]}`},
	{"adv:anyof_required", `{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["value"]}`},
	{"adv:malformed_schema", `not valid json {`},
}

// sigParam is one parsed signature parameter: its (unquoted) name and marker
// flags. The parser understands §3.5 quoting, so names containing signature
// metacharacters round-trip.
type sigParam struct {
	name     string
	required bool
	lossy    bool
}

// parseSigParams tokenizes a rendered signature into its parameters. It
// splits on ", " outside quoted atoms and extracts each param's leading
// (possibly quoted) name plus its *~ markers. It deliberately re-implements
// only the prefix of the grammar the invariant needs.
func parseSigParams(t *testing.T, sig string) []sigParam {
	t.Helper()
	require.True(t, strings.HasPrefix(sig, "(") && strings.HasSuffix(sig, ")"),
		"signature must be parenthesized: %q", sig)
	body := sig[1 : len(sig)-1]
	if body == "" || body == "~" {
		return nil // "()" no params; "(~)" whole-schema-unparseable fallback
	}

	var parts []string
	var cur strings.Builder
	runes := []rune(body)
	inQuote, escaped := false, false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuote {
			cur.WriteRune(r)
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inQuote = false
			}
			continue
		}
		if r == '"' {
			inQuote = true
			cur.WriteRune(r)
			continue
		}
		if r == ',' && i+1 < len(runes) && runes[i+1] == ' ' {
			parts = append(parts, cur.String())
			cur.Reset()
			i++ // skip the space
			continue
		}
		cur.WriteRune(r)
	}
	parts = append(parts, cur.String())

	params := make([]sigParam, 0, len(parts))
	for _, part := range parts {
		params = append(params, parseOneParam(t, part))
	}
	return params
}

// parseOneParam extracts name + markers from "name[*][~]:typespec[=default]".
func parseOneParam(t *testing.T, part string) sigParam {
	t.Helper()
	var name string
	rest := part
	if strings.HasPrefix(part, `"`) {
		// Quoted name: find the closing unescaped quote.
		end := -1
		escaped := false
		for i, r := range part[1:] {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				end = i + 1
				break
			}
		}
		require.Positive(t, end, "unterminated quoted name in param %q", part)
		unquoted, err := parseQuotedAtom(part[:end+1])
		require.NoError(t, err, "quoted name must round-trip in param %q", part)
		name = unquoted
		rest = part[end+1:]
	} else {
		colon := strings.IndexAny(part, "*~:")
		require.GreaterOrEqual(t, colon, 1, "param %q must have a name before its markers", part)
		name = part[:colon]
		rest = part[colon:]
	}

	p := sigParam{name: name}
	for len(rest) > 0 {
		switch rest[0] {
		case '*':
			p.required = true
			rest = rest[1:]
			continue
		case '~':
			p.lossy = true
			rest = rest[1:]
			continue
		}
		break
	}
	require.True(t, strings.HasPrefix(rest, ":"), "param %q markers must be followed by ':'", part)
	return p
}

// requiredNamesFromSchema extracts the schema's required array test-side —
// an independent oracle for the invariant (never trusts Render's own parse).
func requiredNamesFromSchema(params string) (names []string, parseable bool) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(params), &schema); err != nil {
		return nil, false
	}
	raw, _ := schema["required"].([]any)
	seen := map[string]bool{}
	for _, v := range raw {
		if s, ok := v.(string); ok && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	return names, true
}

func TestNeverElideRequired_FrozenCorpus(t *testing.T) {
	require.Len(t, frozenSchemaCorpus, 45, "the schema corpus is frozen at 45 tools")

	for _, entry := range frozenSchemaCorpus {
		entry := entry
		t.Run(entry.id, func(t *testing.T) {
			sig, _ := Render(entry.params, "Corpus tool. Second sentence ignored.")

			requiredNames, parseable := requiredNamesFromSchema(entry.params)
			if !parseable {
				// Whole-schema-unparseable fallback (E11): bare lossy marker,
				// never "()"+lossy, never a hard failure.
				assert.Equal(t, "(~)", sig.Sig)
				assert.True(t, sig.Lossy)
				return
			}

			params := parseSigParams(t, sig.Sig)
			byName := map[string]sigParam{}
			for _, p := range params {
				byName[p.name] = p
			}

			// SC-004 hard invariant: every required NAME renders, marked "*".
			for _, name := range requiredNames {
				p, present := byName[name]
				require.True(t, present,
					"required param %q was ELIDED from signature %q — FR-003 hard invariant violation", name, sig.Sig)
				assert.True(t, p.required,
					"required param %q must carry the '*' marker in %q", name, sig.Sig)
			}

			// Lossy-legibility biconditional over the whole corpus.
			assert.Equal(t, strings.Contains(sig.Sig, "~"), sig.Lossy,
				"Lossy flag must equal contains(sig, \"~\") for %q", sig.Sig)

			// Determinism spot-check (FR-019): render twice, identical bytes.
			again, _ := Render(entry.params, "Corpus tool. Second sentence ignored.")
			assert.Equal(t, sig, again, "Render must be deterministic")
		})
	}
}

// Synthetic required-absent-from-properties cases (grammar E8, ⟲#8): a
// required name with NO property declaration must still render, marked
// required AND lossy, typed any — never dropped (the bench arm's historic
// bug, now a hard invariant).
func TestNeverElideRequired_AbsentFromProperties(t *testing.T) {
	t.Run("one of two absent", func(t *testing.T) {
		sig, _ := Render(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path","token"]}`, "")
		assert.Equal(t, "(path*:str, token*~:any)", sig.Sig)
		assert.True(t, sig.Lossy)
	})

	t.Run("all absent, no properties key at all", func(t *testing.T) {
		sig, _ := Render(`{"type":"object","required":["alpha","beta","gamma"]}`, "")
		assert.Equal(t, "(alpha*~:any, beta*~:any, gamma*~:any)", sig.Sig)
		assert.True(t, sig.Lossy)
	})

	t.Run("absent name containing metacharacters stays quoted and marked", func(t *testing.T) {
		sig, _ := Render(`{"type":"object","properties":{},"required":["weird name, with:stuff"]}`, "")
		assert.Equal(t, `("weird name, with:stuff"*~:any)`, sig.Sig)
		assert.True(t, sig.Lossy)

		// The parser (quote-aware) recovers it — proving the signature stays
		// unambiguous even for hostile names.
		params := parseSigParams(t, sig.Sig)
		require.Len(t, params, 1)
		assert.Equal(t, "weird name, with:stuff", params[0].name)
		assert.True(t, params[0].required)
	})
}
