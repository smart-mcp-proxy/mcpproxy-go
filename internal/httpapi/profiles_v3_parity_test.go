package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 108-l (FR-051, FR-052, T121): the parity checks that read the checked-in
// goldens of specs/108-profiles-v3 and internal/profile/testdata/contract. Every
// helper here is prefixed p108 so Spec 109-m's p109 helpers never collide
// (decision L1).

// p108RepoRoot is the repository root, found from this package directory.
func p108RepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "repo root not found from internal/httpapi")
	return root
}

func p108ReadJSON(t *testing.T, rel string, into any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p108RepoRoot(t), rel))
	require.NoError(t, err, "missing golden %s", rel)
	require.NoError(t, json.Unmarshal(b, into), rel)
}

func p108SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProfilesV3LabelsCoverEnums (L6): labels.json carries exactly one word for
// every value of the label families, no more and no fewer. The Web and macOS
// suites then pin their helpers to these words.
func TestProfilesV3LabelsCoverEnums(t *testing.T) {
	var enums map[string][]string
	p108ReadJSON(t, "internal/profile/testdata/contract/enums.json", &enums)
	var labels map[string]json.RawMessage
	p108ReadJSON(t, "internal/profile/testdata/contract/labels.json", &labels)

	for _, family := range []string{"access_reason", "source", "explain_step", "credential_state", "binding_mode", "unannotated", "max_tier"} {
		raw, ok := labels[family]
		require.True(t, ok, "labels.json has no %q family", family)
		var words map[string]string
		require.NoError(t, json.Unmarshal(raw, &words), family)
		values, ok := enums[family]
		require.True(t, ok, "enums.json has no %q family", family)

		want := append([]string(nil), values...)
		sort.Strings(want)
		require.Equal(t, want, p108SortedKeys(words),
			"labels.json %q must have exactly one label per enums.json value (no missing key, no extra key)", family)
		for key, word := range words {
			if family == "source" && key == "none" {
				require.Empty(t, word, "source none carries no suffix")
				continue
			}
			require.NotEmpty(t, word, "%s.%s has an empty label", family, key)
		}
	}

	// Mutation self-check: dropping a key must be detected by the comparison.
	var words map[string]string
	require.NoError(t, json.Unmarshal(labels["access_reason"], &words))
	delete(words, "tool_approval")
	want := append([]string(nil), enums["access_reason"]...)
	sort.Strings(want)
	require.NotEqual(t, want, p108SortedKeys(words), "the coverage comparison cannot fail")
}
