package scanner

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// sigBundle builds a valid signatures[] bundle as a generic map so each reject
// case can corrupt exactly one thing. Signature 0001 is gating (a runnable regex
// rule plus a structural_diff companion); 0002 owns only a resource_content rule
// so it is the one non-gating signature.
func sigBundle() map[string]any {
	return map[string]any{
		"bundle_version":  "0.1.1",
		"schema_version":  "0.1.0",
		"signature_count": 2,
		"rules": []any{
			map[string]any{"id": "TPA-2026-0001", "detector": "hidden_block", "engine": "regex",
				"target": "tool_description", "pattern": "sig-canary-one",
				"category": "exfiltration", "level": "high", "confidence": 0.9},
			map[string]any{"id": "TPA-2026-0001", "detector": "rug_diff", "engine": "structural_diff",
				"target": "tool_description", "pattern": "", "category": "rug-pull", "level": "high", "confidence": 0.9},
			map[string]any{"id": "TPA-2026-0002", "detector": "res_body", "engine": "regex",
				"target": "resource_content", "pattern": "sig-canary-two",
				"category": "prompt-injection", "level": "high", "confidence": 0.9},
		},
		"skipped": []any{
			map[string]any{"id": "TPA-2026-0001", "detector": "intent_judge", "reason": "engine llm_judge not offline-capable"},
		},
		"signatures": []any{
			map[string]any{
				"id": "TPA-2026-0001", "category": "tpa_hidden_instruction_block", "gating": true,
				"provenance": map[string]any{"source": "https://example.test/disclosure", "published": "2026-01-14"},
				"license":    "MIT",
				"rule_ids":   []any{"TPA-2026-0001/hidden_block", "TPA-2026-0001/rug_diff"},
			},
			map[string]any{
				"id": "TPA-2026-0002", "category": "tpa_comment_directive", "gating": false,
				"provenance": map[string]any{"source": "https://example.test/other", "published": "2026-02-01"},
				"license":    "CC0-1.0",
				"rule_ids":   []any{"TPA-2026-0002/res_body"},
			},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func sigsOf(b map[string]any) []any                { return b["signatures"].([]any) }
func sigAt(b map[string]any, i int) map[string]any { return sigsOf(b)[i].(map[string]any) }
func ruleAt(b map[string]any, i int) map[string]any {
	return b["rules"].([]any)[i].(map[string]any)
}

func TestValidateSignatures_Accepts(t *testing.T) {
	t.Run("valid signatures bundle loads", func(t *testing.T) {
		check, stats, err := loadBundleCheck(mustJSON(t, sigBundle()))
		require.NoError(t, err)
		require.NotNil(t, check)
		assert.Equal(t, 1, stats.Runnable)
		assert.Equal(t, 2, stats.Skipped)
	})

	t.Run("v0.1 bundle without signatures loads unchanged", func(t *testing.T) {
		check, _, err := loadBundleCheck([]byte(miniBundle))
		require.NoError(t, err)
		require.NotNil(t, check)
	})

	t.Run("embedded bundle still loads", func(t *testing.T) {
		check, _, err := loadBundleCheck(bundledScannerBundle)
		require.NoError(t, err)
		require.NotNil(t, check)
	})
}

func TestValidateSignatures_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(b map[string]any)
		want   string
	}{
		{"id grammar", func(b map[string]any) { sigAt(b, 1)["id"] = "TPA-26-2" }, `does not match TPA-YYYY-NNNN`},
		{"id grammar lowercase", func(b map[string]any) { sigAt(b, 1)["id"] = "tpa-2026-0002" }, `does not match TPA-YYYY-NNNN`},
		{"duplicate id", func(b map[string]any) {
			sigAt(b, 1)["id"] = "TPA-2026-0001"
		}, `duplicate signature id`},
		{"signature_count mismatch", func(b map[string]any) { b["signature_count"] = 6 }, `signature_count 6 != len(signatures) 2`},
		{"empty provenance.source", func(b map[string]any) {
			sigAt(b, 0)["provenance"].(map[string]any)["source"] = ""
		}, `provenance.source`},
		{"missing provenance", func(b map[string]any) { delete(sigAt(b, 0), "provenance") }, `provenance.source`},
		{"empty provenance.published", func(b map[string]any) {
			sigAt(b, 0)["provenance"].(map[string]any)["published"] = ""
		}, `provenance.published`},
		{"bad provenance.published format", func(b map[string]any) {
			sigAt(b, 0)["provenance"].(map[string]any)["published"] = "14/01/2026"
		}, `provenance.published`},
		{"non-padded provenance.published", func(b map[string]any) {
			sigAt(b, 0)["provenance"].(map[string]any)["published"] = "2026-1-4"
		}, `provenance.published`},
		{"empty license", func(b map[string]any) { sigAt(b, 0)["license"] = "" }, `license`},
		{"no rule_ids", func(b map[string]any) { sigAt(b, 1)["rule_ids"] = []any{} }, `lists no rule_ids`},
		{"rule_ids malformed", func(b map[string]any) {
			sigAt(b, 1)["rule_ids"] = []any{"TPA-2026-0002"}
		}, `is not "<TPA-id>/<detector>"`},
		{"rule_ids unresolved", func(b map[string]any) {
			sigAt(b, 1)["rule_ids"] = []any{"TPA-2026-0002/res_body", "TPA-2026-0002/ghost"}
		}, `does not resolve to a rules[] entry`},
		{"rule_ids id part belongs to another signature", func(b map[string]any) {
			sigAt(b, 1)["rule_ids"] = []any{"TPA-2026-0002/res_body", "TPA-2026-0001/hidden_block"}
		}, `must start with its own id`},
		{"rule_ids repeated entry", func(b map[string]any) {
			sigAt(b, 0)["rule_ids"] = []any{"TPA-2026-0001/hidden_block", "TPA-2026-0001/rug_diff", "TPA-2026-0001/rug_diff"}
		}, `owned more than once`},
		{"rule not owned", func(b map[string]any) {
			sigAt(b, 0)["rule_ids"] = []any{"TPA-2026-0001/hidden_block"}
		}, `is not owned by any signature`},
		{"rule with no signature", func(b map[string]any) {
			rules := b["rules"].([]any)
			b["rules"] = append(rules, map[string]any{"id": "TPA-2026-0003", "detector": "x", "engine": "regex",
				"target": "tool_description", "pattern": "x", "category": "rug-pull", "level": "high", "confidence": 0.9})
		}, `is not owned by any signature`},
		{"duplicate (id, detector)", func(b map[string]any) {
			ruleAt(b, 1)["detector"] = "hidden_block"
			sigAt(b, 0)["rule_ids"] = []any{"TPA-2026-0001/hidden_block"}
		}, `duplicate rule`},
		{"rules unsorted", func(b map[string]any) {
			rules := b["rules"].([]any)
			rules[0], rules[1] = rules[1], rules[0]
		}, `not sorted by (id, detector)`},
		{"gating true without runnable rule", func(b map[string]any) { sigAt(b, 1)["gating"] = true },
			`declares gating=true but owns no runnable rule`},
		{"gating false with runnable rule", func(b map[string]any) { sigAt(b, 0)["gating"] = false },
			`declares gating=false but owns a runnable rule`},
		{"skipped id unresolved", func(b map[string]any) {
			b["skipped"] = []any{map[string]any{"id": "TPA-2026-0099", "detector": "j", "reason": "r"}}
		}, `skipped entry TPA-2026-0099/j does not resolve`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := sigBundle()
			tc.mutate(b)
			check, _, err := loadBundleCheck(mustJSON(t, b))
			require.Error(t, err)
			assert.Nil(t, check, "a rejected bundle must never hand out a half-live check")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestConfigureBundle_SignatureViolationKeepsLastKnownGood proves an integrity
// failure takes the same fail-closed path as any other contract violation.
func TestConfigureBundle_SignatureViolationKeepsLastKnownGood(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())
	embedded := BundleStatus()

	b := sigBundle()
	sigAt(b, 0)["license"] = ""
	ConfigureBundle(writeBundle(t, string(mustJSON(t, b))), zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "the previous corpus must keep serving")
	assert.Equal(t, embedded.Fingerprint, info.Fingerprint)
	assert.Contains(t, info.LoadError, "license")
	require.NotNil(t, defaultBundleCheck())

	// The same bytes with the violation fixed load as the active corpus.
	sigAt(b, 0)["license"] = "MIT"
	ConfigureBundle(writeBundle(t, string(mustJSON(t, b))), zap.NewNop())
	info = BundleStatus()
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Empty(t, info.LoadError)
}
