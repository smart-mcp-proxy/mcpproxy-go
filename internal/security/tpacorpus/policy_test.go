package tpacorpus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyBundle builds a bundle with one gating signature of class
// tpa_hidden_instruction_block and one non-gating signature of tpa_comment_directive.
func policyBundle(t testing.TB, mutate func(sigs []map[string]any)) []byte {
	t.Helper()
	sigs := []map[string]any{
		{
			"id": "TPA-2026-0001", "category": "tpa_hidden_instruction_block", "gating": true,
			"license":    "MIT",
			"provenance": map[string]any{"source": "https://example.org/a", "published": "2025-04-01"},
			"rule_ids":   []string{"TPA-2026-0001/r"},
		},
		{
			"id": "TPA-2026-0002", "category": "tpa_comment_directive", "gating": false,
			"license":    "MIT",
			"provenance": map[string]any{"source": "https://example.org/b", "published": "2025-04-01"},
			"rule_ids":   []string{"TPA-2026-0002/r"},
		},
	}
	if mutate != nil {
		mutate(sigs)
	}
	return jsonBytes(t, map[string]any{"signatures": sigs})
}

func malicious(id, tpaID, class string, canonical bool) map[string]any {
	e := map[string]any{"id": id, "label": "malicious", "category": class, "tpa_id": tpaID}
	if canonical {
		e["canonical"] = true
	}
	return e
}

func hardNeg(id, class string) map[string]any {
	return map[string]any{"id": id, "label": "benign", "category": "hard_negative", "resembles": class}
}

func policyDataset(t testing.TB, mutate func(entries []map[string]any) []map[string]any) []byte {
	t.Helper()
	entries := []map[string]any{
		malicious("tpa_hidden_01", "TPA-2026-0001", "tpa_hidden_instruction_block", true),
		hardNeg("hn_tpa_hidden_instruction_block_01", "tpa_hidden_instruction_block"),
		// Built-in categories already in the corpus must not trip the policy.
		{"id": "us_zwsp", "label": "malicious", "category": "unicode_smuggling"},
	}
	if mutate != nil {
		entries = mutate(entries)
	}
	return jsonBytes(t, map[string]any{"entries": entries})
}

func TestValidatePolicy_Accepts(t *testing.T) {
	require.NoError(t, ValidatePolicy(policyBundle(t, nil), policyDataset(t, nil)))
}

func TestValidatePolicy_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		bundle  func(sigs []map[string]any)
		dataset func(e []map[string]any) []map[string]any
		want    string
	}{
		{
			name: "gating signature without a malicious sample",
			dataset: func(e []map[string]any) []map[string]any {
				e[0]["tpa_id"] = "TPA-2026-0002"
				return e
			},
			want: "TPA-2026-0001",
		},
		{
			name: "sample category differs from the signature class",
			dataset: func(e []map[string]any) []map[string]any {
				e[0]["category"] = "tpa_comment_directive"
				return e
			},
			want: "TPA-2026-0001",
		},
		{
			name: "benign entry does not count as the malicious sample",
			dataset: func(e []map[string]any) []map[string]any {
				e[0]["label"] = "benign"
				return e
			},
			want: "TPA-2026-0001",
		},
		{
			name: "no hard negative for the class",
			dataset: func(e []map[string]any) []map[string]any {
				return []map[string]any{e[0], e[2]}
			},
			want: "hard negative",
		},
		{
			name: "hard negative resembles another class",
			dataset: func(e []map[string]any) []map[string]any {
				e[1]["resembles"] = "tpa_comment_directive"
				return e
			},
			want: "hard negative",
		},
		{
			name: "hard negative id lacks the class prefix",
			dataset: func(e []map[string]any) []map[string]any {
				e[1]["id"] = "hn_other_01"
				return e
			},
			want: "hard negative",
		},
		{
			name: "no canonical entry",
			dataset: func(e []map[string]any) []map[string]any {
				delete(e[0], "canonical")
				return e
			},
			want: "canonical",
		},
		{
			name: "two canonical entries",
			dataset: func(e []map[string]any) []map[string]any {
				return append(e, malicious("tpa_hidden_02", "TPA-2026-0001", "tpa_hidden_instruction_block", true))
			},
			want: "canonical",
		},
		{
			name: "tpa_id that resolves to nothing",
			dataset: func(e []map[string]any) []map[string]any {
				return append(e, malicious("tpa_orphan", "TPA-2026-0099", "tpa_hidden_instruction_block", false))
			},
			want: "TPA-2026-0099",
		},
		{
			name:   "licence outside the allowlist",
			bundle: func(s []map[string]any) { s[1]["license"] = "GPL-3.0-only" },
			want:   "allowlist",
		},
		{
			name:   "unknown class",
			bundle: func(s []map[string]any) { s[1]["category"] = "tpa_made_up" },
			want:   "unknown class",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePolicy(policyBundle(t, tc.bundle), policyDataset(t, tc.dataset))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidatePolicy_ReportsEveryProblem(t *testing.T) {
	ds := policyDataset(t, func(e []map[string]any) []map[string]any {
		return []map[string]any{e[2]}
	})
	err := ValidatePolicy(policyBundle(t, nil), ds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malicious sample")
	assert.Contains(t, err.Error(), "hard negative")
	assert.Contains(t, err.Error(), "canonical")
}

func TestValidatePolicy_NonGatingSignatureNeedsNoEvalPair(t *testing.T) {
	// TPA-2026-0002 is gating=false and has no dataset entries; the accept case
	// above already proves it is not required to. Make that explicit.
	sigs := policyBundle(t, nil)
	require.NoError(t, ValidatePolicy(sigs, policyDataset(t, nil)))
}

func TestClasses_Vocabulary(t *testing.T) {
	assert.GreaterOrEqual(t, len(Classes), 8)
	seen := map[string]bool{}
	for _, a := range Classes {
		assert.Regexp(t, `^tpa_[a-z0-9_]+$`, a)
		assert.False(t, seen[a], "duplicate class %s", a)
		seen[a] = true
		for _, b := range Classes {
			if a != b {
				assert.False(t, len(a) < len(b) && b[:len(a)] == a, "class %q is a prefix of %q", a, b)
			}
		}
	}
	assert.ElementsMatch(t, []string{"CC0-1.0", "CC-BY-4.0", "MIT", "Apache-2.0"}, LicenceAllowlist)
}

func TestValidatePolicy_PrefixClasses(t *testing.T) {
	restore := Classes
	t.Cleanup(func() { Classes = restore })
	Classes = append(append([]string(nil), Classes...), "tpa_hidden")
	err := ValidatePolicy(policyBundle(t, nil), policyDataset(t, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefix")
}
