package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Issue #1146, review round 3. The header-name matcher recognised a marker only
// as a WHOLE delimiter-separated segment, so a CamelCase custom header —
// `X-AuthToken`, `X-SecretValue` — matched nothing and its (opaque, unshaped)
// value passed through RedactSensitiveData untouched. Splitting on case
// transitions as well as delimiters closes that without reopening the
// substring false positives the segment rule was introduced to prevent.
func TestIsSensitiveHeaderName_CamelCaseCustomHeaders(t *testing.T) {
	sensitive := []string{
		"X-AuthToken",
		"X-SecretValue",
		"XApiKeyHeader",
		"X-SessionCookie",
		"MyCredentialHeader",
		// Still covered by the original delimiter-separated rule.
		"X-CUSTOM-TOKEN",
		"x-custom-token",
		"Authorization",
	}
	for _, name := range sensitive {
		assert.Truef(t, IsSensitiveHeaderName(name), "%s must be treated as credential-bearing", name)
	}

	// The false positives the segment rule exists to prevent must stay clear:
	// "Author" contains "auth", "Monkey" contains "key", "Passage" contains
	// "pass".
	benign := []string{
		"X-Author-ID",
		"X-Monkey-ID",
		"X-AuthorName",
		"X-MonkeyId",
		"X-PassageId",
		"Content-Type",
		"Accept",
	}
	for _, name := range benign {
		assert.Falsef(t, IsSensitiveHeaderName(name), "%s must stay readable", name)
	}
}

// The audit/activity surfaces need a mask that carries no length and no
// trailing bytes, while the interactive surfaces keep MaskValue's recognisable
// rendering. Both must still pass ${keyring:…}/${env:…} references through,
// because those are labels rather than secrets.
func TestAuditMaskValue(t *testing.T) {
	assert.Equal(t, "••••", AuditMaskValue("sk-live-abcdef1234567890"))
	assert.Equal(t, "••••", AuditMaskValue("ab"))
	assert.Equal(t, "(empty)", AuditMaskValue(""))
	assert.Equal(t, "${keyring:gh}", AuditMaskValue("${keyring:gh}"))
	assert.Equal(t, "${env:GITHUB_TOKEN}", AuditMaskValue("${env:GITHUB_TOKEN}"))

	assert.NotContains(t, AuditMaskValue("sk-live-abcdef1234567890"), "chars")
	assert.NotContains(t, AuditMaskValue("sk-live-abcdef1234567890"), "90")
}

// The *With variants let a caller choose the mask without duplicating the
// "which name is sensitive" rules; passing MaskValue must reproduce the
// existing exported behaviour exactly.
func TestRedactWithVariants_HonourTheSuppliedMask(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer abcdefgh", "Accept": "application/json"}
	assert.Equal(t, RedactStringHeaders(headers), RedactStringHeadersWith(headers, MaskValue))
	assert.Equal(t, "••••", RedactStringHeadersWith(headers, AuditMaskValue)["Authorization"])
	assert.Equal(t, "application/json", RedactStringHeadersWith(headers, AuditMaskValue)["Accept"])

	env := map[string]string{"API_KEY": "sk-live-abcdef1234567890", "LOG_LEVEL": "debug"}
	assert.Equal(t, RedactEnvValues(env), RedactEnvValuesWith(env, MaskValue))
	assert.Equal(t, "••••", RedactEnvValuesWith(env, AuditMaskValue)["API_KEY"])
	assert.Equal(t, "debug", RedactEnvValuesWith(env, AuditMaskValue)["LOG_LEVEL"])

	const rawURL = "https://user:hunter2pass@host.test/mcp?token=leakmeleakmeleakme&page=2"
	assert.Equal(t, RedactURLQueryParams(rawURL), RedactURLQueryParamsWith(rawURL, MaskValue))
	audited := RedactURLQueryParamsWith(rawURL, AuditMaskValue)
	assert.NotContains(t, audited, "leakmeleakmeleakme")
	assert.NotContains(t, audited, "hunter2pass")
	assert.NotContains(t, audited, "chars")
	assert.Contains(t, audited, "page=2")
}

func TestIsConfigReference_Exported(t *testing.T) {
	assert.True(t, IsConfigReference("${keyring:gh}"))
	assert.True(t, IsConfigReference("${env:GITHUB_TOKEN}"))
	assert.False(t, IsConfigReference("${env:GITHUB_TOKEN}garbage"))
	assert.False(t, IsConfigReference("plain"))
}
