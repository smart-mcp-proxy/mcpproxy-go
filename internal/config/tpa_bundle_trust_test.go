package config

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tpaTestSigner returns the standard base64 of a deterministic 32-byte value.
// The config package only checks shape (32 raw bytes, canonical encoding); it
// never verifies signatures, so any 32 bytes stand in for a public key.
func tpaTestSigner(seed byte) string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func tpaValidConfig(sec *SecurityConfig) *Config {
	cfg := DefaultConfig()
	cfg.Security = sec
	return cfg
}

func tpaErrorFields(errs []ValidationError) []string {
	out := []string{}
	for _, e := range errs {
		out = append(out, e.Field)
	}
	return out
}

func TestTPAAccessors_Defaults(t *testing.T) {
	sc := &SecurityConfig{}
	assert.False(t, sc.IsRequireSignedBundle())
	assert.Equal(t, TPARootPublisher, sc.EffectiveTPAActiveRoot())
	assert.Empty(t, sc.EffectiveTPAOperatorSigners())
	_, ok := sc.EffectiveTPAOperatorBaseline()
	assert.False(t, ok, "baseline absent by default")
	assert.Equal(t, uint64(0), sc.EffectiveTPAOperatorEpoch())
}

func TestTPAAccessors_NilSafe(t *testing.T) {
	var sc *SecurityConfig
	assert.False(t, sc.IsRequireSignedBundle())
	assert.Equal(t, TPARootPublisher, sc.EffectiveTPAActiveRoot())
	assert.Empty(t, sc.EffectiveTPAOperatorSigners())
	_, ok := sc.EffectiveTPAOperatorBaseline()
	assert.False(t, ok)
	assert.Equal(t, uint64(0), sc.EffectiveTPAOperatorEpoch())
}

func TestTPAAccessors_ExplicitValues(t *testing.T) {
	zero, five := uint64(0), uint64(5)
	sc := &SecurityConfig{
		RequireSignedBundle: true,
		TPAActiveRoot:       TPARootOperator,
		TPAOperatorSigners:  []string{tpaTestSigner(1)},
		TPAOperatorBaseline: &zero,
		TPAOperatorEpoch:    &five,
	}
	assert.True(t, sc.IsRequireSignedBundle())
	assert.Equal(t, TPARootOperator, sc.EffectiveTPAActiveRoot())
	assert.Equal(t, []string{tpaTestSigner(1)}, sc.EffectiveTPAOperatorSigners())
	b, ok := sc.EffectiveTPAOperatorBaseline()
	assert.True(t, ok, "an explicit 0 baseline is present, not absent")
	assert.Equal(t, uint64(0), b)
	assert.Equal(t, uint64(5), sc.EffectiveTPAOperatorEpoch())
}

func TestValidateDetailed_TPATrustRoot(t *testing.T) {
	good1, good2 := tpaTestSigner(1), tpaTestSigner(2)
	short := base64.StdEncoding.EncodeToString(make([]byte, 31))
	long := base64.StdEncoding.EncodeToString(make([]byte, 33))
	// 32 bytes encode to 43 data chars plus one '='. The last data char carries
	// only 4 meaningful bits, so changing it to a different char with the same
	// high bits decodes (leniently) to the same key but is not the canonical
	// encoding. Strict decoding must reject it.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := good1[42]
	idx := strings.IndexByte(alphabet, last)
	nonCanonical := good1[:42] + string(alphabet[idx^1]) + "="
	urlSafe := strings.NewReplacer("+", "-", "/", "_").Replace(
		base64.StdEncoding.EncodeToString(append([]byte{0xfb, 0xff}, make([]byte, 30)...)))
	require.NotContains(t, urlSafe, "+")

	tests := []struct {
		name       string
		sec        *SecurityConfig
		wantFields []string
		wantMsg    string
	}{
		{name: "nil security", sec: nil},
		{name: "empty security", sec: &SecurityConfig{}},
		{name: "publisher root", sec: &SecurityConfig{TPAActiveRoot: "publisher"}},
		{name: "operator root with signers", sec: &SecurityConfig{TPAActiveRoot: "operator", TPAOperatorSigners: []string{good1, good2}}},
		{name: "unknown root", sec: &SecurityConfig{TPAActiveRoot: "vendor"}, wantFields: []string{"security.tpa_active_root"}},
		{name: "root is case sensitive", sec: &SecurityConfig{TPAActiveRoot: "Operator", TPAOperatorSigners: []string{good1}}, wantFields: []string{"security.tpa_active_root"}},
		{name: "operator root without signers", sec: &SecurityConfig{TPAActiveRoot: "operator"}, wantFields: []string{"security.tpa_active_root"}, wantMsg: "operator root declared but no tpa_operator_signers"},
		{name: "not base64", sec: &SecurityConfig{TPAOperatorSigners: []string{good1, "not base64!"}}, wantFields: []string{"security.tpa_operator_signers[1]"}},
		{name: "too short", sec: &SecurityConfig{TPAOperatorSigners: []string{short}}, wantFields: []string{"security.tpa_operator_signers[0]"}},
		{name: "too long", sec: &SecurityConfig{TPAOperatorSigners: []string{long}}, wantFields: []string{"security.tpa_operator_signers[0]"}},
		{name: "url-safe alphabet", sec: &SecurityConfig{TPAOperatorSigners: []string{urlSafe}}, wantFields: []string{"security.tpa_operator_signers[0]"}},
		{name: "non-canonical encoding", sec: &SecurityConfig{TPAOperatorSigners: []string{nonCanonical}}, wantFields: []string{"security.tpa_operator_signers[0]"}},
		{name: "surrounding whitespace", sec: &SecurityConfig{TPAOperatorSigners: []string{" " + good1}}, wantFields: []string{"security.tpa_operator_signers[0]"}},
		{name: "duplicate signer", sec: &SecurityConfig{TPAOperatorSigners: []string{good1, good2, good1}}, wantFields: []string{"security.tpa_operator_signers[2]"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := tpaValidConfig(tc.sec).ValidateDetailed()
			assert.ElementsMatch(t, tc.wantFields, tpaErrorFields(errs), "errors: %v", errs)
			if tc.wantMsg != "" {
				require.NotEmpty(t, errs)
				assert.Contains(t, errs[0].Message, tc.wantMsg)
			}
		})
	}
}

// TestValidate_TPATrustRootDoesNotBrickBoot pins the write-only contract: the
// boot path (Validate -> validateDetailedCore) must keep loading a config whose
// trust-root keys are malformed. The scanner treats an unusable operator root
// as "no operator authority" and surfaces it; refusing to start would take
// every upstream server down over one key.
func TestValidate_TPATrustRootDoesNotBrickBoot(t *testing.T) {
	cfg := tpaValidConfig(&SecurityConfig{
		TPAActiveRoot:      "vendor",
		TPAOperatorSigners: []string{"not base64!", tpaTestSigner(1), tpaTestSigner(1)},
	})
	require.NoError(t, cfg.Validate())
	assert.Empty(t, cfg.validateDetailedCore())
	assert.NotEmpty(t, cfg.ValidateDetailed(), "the same config is rejected on the write path")

	cfg = tpaValidConfig(&SecurityConfig{TPAActiveRoot: "operator"})
	require.NoError(t, cfg.Validate())
}

func TestSecurityConfig_TPAJSONRoundTrip(t *testing.T) {
	t.Run("explicit zero survives", func(t *testing.T) {
		zero := uint64(0)
		in := SecurityConfig{TPAOperatorBaseline: &zero, TPAOperatorEpoch: &zero}
		raw, err := json.Marshal(in)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"tpa_operator_baseline":0`)
		assert.Contains(t, string(raw), `"tpa_operator_epoch":0`)

		var out SecurityConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		require.NotNil(t, out.TPAOperatorBaseline)
		require.NotNil(t, out.TPAOperatorEpoch)
		assert.Equal(t, uint64(0), *out.TPAOperatorBaseline)
		assert.Equal(t, uint64(0), *out.TPAOperatorEpoch)
	})

	t.Run("absence survives", func(t *testing.T) {
		raw, err := json.Marshal(SecurityConfig{})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "tpa_operator_baseline")
		assert.NotContains(t, string(raw), "tpa_operator_epoch")
		assert.NotContains(t, string(raw), "tpa_active_root")
		assert.NotContains(t, string(raw), "tpa_operator_signers")

		var out SecurityConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Nil(t, out.TPAOperatorBaseline)
		assert.Nil(t, out.TPAOperatorEpoch)
	})

	t.Run("require_signed_bundle always serialised", func(t *testing.T) {
		raw, err := json.Marshal(SecurityConfig{})
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"require_signed_bundle":false`)
	})
}
