package tpacorpus

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// TestEmbeddedCorpus_RebuildIsByteIdentical is the in-process twin of
// `tpa-corpus verify`: the committed bundle must be exactly what the sources
// build to under the manifest pin. A hand edit of scanner-bundle.json, or a
// source edit without a rebuild, fails here.
func TestEmbeddedCorpus_RebuildIsByteIdentical(t *testing.T) {
	built, err := Build(os.DirFS("signatures"), Options{})
	require.NoError(t, err)
	embedded := scanner.EmbeddedBundle()
	require.True(t, bytes.Equal(built, embedded),
		"scanner-bundle.json (%d bytes) drifted from a rebuild of signatures/ (%d bytes); run: go run ./cmd/tpa-corpus build",
		len(embedded), len(built))
}

// TestEmbeddedCorpus_V01RulesUnchanged pins the ten v0.1.0 rules and three
// skipped entries: the CheckIDs that attack-lab and the runtime tests name
// (tpa.<TPA-id>.<detector>) must not move when the corpus grows.
func TestEmbeddedCorpus_V01RulesUnchanged(t *testing.T) {
	type section struct {
		Rules   []json.RawMessage `json:"rules"`
		Skipped []json.RawMessage `json:"skipped"`
	}
	load := func(data []byte) section {
		var s section
		require.NoError(t, json.Unmarshal(data, &s))
		return s
	}
	golden, err := os.ReadFile("testdata/bundle_v0.1.0.json")
	require.NoError(t, err)
	old, now := load(golden), load(scanner.EmbeddedBundle())

	require.Len(t, old.Rules, 10)
	require.GreaterOrEqual(t, len(now.Rules), len(old.Rules))
	for i, r := range old.Rules {
		assert.JSONEq(t, string(r), string(now.Rules[i]), "v0.1 rule %d changed", i)
	}
	require.Len(t, old.Skipped, 3)
	for i, s := range old.Skipped {
		assert.JSONEq(t, string(s), string(now.Skipped[i]), "v0.1 skipped entry %d changed", i)
	}
}

func TestEmbeddedCorpus_SignaturesMeetBuildPolicy(t *testing.T) {
	sigs, err := scanner.ParseBundleSignatures(scanner.EmbeddedBundle())
	require.NoError(t, err)
	require.NotEmpty(t, sigs)
	for _, s := range sigs {
		assert.Contains(t, Classes, s.Category, "%s", s.ID)
		assert.Contains(t, LicenceAllowlist, s.License, "%s", s.ID)
		assert.NotEmpty(t, s.Provenance.Source, "%s", s.ID)
	}
}

// TestEmbeddedCorpus_PassesPolicyAgainstDetectCorpus is the build-time policy
// the CI step enforces: the embedded bundle, checked against the committed eval
// dataset, has a malicious sample for every gating signature, a hard negative
// and exactly one canonical sample for every gating class, and resolves every
// tpa_id. It is the in-process twin of `tpa-corpus validate`.
func TestEmbeddedCorpus_PassesPolicyAgainstDetectCorpus(t *testing.T) {
	dataset, err := os.ReadFile("../../../specs/065-evaluation-foundation/datasets/detect_corpus_v1.json")
	require.NoError(t, err)
	require.NoError(t, ValidatePolicy(scanner.EmbeddedBundle(), dataset))
}
