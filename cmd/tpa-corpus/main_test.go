package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// fixtureSrc writes a one-signature source tree and returns its directory.
func fixtureSrc(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "signatures")
	writeJSON(t, filepath.Join(dir, "manifest.json"), map[string]any{
		"bundle_version":    "0.1.7",
		"schema_version":    "0.1.0",
		"generated_from":    "signatures/",
		"source_date_epoch": 1792454400,
	})
	writeJSON(t, filepath.Join(dir, "TPA-2026-0001.json"), map[string]any{
		"id":         "TPA-2026-0001",
		"category":   "tpa_hidden_instruction_block",
		"gating":     true,
		"provenance": map[string]any{"source": "https://example.org/d", "published": "2025-04-01"},
		"license":    "MIT",
		"rules": []any{map[string]any{
			"category": "rug-pull", "confidence": 0.9, "detector": "r", "engine": "regex",
			"flags": []string{"i", "s"}, "indicators": []any{}, "level": "high",
			"pattern": "(?is)<important>.*?(read|cat).*?</important>", "severity": "critical",
			"target": "tool_description", "type": "lexical",
		}},
		"skipped": []any{},
	})
	return dir
}

func fixtureDataset(t *testing.T, complete bool) string {
	t.Helper()
	entries := []any{
		map[string]any{"id": "m1", "label": "malicious", "category": "tpa_hidden_instruction_block", "tpa_id": "TPA-2026-0001", "canonical": true},
	}
	if complete {
		entries = append(entries, map[string]any{"id": "hn_tpa_hidden_instruction_block_01", "label": "benign", "category": "hard_negative", "resembles": "tpa_hidden_instruction_block"})
	}
	p := filepath.Join(t.TempDir(), "dataset.json")
	writeJSON(t, p, map[string]any{"entries": entries})
	return p
}

func do(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRun_UsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"frobnicate"},
		{"validate"},
		{"validate", "--bundle", "x"},
		{"build", "--no-such-flag"},
		{"verify", "unexpected", "--out", "/definitely-not-present/scanner-bundle.json"},
		{"build", "extra"},
		{"validate", "--bundle", "x", "--dataset", "y", "extra"},
	} {
		code, _, stderr := do(args...)
		assert.Equal(t, exitUsage, code, "args=%v", args)
		assert.NotEmpty(t, stderr, "args=%v", args)
	}
	code, _, _ := do("build", "--help")
	assert.Equal(t, exitOK, code)
}

func TestRun_BuildThenVerify(t *testing.T) {
	src := fixtureSrc(t)
	out := filepath.Join(t.TempDir(), "bundle.json")

	code, _, stderr := do("build", "--src", src, "--out", out)
	require.Equal(t, exitOK, code, stderr)
	first, err := os.ReadFile(out)
	require.NoError(t, err)

	code, _, stderr = do("verify", "--src", src, "--out", out)
	assert.Equal(t, exitOK, code, stderr)

	// A second build is byte-identical.
	code, _, _ = do("build", "--src", src, "--out", out)
	require.Equal(t, exitOK, code)
	second, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestRun_VerifyDetectsDrift(t *testing.T) {
	src := fixtureSrc(t)
	out := filepath.Join(t.TempDir(), "bundle.json")
	require.Equal(t, exitOK, func() int { c, _, _ := do("build", "--src", src, "--out", out); return c }())

	b, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out, bytes.Replace(b, []byte("0.1.7"), []byte("0.1.8"), 1), 0o600))

	code, _, stderr := do("verify", "--src", src, "--out", out)
	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr, "drift")
	assert.Contains(t, stderr, "build", "the message says how to fix it")

	// A missing bundle is drift too.
	code, _, _ = do("verify", "--src", src, "--out", filepath.Join(t.TempDir(), "absent.json"))
	assert.Equal(t, exitFailure, code)
}

func TestRun_BuildFailsOnBadSource(t *testing.T) {
	src := fixtureSrc(t)
	require.NoError(t, os.WriteFile(filepath.Join(src, "TPA-2026-0001.json"), []byte(`{"id":"TPA-2026-0001"}`), 0o600))
	out := filepath.Join(t.TempDir(), "bundle.json")
	code, _, stderr := do("build", "--src", src, "--out", out)
	assert.Equal(t, exitFailure, code)
	assert.NotEmpty(t, stderr)
	_, err := os.Stat(out)
	assert.True(t, os.IsNotExist(err), "a failed build must not leave a bundle behind")
}

func TestRun_SourceDateEpochEnv(t *testing.T) {
	src := fixtureSrc(t)
	out := filepath.Join(t.TempDir(), "bundle.json")

	t.Setenv("SOURCE_DATE_EPOCH", "0")
	code, _, stderr := do("build", "--src", src, "--out", out)
	require.Equal(t, exitOK, code, stderr)
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"generated_at": "1970-01-01T00:00:00Z"`)

	t.Setenv("SOURCE_DATE_EPOCH", "not-a-number")
	code, _, stderr = do("build", "--src", src, "--out", out)
	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr, "SOURCE_DATE_EPOCH")
}

func TestRun_Validate(t *testing.T) {
	src := fixtureSrc(t)
	bundle := filepath.Join(t.TempDir(), "bundle.json")
	require.Equal(t, exitOK, func() int { c, _, _ := do("build", "--src", src, "--out", bundle); return c }())

	code, _, stderr := do("validate", "--bundle", bundle, "--dataset", fixtureDataset(t, true))
	assert.Equal(t, exitOK, code, stderr)

	code, _, stderr = do("validate", "--bundle", bundle, "--dataset", fixtureDataset(t, false))
	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr, "hard negative")

	garbage := filepath.Join(t.TempDir(), "garbage.json")
	require.NoError(t, os.WriteFile(garbage, []byte(`{"bundle_version":"9.9.0"}`), 0o600))
	code, _, stderr = do("validate", "--bundle", garbage, "--dataset", fixtureDataset(t, true))
	assert.Equal(t, exitFailure, code, "a bundle the production loader rejects fails validation")
	assert.NotEmpty(t, stderr)

	code, _, _ = do("validate", "--bundle", filepath.Join(t.TempDir(), "absent.json"), "--dataset", fixtureDataset(t, true))
	assert.Equal(t, exitFailure, code)
}
