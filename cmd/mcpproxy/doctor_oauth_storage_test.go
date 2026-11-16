package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Spec 123 PR-3 T053 (FR-030): the OAuth token storage section of
// `mcpproxy doctor`, rendered from the daemon's /api/v1/diagnostics payload.

func oauthStorageDiag(section map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"total_issues":        0,
		"oauth_token_storage": section,
	}
}

func renderDoctorPretty(t *testing.T, diag map[string]interface{}) string {
	t.Helper()
	prev := doctorOutput
	doctorOutput = "pretty"
	t.Cleanup(func() { doctorOutput = prev })
	return captureOutput(func() {
		if err := outputDiagnostics(diag, nil, nil, "", nil); err != nil {
			t.Errorf("outputDiagnostics: %v", err)
		}
	})
}

func TestDoctorPretty_OAuthTokenStorageSection(t *testing.T) {
	out := renderDoctorPretty(t, oauthStorageDiag(map[string]interface{}{
		"encryption_enabled": true,
		"key_id":             "0123456789abcdef",
		"key_source_kind":    "ref_file",
		"sealed":             2,
		"plaintext":          1,
		"undecryptable":      1,
		"undecryptable_by_reason": map[string]interface{}{
			"no_key": 0, "key_mismatch": 1, "auth_failed": 0, "unsupported_version": 0, "malformed": 0,
		},
		"servers": []interface{}{
			map[string]interface{}{"bucket_key": "delta_0123456789abcdef", "server": "delta", "reason": "key_mismatch", "row_key_id": "fedcba9876543210"},
		},
		"compaction_advised": true,
		"warnings":           []interface{}{"literal_key", "compaction_advised", "backup_file_present"},
	}))

	assert.Contains(t, out, "OAuth Token Storage")
	assert.Contains(t, out, "on (key id 0123456789abcdef, source: ref_file)")
	assert.Contains(t, out, "sealed 2, plaintext 1, undecryptable 1")
	assert.Contains(t, out, "delta: key_mismatch (row key id fedcba9876543210)")
	assert.Contains(t, out, "mcpproxy db compact --keep-backup=false", "compaction pending is advised with the exact command")
	assert.Contains(t, out, "config.db.bak")
	assert.Contains(t, out, "written in the configuration file")
	assert.Contains(t, out, "mcpproxy auth logout --server=<name>", "undecryptable rows name the way out")
}

func TestDoctorPretty_OAuthTokenStorageOffStaysQuiet(t *testing.T) {
	out := renderDoctorPretty(t, oauthStorageDiag(map[string]interface{}{
		"encryption_enabled": false,
		"sealed":             0,
		"plaintext":          3,
		"undecryptable":      0,
		"servers":            []interface{}{},
		"compaction_advised": false,
		"warnings":           []interface{}{},
	}))
	assert.NotContains(t, out, "OAuth Token Storage", "with no key and plaintext rows, doctor prints nothing new")
}

func TestDoctorPretty_OAuthTokenStorageAbsentFromOlderDaemon(t *testing.T) {
	out := renderDoctorPretty(t, map[string]interface{}{"total_issues": 0})
	assert.NotContains(t, out, "OAuth Token Storage")
}

func TestDoctorJSON_OAuthTokenStorageIsPassedThrough(t *testing.T) {
	prev := doctorOutput
	doctorOutput = "json"
	t.Cleanup(func() { doctorOutput = prev })
	out := captureOutput(func() {
		_ = outputDiagnostics(oauthStorageDiag(map[string]interface{}{
			"encryption_enabled": true, "key_id": "0123456789abcdef", "sealed": 1,
		}), nil, nil, "", nil)
	})
	assert.Contains(t, out, `"oauth_token_storage"`)
	assert.Contains(t, out, `"key_id": "0123456789abcdef"`)
}
