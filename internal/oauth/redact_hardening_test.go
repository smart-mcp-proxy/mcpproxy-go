package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #872 (Codex review round): hardening tests for the redaction helpers
// and the new mask-aware write-path un-maskers.

// FINDING 3 — RedactURLQueryParams must mask SigV4 / auth-token param variants
// that exact-name matching misses.
func TestRedactURLQueryParams_NormalizedVariants(t *testing.T) {
	cases := []struct {
		name     string
		rawURL   string
		leak     string // secret that must NOT survive
		keepHost string
	}{
		{
			name:     "X-Amz-Credential",
			rawURL:   "https://s3.example.com/o?X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2Fus-east-1&X-Amz-Date=20260101",
			leak:     "AKIAIOSFODNN7EXAMPLE",
			keepHost: "s3.example.com",
		},
		{
			name:     "X-Amz-Signature",
			rawURL:   "https://s3.example.com/o?X-Amz-Signature=abcdef0123456789deadbeef&foo=bar",
			leak:     "abcdef0123456789deadbeef",
			keepHost: "s3.example.com",
		},
		{
			name:     "X-Amz-Security-Token",
			rawURL:   "https://s3.example.com/o?X-Amz-Security-Token=FwoGZXIvYXdzECEXAMPLETOKEN&x=1",
			leak:     "FwoGZXIvYXdzECEXAMPLETOKEN",
			keepHost: "s3.example.com",
		},
		{
			name:     "authToken camelCase",
			rawURL:   "https://api.example.com/mcp?authToken=supersecrettokenvalue&team=eng",
			leak:     "supersecrettokenvalue",
			keepHost: "api.example.com",
		},
		{
			name:     "access-token hyphenated",
			rawURL:   "https://api.example.com/mcp?access-token=hyphensecretvalue0000",
			leak:     "hyphensecretvalue0000",
			keepHost: "api.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactURLQueryParams(tc.rawURL)
			assert.NotContains(t, got, tc.leak, "secret leaked through redaction")
			assert.Contains(t, got, tc.keepHost, "host should stay readable")
		})
	}
}

// A non-sensitive param must remain readable and untouched.
func TestRedactURLQueryParams_NonSensitiveUntouched(t *testing.T) {
	got := RedactURLQueryParams("https://api.example.com/mcp?team=eng&page=2")
	assert.Equal(t, "https://api.example.com/mcp?team=eng&page=2", got)
}

// FINDING 2 — URL-valued env vars (DATABASE_URL, REDIS_URL) whose key does not
// look sensitive still embed a userinfo password that must be masked while the
// host/db stay readable. DSN / CONNECTION_STRING keys are whole-value masked.
func TestRedactEnvValues_ConnectionStrings(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":      "postgres://dbuser:sup3rsecretpw@db.internal:5432/appdb",
		"REDIS_URL":         "redis://:redispassword123@cache.internal:6379/0",
		"DB_DSN":            "user=admin password=dsnsecretvalue host=db",
		"CONNECTION_STRING": "Server=db;User=sa;Password=connstrsecret;",
		"LOG_LEVEL":         "debug",
	}
	got := RedactEnvValues(env)

	// DATABASE_URL: password masked, host/db readable.
	assert.NotContains(t, got["DATABASE_URL"], "sup3rsecretpw")
	assert.Contains(t, got["DATABASE_URL"], "db.internal")
	assert.Contains(t, got["DATABASE_URL"], "appdb")

	// REDIS_URL: password masked, host readable.
	assert.NotContains(t, got["REDIS_URL"], "redispassword123")
	assert.Contains(t, got["REDIS_URL"], "cache.internal")

	// DSN / CONNECTION_STRING keys: whole value masked (sensitive marker).
	assert.NotContains(t, got["DB_DSN"], "dsnsecretvalue")
	assert.NotContains(t, got["CONNECTION_STRING"], "connstrsecret")

	// Ordinary config stays readable.
	assert.Equal(t, "debug", got["LOG_LEVEL"])
}

// FINDING 4 — isConfigReference must require a syntactically complete reference,
// not merely a prefix.
func TestIsConfigReference_FullMatchOnly(t *testing.T) {
	assert.True(t, isConfigReference("${env:MY_TOKEN}"))
	assert.True(t, isConfigReference("${keyring:svc/user}"))
	assert.False(t, isConfigReference("${env:MY_TOKEN}garbage"))
	assert.False(t, isConfigReference("prefix${env:MY_TOKEN}"))
	assert.False(t, isConfigReference("${env:MY_TOKEN"))
	assert.False(t, isConfigReference("plainsecret"))
	assert.False(t, isConfigReference("${vault:x}"))
}

// A composite value that only starts with a reference must be masked (not
// passed through) by MaskValue and by the URL query redactor.
func TestMaskValue_CompositeReferenceMasked(t *testing.T) {
	got := MaskValue("${env:NAME}garbage")
	assert.NotEqual(t, "${env:NAME}garbage", got)
	assert.Contains(t, got, "••••")
}

// FINDING 1 — mask-aware write path helpers.

func TestUnmaskURL_RestoresEchoedMask(t *testing.T) {
	stored := "https://api.example.com/mcp?apikey=REALSECRETKEY123&team=eng"
	masked := RedactURLQueryParams(stored)
	require.NotEqual(t, stored, masked, "precondition: read path masked the URL")
	require.NotContains(t, masked, "REALSECRETKEY123")

	// Client edits a non-secret part (team=eng -> team=ops) and echoes the
	// masked apikey back verbatim.
	incoming := strings.Replace(masked, "team=eng", "team=ops", 1)
	got := UnmaskURL(incoming, stored)

	assert.Contains(t, got, "apikey=REALSECRETKEY123", "real secret must be restored")
	assert.Contains(t, got, "team=ops", "genuine edit must be preserved")
}

func TestUnmaskURL_UserinfoPassword(t *testing.T) {
	stored := "https://user:realpassword@host.example.com/path"
	masked := RedactURLQueryParams(stored)
	require.NotContains(t, masked, "realpassword")

	got := UnmaskURL(masked, stored)
	assert.Contains(t, got, "realpassword")
}

func TestUnmaskURL_GenuineEditNotClobbered(t *testing.T) {
	stored := "https://api.example.com/mcp?apikey=REALSECRETKEY123"
	// Client actually types a brand-new key — must be persisted verbatim.
	incoming := "https://api.example.com/mcp?apikey=BRANDNEWKEY999"
	got := UnmaskURL(incoming, stored)
	assert.Equal(t, incoming, got)
}

func TestUnmaskURL_NoStoredReturnsIncoming(t *testing.T) {
	incoming := "https://api.example.com/mcp?apikey=whatever"
	assert.Equal(t, incoming, UnmaskURL(incoming, ""))
}

func TestUnmaskEnvValues_RestoresEchoedMask(t *testing.T) {
	stored := map[string]string{
		"API_KEY":   "realapikeysecret",
		"LOG_LEVEL": "debug",
	}
	masked := RedactEnvValues(stored)

	// Client echoes the masked API_KEY back but changes LOG_LEVEL.
	incoming := map[string]string{
		"API_KEY":   masked["API_KEY"],
		"LOG_LEVEL": "info",
	}
	got := UnmaskEnvValues(incoming, stored)
	assert.Equal(t, "realapikeysecret", got["API_KEY"], "masked secret restored")
	assert.Equal(t, "info", got["LOG_LEVEL"], "genuine edit preserved")
}

func TestUnmaskEnvValues_GenuineNewSecretKept(t *testing.T) {
	stored := map[string]string{"API_KEY": "oldsecretvalue"}
	incoming := map[string]string{"API_KEY": "brandnewsecretvalue"}
	got := UnmaskEnvValues(incoming, stored)
	assert.Equal(t, "brandnewsecretvalue", got["API_KEY"])
}

func TestUnmaskHeaders_RestoresEchoedMask(t *testing.T) {
	stored := map[string]string{"Authorization": "Bearer realtokenvalue123"}
	masked := RedactStringHeaders(stored)
	incoming := map[string]string{"Authorization": masked["Authorization"]}
	got := UnmaskHeaders(incoming, stored)
	assert.Equal(t, "Bearer realtokenvalue123", got["Authorization"])
}

// --- Codex round 2 ---

// FINDING 1 (round 2) — UnmaskURL must NOT move a stored secret onto a different
// scheme/host. Redirecting the URL to an attacker host while echoing the mask
// back must leave the mask literal, never restore the real credential.
func TestUnmaskURL_AuthorityChange_DoesNotRestoreQuerySecret(t *testing.T) {
	stored := "https://api.example.com/mcp?apikey=REALSECRETKEY123&team=eng"
	masked := RedactURLQueryParams(stored)
	require.NotContains(t, masked, "REALSECRETKEY123")

	// Attacker swaps the host but keeps the masked apikey.
	attacker := strings.Replace(masked, "api.example.com", "attacker.example.com", 1)
	got := UnmaskURL(attacker, stored)

	assert.NotContains(t, got, "REALSECRETKEY123",
		"stored secret must never be written into a different-host URL")
	assert.Contains(t, got, "attacker.example.com")
}

func TestUnmaskURL_AuthorityChange_DoesNotRestoreUserinfoPassword(t *testing.T) {
	stored := "https://user:realpassword@host.example.com/path"
	masked := RedactURLQueryParams(stored)
	require.NotContains(t, masked, "realpassword")

	attacker := strings.Replace(masked, "host.example.com", "evil.example.com", 1)
	got := UnmaskURL(attacker, stored)
	assert.NotContains(t, got, "realpassword",
		"stored password must not be moved to a different host")
}

func TestUnmaskURL_UsernameChange_DoesNotRestorePassword(t *testing.T) {
	stored := "https://user:realpassword@host.example.com/path"
	masked := RedactURLQueryParams(stored)
	// Same host, but different username — treat as a different principal.
	other := strings.Replace(masked, "user:", "attacker:", 1)
	got := UnmaskURL(other, stored)
	assert.NotContains(t, got, "realpassword",
		"password must not be restored under a different username")
}

func TestUnmaskURL_SchemeChange_DoesNotRestore(t *testing.T) {
	stored := "https://api.example.com/mcp?apikey=REALSECRETKEY123"
	masked := RedactURLQueryParams(stored)
	downgraded := strings.Replace(masked, "https://", "http://", 1)
	got := UnmaskURL(downgraded, stored)
	assert.NotContains(t, got, "REALSECRETKEY123",
		"scheme downgrade must not restore the stored secret")
}

// FINDING 3 (round 2) — duplicate sensitive query keys are masked at every
// occurrence; restoration must be positional, i-th mask ← i-th stored value.
func TestUnmaskURL_DuplicateKeys_PositionalRestore(t *testing.T) {
	stored := "https://api.example.com/mcp?token=FIRSTSECRET111&token=SECONDSECRET222"
	masked := RedactURLQueryParams(stored)
	require.NotContains(t, masked, "FIRSTSECRET111")
	require.NotContains(t, masked, "SECONDSECRET222")

	got := UnmaskURL(masked, stored)
	assert.Contains(t, got, "token=FIRSTSECRET111", "first occurrence must be restored")
	assert.Contains(t, got, "token=SECONDSECRET222", "second occurrence must be restored")
	// Ensure no mask survived.
	assert.NotContains(t, got, "••••")
}

// When the incoming occurrence count for a key differs from stored, positional
// matching is ambiguous — leave the masks literal rather than guess.
func TestUnmaskURL_DuplicateKeys_CountMismatch_LeavesMasks(t *testing.T) {
	stored := "https://api.example.com/mcp?token=FIRSTSECRET111&token=SECONDSECRET222"
	masked := RedactURLQueryParams(stored)
	// Client dropped one occurrence.
	single := strings.SplitN(masked, "&", 2)[0] // keep only the first token=...
	got := UnmaskURL(single, stored)
	assert.NotContains(t, got, "FIRSTSECRET111",
		"count mismatch must not positionally restore a secret")
	assert.NotContains(t, got, "SECONDSECRET222")
}

// FINDING 2 (round 2) — URL-shaped env values must be unmasked component-wise.
func TestUnmaskEnvValues_URLValue_PathEditRestoresPassword(t *testing.T) {
	stored := map[string]string{
		"DATABASE_URL": "postgres://user:realdbpassword@db.internal:5432/olddb",
	}
	masked := RedactEnvValues(stored)
	require.NotContains(t, masked["DATABASE_URL"], "realdbpassword")

	// User edits only the database name (path); host/authority unchanged.
	incoming := map[string]string{
		"DATABASE_URL": strings.Replace(masked["DATABASE_URL"], "olddb", "newdb", 1),
	}
	got := UnmaskEnvValues(incoming, stored)
	assert.Contains(t, got["DATABASE_URL"], "realdbpassword",
		"password must be restored when only the db name changed")
	assert.Contains(t, got["DATABASE_URL"], "newdb", "the db-name edit must persist")
	assert.NotContains(t, got["DATABASE_URL"], "••••", "no mask should survive")
}

func TestUnmaskEnvValues_URLValue_HostEditDoesNotRestorePassword(t *testing.T) {
	stored := map[string]string{
		"DATABASE_URL": "postgres://user:realdbpassword@db.internal:5432/appdb",
	}
	masked := RedactEnvValues(stored)
	incoming := map[string]string{
		"DATABASE_URL": strings.Replace(masked["DATABASE_URL"], "db.internal", "attacker.internal", 1),
	}
	got := UnmaskEnvValues(incoming, stored)
	assert.NotContains(t, got["DATABASE_URL"], "realdbpassword",
		"password must not be moved to a different host")
}
