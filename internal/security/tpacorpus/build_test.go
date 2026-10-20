package tpacorpus

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// runnableRule is a regex x tool_description rule: the only kind the offline
// tier executes, so a signature owning one must be gating.
func runnableRule(detector string) map[string]any {
	return map[string]any{
		"category":   "rug-pull",
		"confidence": 0.9,
		"detector":   detector,
		"engine":     "regex",
		"flags":      []string{"i", "s"},
		"indicators": []any{},
		"level":      "high",
		"pattern":    "(?is)<important>.*?(read|cat).*?</important>",
		"severity":   "critical",
		"target":     "tool_description",
		"type":       "lexical",
	}
}

func diffRule(detector string) map[string]any {
	return map[string]any{
		"category":   "rug-pull",
		"confidence": 0.95,
		"detector":   detector,
		"engine":     "structural_diff",
		"indicators": []any{},
		"level":      "critical",
		"rule":       map[string]any{"change": "modified", "field": "description"},
		"runtime":    "stateful",
		"severity":   "critical",
		"target":     "server_manifest",
		"type":       "behavioral",
	}
}

// source builds a valid per-signature source document.
func source(id, class string, gating bool, rules ...map[string]any) map[string]any {
	rs := make([]any, 0, len(rules))
	for _, r := range rules {
		rs = append(rs, r)
	}
	return map[string]any{
		"id":       id,
		"category": class,
		"gating":   gating,
		"provenance": map[string]any{
			"source":    "https://example.org/disclosure",
			"published": "2025-04-01",
			"note":      "test fixture",
		},
		"license": "MIT",
		"rules":   rs,
		"skipped": []any{},
	}
}

func jsonBytes(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func manifest(extra map[string]any) map[string]any {
	m := map[string]any{
		"bundle_version":    "0.1.7",
		"schema_version":    "0.1.0",
		"generated_from":    "signatures/",
		"source_date_epoch": 1792454400,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// fsOf assembles a source tree: manifest.json plus one TPA-*.json per source.
func fsOf(t testing.TB, m map[string]any, sources ...map[string]any) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{"manifest.json": {Data: jsonBytes(t, m)}}
	for _, s := range sources {
		fsys[s["id"].(string)+".json"] = &fstest.MapFile{Data: jsonBytes(t, s)}
	}
	return fsys
}

func twoSignatureFS(t testing.TB) fstest.MapFS {
	// Rules are deliberately authored out of (id, detector) order.
	s1 := source("TPA-2026-0001", "tpa_hidden_instruction_block", true,
		runnableRule("zz_late"), runnableRule("aa_early"), diffRule("manifest_desc_diff"))
	s1["skipped"] = []any{
		map[string]any{"detector": "z_judge", "id": "TPA-2026-0001", "reason": "engine llm_judge not offline-capable"},
		map[string]any{"detector": "a_judge", "id": "TPA-2026-0001", "reason": "engine llm_judge not offline-capable"},
	}
	s2 := source("TPA-2026-0002", "tpa_comment_directive", false, diffRule("only_diff"))
	return fsOf(t, manifest(nil), s2, s1)
}

func decodeTop(t testing.TB, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestBuild_DeterministicAndShape(t *testing.T) {
	fsys := twoSignatureFS(t)
	a, err := Build(fsys, Options{})
	require.NoError(t, err)
	b, err := Build(fsys, Options{})
	require.NoError(t, err)
	assert.Equal(t, a, b, "two builds of the same sources must be byte-equal")

	assert.True(t, bytes.HasSuffix(a, []byte("}\n")), "trailing LF")
	assert.Contains(t, string(a), "\n  \"bundle_version\": \"0.1.7\"", "two-space indent")

	top := decodeTop(t, a)
	for _, k := range []string{"bundle_version", "generated_at", "generated_from", "rules", "schema_version", "signature_count", "signatures", "skipped"} {
		assert.Contains(t, top, k)
	}
	assert.JSONEq(t, "2", string(top["signature_count"]))
}

func TestBuild_NoHTMLEscaping(t *testing.T) {
	out, err := Build(twoSignatureFS(t), Options{})
	require.NoError(t, err)
	assert.Contains(t, string(out), "<important>")
	assert.NotContains(t, string(out), "\\u003c", "encoding/json's default HTML escaping must be off")
}

func TestBuild_SortsRulesSignaturesAndSkipped(t *testing.T) {
	out, err := Build(twoSignatureFS(t), Options{})
	require.NoError(t, err)

	var b struct {
		Rules []struct {
			ID       string `json:"id"`
			Detector string `json:"detector"`
		} `json:"rules"`
		Signatures []struct {
			ID      string   `json:"id"`
			RuleIDs []string `json:"rule_ids"`
		} `json:"signatures"`
		Skipped []struct {
			ID       string `json:"id"`
			Detector string `json:"detector"`
		} `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(out, &b))

	var got []string
	for _, r := range b.Rules {
		got = append(got, r.ID+"/"+r.Detector)
	}
	assert.Equal(t, []string{
		"TPA-2026-0001/aa_early", "TPA-2026-0001/manifest_desc_diff", "TPA-2026-0001/zz_late",
		"TPA-2026-0002/only_diff",
	}, got)

	require.Len(t, b.Signatures, 2)
	assert.Equal(t, "TPA-2026-0001", b.Signatures[0].ID)
	assert.Equal(t, "TPA-2026-0002", b.Signatures[1].ID)
	assert.Equal(t, []string{"TPA-2026-0001/aa_early", "TPA-2026-0001/manifest_desc_diff", "TPA-2026-0001/zz_late"}, b.Signatures[0].RuleIDs)

	require.Len(t, b.Skipped, 2)
	assert.Equal(t, "a_judge", b.Skipped[0].Detector)
	assert.Equal(t, "z_judge", b.Skipped[1].Detector)
}

func TestBuild_GeneratedAt(t *testing.T) {
	fsys := twoSignatureFS(t)

	out, err := Build(fsys, Options{})
	require.NoError(t, err)
	var got struct {
		GeneratedAt string `json:"generated_at"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "2026-10-20T00:00:00Z", got.GeneratedAt, "manifest pin, RFC 3339 UTC")

	epoch := int64(0)
	out, err = Build(fsys, Options{SourceDateEpoch: &epoch})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "1970-01-01T00:00:00Z", got.GeneratedAt, "Options.SourceDateEpoch overrides the manifest pin")
}

func TestBuild_NoPinNoWallClock(t *testing.T) {
	m := manifest(nil)
	delete(m, "source_date_epoch")
	s := source("TPA-2026-0001", "tpa_hidden_instruction_block", true, runnableRule("r"))
	_, err := Build(fsOf(t, m, s), Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source_date_epoch")

	epoch := int64(1792454400)
	_, err = Build(fsOf(t, m, s), Options{SourceDateEpoch: &epoch})
	require.NoError(t, err, "an explicit epoch stands in for the missing pin")
}

func TestBuild_HeaderPassthrough(t *testing.T) {
	m := manifest(map[string]any{"header": map[string]any{"sequence": 7, "trust_epoch": "e1"}})
	s := source("TPA-2026-0001", "tpa_hidden_instruction_block", true, runnableRule("r"))
	out, err := Build(fsOf(t, m, s), Options{})
	require.NoError(t, err)
	top := decodeTop(t, out)
	assert.JSONEq(t, "7", string(top["sequence"]))
	assert.JSONEq(t, `"e1"`, string(top["trust_epoch"]))
	assert.Less(t, strings.Index(string(out), `"schema_version"`), strings.Index(string(out), `"sequence"`), "keys are alphabetical")

	m = manifest(map[string]any{"header": map[string]any{"rules": []any{}}})
	_, err = Build(fsOf(t, m, s), Options{})
	require.Error(t, err, "a header key must not shadow a builder-owned key")
	assert.Contains(t, err.Error(), "rules")
}

func TestBuild_ExcludeDropsSignatureAndRules(t *testing.T) {
	fsys := twoSignatureFS(t)
	out, err := Build(fsys, Options{Exclude: []string{"TPA-2026-0002"}})
	require.NoError(t, err)

	top := decodeTop(t, out)
	assert.JSONEq(t, "1", string(top["signature_count"]))
	assert.NotContains(t, string(out), "TPA-2026-0002")

	_, info, err := scanner.LoadBundleCheck(out)
	require.NoError(t, err, "the reduced bundle must still pass the load layer")
	assert.Equal(t, 1, info.SignatureCount)

	_, err = Build(fsys, Options{Exclude: []string{"TPA-2026-9999"}})
	require.Error(t, err, "excluding an unknown id is a typo, not a no-op")
}

func TestBuild_OutputPassesProductionLoader(t *testing.T) {
	out, err := Build(twoSignatureFS(t), Options{})
	require.NoError(t, err)
	check, info, err := scanner.LoadBundleCheck(out)
	require.NoError(t, err)
	require.NotNil(t, check)
	assert.Equal(t, 2, info.SignatureCount)
	assert.Equal(t, 2, info.RunnableRules)
	assert.Equal(t, "0.1.7", info.BundleVersion)
}

func TestBuild_NumbersSurviveRoundTrip(t *testing.T) {
	fsys := twoSignatureFS(t)
	fsys["TPA-2026-0001.json"] = &fstest.MapFile{Data: []byte(strings.Replace(
		string(fsys["TPA-2026-0001.json"].Data), `"confidence":0.9`, `"confidence":0.90`, 1))}
	out, err := Build(fsys, Options{})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"confidence": 0.90`, "numeric literals are copied, not re-formatted")
}

func TestBuild_RefusesBadSources(t *testing.T) {
	good := func() map[string]any {
		return source("TPA-2026-0001", "tpa_hidden_instruction_block", true, runnableRule("r"))
	}
	cases := []struct {
		name   string
		mutate func(s map[string]any)
		want   string
	}{
		{"missing provenance", func(s map[string]any) { delete(s, "provenance") }, "provenance"},
		{"empty provenance source", func(s map[string]any) { s["provenance"].(map[string]any)["source"] = "" }, "provenance.source"},
		{"bad published date", func(s map[string]any) { s["provenance"].(map[string]any)["published"] = "April 2025" }, "published"},
		{"missing licence", func(s map[string]any) { delete(s, "license") }, "license"},
		{"licence outside allowlist", func(s map[string]any) { s["license"] = "GPL-3.0-only" }, "allowlist"},
		{"licence not stated", func(s map[string]any) { s["license"] = "NOASSERTION" }, "allowlist"},
		{"unknown class", func(s map[string]any) { s["category"] = "tpa_made_up" }, "unknown class"},
		{"bad id", func(s map[string]any) { s["id"] = "TPA-26-1" }, "id"},
		{"no rules", func(s map[string]any) { s["rules"] = []any{} }, "rules"},
		{"rule of another signature", func(s map[string]any) {
			r := runnableRule("r")
			r["id"] = "TPA-2026-0009"
			s["rules"] = []any{r}
		}, "TPA-2026-0009"},
		{"null rule entry", func(s map[string]any) { s["rules"] = []any{nil} }, "rules[0]"},
		{"null rule entry after a valid one", func(s map[string]any) {
			s["rules"] = []any{runnableRule("r"), nil}
		}, "rules[1]"},
		{"null skipped entry", func(s map[string]any) { s["skipped"] = []any{nil} }, "skipped[0]"},
		{"gating mismatch is a load error", func(s map[string]any) { s["gating"] = false }, "gating"},
		{"unknown source field", func(s map[string]any) { s["licence"] = "MIT" }, "licence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := good()
			tc.mutate(s)
			fsys := fstest.MapFS{
				"manifest.json":      {Data: jsonBytes(t, manifest(nil))},
				"TPA-2026-0001.json": {Data: jsonBytes(t, s)},
			}
			out, err := Build(fsys, Options{})
			require.Error(t, err)
			assert.Nil(t, out)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuild_RefusesStructuralProblems(t *testing.T) {
	s := source("TPA-2026-0001", "tpa_hidden_instruction_block", true, runnableRule("r"))

	t.Run("no manifest", func(t *testing.T) {
		_, err := Build(fstest.MapFS{"TPA-2026-0001.json": {Data: jsonBytes(t, s)}}, Options{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifest.json")
	})
	t.Run("file name must match id", func(t *testing.T) {
		fsys := fsOf(t, manifest(nil), s)
		fsys["TPA-2026-0002.json"] = fsys["TPA-2026-0001.json"]
		_, err := Build(fsys, Options{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TPA-2026-0002.json")
	})
	t.Run("stray json file", func(t *testing.T) {
		fsys := fsOf(t, manifest(nil), s)
		fsys["notes.json"] = &fstest.MapFile{Data: []byte(`{}`)}
		_, err := Build(fsys, Options{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notes.json")
	})
	t.Run("no signatures", func(t *testing.T) {
		_, err := Build(fsOf(t, manifest(nil)), Options{})
		require.Error(t, err)
	})
	t.Run("duplicate detector", func(t *testing.T) {
		d := source("TPA-2026-0001", "tpa_hidden_instruction_block", true, runnableRule("r"), runnableRule("r"))
		_, err := Build(fsOf(t, manifest(nil), d), Options{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})
}
