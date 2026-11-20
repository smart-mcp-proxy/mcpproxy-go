package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// seedFixtureRule is one regex rule a test adds to the seed corpus.
type seedFixtureRule struct {
	ID       string // TPA-2099-xxxx, sorts after every shipped id
	Detector string
	Pattern  string
	Category string // rule category, e.g. "prompt-injection"
}

// seedDerivedBundle renders the EMBEDDED seed bundle with the signatures in
// drop removed (with every rule they own) and extra added as one more
// signature-owned rule.
//
// Every file-drop fixture that reaches the real activation self-check (Spec
// 087 FR-008, installed by NewServer for good) must be built this way: the
// self-check scores the candidate on the full eval corpus, whose tpa_* samples
// (Spec 101 US2) only the seed signatures catch, so a one-rule bundle is
// refused as gate_failure (recall 56/131). Dropping one signature keeps recall
// above 0.90 because each seed signature owns few corpus samples (FR-016b).
func seedDerivedBundle(t *testing.T, extra seedFixtureRule, drop ...string) []byte {
	t.Helper()
	dropped := make(map[string]bool, len(drop))
	for _, id := range drop {
		dropped[id] = true
	}

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(scanner.EmbeddedBundle(), &doc))
	set := func(k string, v any) {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		doc[k] = raw
	}

	var rules []json.RawMessage
	require.NoError(t, json.Unmarshal(doc["rules"], &rules))
	kept := make([]json.RawMessage, 0, len(rules)+1)
	for _, raw := range rules {
		var r struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &r))
		if !dropped[r.ID] {
			kept = append(kept, raw)
		}
	}
	rule, err := json.Marshal(map[string]any{
		"category": extra.Category, "confidence": 0.95, "detector": extra.Detector,
		"engine": "regex", "id": extra.ID, "level": "high",
		"pattern": extra.Pattern, "target": "tool_description",
	})
	require.NoError(t, err)
	set("rules", append(kept, rule))

	raw, ok := doc["signatures"]
	require.True(t, ok, "the seed bundle carries signatures[] (Spec 101 US2)")
	var sigs []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &sigs))
	keptSigs := make([]json.RawMessage, 0, len(sigs)+1)
	for _, s := range sigs {
		var sig struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(s, &sig))
		if dropped[sig.ID] {
			delete(dropped, sig.ID)
			continue
		}
		keptSigs = append(keptSigs, s)
	}
	require.Empty(t, dropped, "every dropped signature id must exist in the seed bundle")
	sig, err := json.Marshal(map[string]any{
		"id": extra.ID, "category": "tpa_server_test_fixture", "gating": true,
		"license": "self-authored", "rule_ids": []string{extra.ID + "/" + extra.Detector},
		"provenance": map[string]string{"source": "internal/server test fixture", "published": "2026-11-10", "note": "test-only"},
	})
	require.NoError(t, err)
	keptSigs = append(keptSigs, sig)
	set("signatures", keptSigs)
	set("signature_count", len(keptSigs))

	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

// seedRunnableRules is the runnable rule count of the embedded seed bundle.
func seedRunnableRules(t *testing.T) int {
	t.Helper()
	_, info, err := scanner.LoadBundleCheck(scanner.EmbeddedBundle())
	require.NoError(t, err)
	return info.RunnableRules
}
