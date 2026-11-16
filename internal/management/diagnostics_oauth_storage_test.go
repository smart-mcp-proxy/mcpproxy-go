package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3 T050 (FR-030): the OAuth token storage section of doctor.

type oauthStorageRuntime struct {
	*mockRuntimeOperations
	st  OAuthTokenStorageState
	err error
}

func nopSugar() *zap.SugaredLogger { return zap.NewNop().Sugar() }

func newOAuthStorageService(t *testing.T, rt RuntimeOperations) *service {
	t.Helper()
	return NewService(rt, &config.Config{}, "", &mockEventEmitter{}, nil, nopSugar()).(*service)
}

func (r *oauthStorageRuntime) OAuthTokenStorageState() (OAuthTokenStorageState, error) {
	return r.st, r.err
}

func mixedState() OAuthTokenStorageState {
	return OAuthTokenStorageState{
		KeyID:     "0123456789abcdef",
		KeySource: config.KeySource{Kind: config.KeySourceRefFile, InDataDir: false},
		Rows: []storage.OAuthTokenRow{
			{BucketKey: "alpha_0123456789abcdef", Class: storage.OAuthRowSealed, RowKID: "0123456789abcdef"},
			{BucketKey: "beta_0123456789abcdef", Class: storage.OAuthRowSealed, RowKID: "0123456789abcdef"},
			{BucketKey: "gamma_0123456789abcdef", Class: storage.OAuthRowPlaintext},
			{BucketKey: "delta_0123456789abcdef", Class: storage.OAuthRowUndecryptable, RowKID: "fedcba9876543210", Reason: tokenseal.ReasonKeyMismatch},
			{BucketKey: "eps_0123456789abcdef", Class: storage.OAuthRowUndecryptable, Reason: tokenseal.ReasonNoKey, RowKID: "0123456789abcdef"},
			{BucketKey: "zeta_0123456789abcdef", Class: storage.OAuthRowUndecryptable, Reason: tokenseal.ReasonMalformed},
		},
	}
}

func TestBuildOAuthTokenStorageReport_PartitionAndReasons(t *testing.T) {
	rep := BuildOAuthTokenStorageReport(mixedState(), nil, true)
	require.NotNil(t, rep)
	assert.True(t, rep.EncryptionEnabled)
	assert.Equal(t, "0123456789abcdef", rep.KeyID)
	assert.Equal(t, "ref_file", rep.KeySourceKind)
	assert.Equal(t, 2, rep.Sealed)
	assert.Equal(t, 1, rep.Plaintext)
	assert.Equal(t, 3, rep.Undecryptable)
	assert.Equal(t, 6, rep.Sealed+rep.Plaintext+rep.Undecryptable, "the three counts partition the rows")
	assert.Equal(t, 1, rep.UndecryptableByReason.KeyMismatch, "a key_mismatch row is counted once, under undecryptable")
	assert.Equal(t, 1, rep.UndecryptableByReason.NoKey)
	assert.Equal(t, 1, rep.UndecryptableByReason.Malformed)
	assert.Equal(t, 0, rep.UndecryptableByReason.AuthFailed)
	assert.Equal(t, 0, rep.UndecryptableByReason.UnsupportedVersion)
	require.Len(t, rep.Servers, 3)
	byServer := map[string]contracts.OAuthTokenStorageServer{}
	for _, s := range rep.Servers {
		byServer[s.Server] = s
	}
	assert.Equal(t, "key_mismatch", byServer["delta"].Reason)
	assert.Equal(t, "fedcba9876543210", byServer["delta"].RowKeyID)
	assert.Equal(t, "delta_0123456789abcdef", byServer["delta"].BucketKey)
	assert.Equal(t, "malformed", byServer["zeta"].Reason)
}

func TestBuildOAuthTokenStorageReport_Warnings(t *testing.T) {
	base := mixedState()
	cases := []struct {
		name string
		mod  func(*OAuthTokenStorageState)
		want []string
		not  []string
	}{
		{"literal", func(s *OAuthTokenStorageState) { s.KeySource = config.KeySource{Kind: config.KeySourceLiteral} }, []string{"literal_key"}, []string{"key_file_in_data_dir"}},
		{"in data dir", func(s *OAuthTokenStorageState) {
			s.KeySource = config.KeySource{Kind: config.KeySourceEnvFile, InDataDir: true}
		}, []string{"key_file_in_data_dir"}, []string{"literal_key"}},
		{"plaintext with key", func(s *OAuthTokenStorageState) {}, []string{"plaintext_rows_with_key"}, nil},
		{"compaction", func(s *OAuthTokenStorageState) { s.CompactAdvised = true }, []string{"compaction_advised"}, nil},
		{"backup", func(s *OAuthTokenStorageState) { s.BackupPresent = true }, []string{"backup_file_present"}, nil},
		{"clean", func(s *OAuthTokenStorageState) {
			s.Rows = []storage.OAuthTokenRow{{BucketKey: "a_0123456789abcdef", Class: storage.OAuthRowSealed, RowKID: "0123456789abcdef"}}
		}, nil, []string{"literal_key", "key_file_in_data_dir", "plaintext_rows_with_key", "compaction_advised", "backup_file_present"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := base
			c.mod(&st)
			rep := BuildOAuthTokenStorageReport(st, nil, true)
			for _, w := range c.want {
				assert.Contains(t, rep.Warnings, w)
			}
			for _, w := range c.not {
				assert.NotContains(t, rep.Warnings, w)
			}
		})
	}
	t.Run("compaction flag mirrors marker", func(t *testing.T) {
		st := base
		st.CompactAdvised = true
		assert.True(t, BuildOAuthTokenStorageReport(st, nil, true).CompactionAdvised)
		st.CompactAdvised = false
		assert.False(t, BuildOAuthTokenStorageReport(st, nil, true).CompactionAdvised)
	})
}

func TestBuildOAuthTokenStorageReport_EncryptionOff(t *testing.T) {
	rep := BuildOAuthTokenStorageReport(OAuthTokenStorageState{
		Rows: []storage.OAuthTokenRow{{BucketKey: "a_0123456789abcdef", Class: storage.OAuthRowPlaintext}},
	}, nil, true)
	assert.False(t, rep.EncryptionEnabled)
	assert.Equal(t, "", rep.KeyID)
	assert.Equal(t, 1, rep.Plaintext)
	assert.Empty(t, rep.Warnings, "no key: no warnings (plaintext rows are the normal state)")
}

func TestBuildOAuthTokenStorageReport_ScopedCaller(t *testing.T) {
	st := mixedState()
	st.CompactAdvised = true
	st.BackupPresent = true
	st.KeySource = config.KeySource{Kind: config.KeySourceLiteral}
	visible := func(name string) bool { return name == "alpha" || name == "delta" }
	rep := BuildOAuthTokenStorageReport(st, visible, false)
	assert.Equal(t, 1, rep.Sealed)
	assert.Equal(t, 0, rep.Plaintext)
	assert.Equal(t, 1, rep.Undecryptable)
	require.Len(t, rep.Servers, 1)
	assert.Equal(t, "delta", rep.Servers[0].Server)
	assert.Equal(t, "", rep.Servers[0].RowKeyID, "key ids are administrator-only")
	assert.Equal(t, "", rep.KeyID)
	assert.Equal(t, "", rep.KeySourceKind)
	assert.Empty(t, rep.Warnings)
	assert.False(t, rep.CompactionAdvised)
}

func TestDoctor_OAuthTokenStorageSection(t *testing.T) {
	rt := &oauthStorageRuntime{mockRuntimeOperations: newMockRuntime(), st: mixedState()}
	rt.servers = []map[string]interface{}{{"name": "alpha"}}
	svc := newOAuthStorageService(t, rt)

	diag, err := svc.Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	require.NotNil(t, diag.OAuthTokenStorage)
	assert.Equal(t, 3, diag.OAuthTokenStorage.Undecryptable)

	// one runtime warning per undecryptable server, naming the reason
	var warned []string
	for _, w := range diag.RuntimeWarnings {
		if strings.Contains(w, "OAuth token") {
			warned = append(warned, w)
		}
	}
	require.Len(t, warned, 3)
	joined := strings.Join(warned, "\n")
	assert.Contains(t, joined, "delta")
	assert.Contains(t, joined, "key_mismatch")
	assert.GreaterOrEqual(t, diag.TotalIssues, 3, "undecryptable servers count as issues")
}

func TestDoctor_OAuthTokenStorageScopedCallerSeesOnlyItsServers(t *testing.T) {
	rt := &oauthStorageRuntime{mockRuntimeOperations: newMockRuntime(), st: mixedState()}
	rt.servers = []map[string]interface{}{{"name": "alpha"}, {"name": "delta"}}
	svc := newOAuthStorageService(t, rt)

	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AllowedServers: []string{"delta"},
	})
	diag, err := svc.Doctor(scoped)
	require.NoError(t, err)
	require.NotNil(t, diag.OAuthTokenStorage)
	assert.Equal(t, 1, diag.OAuthTokenStorage.Undecryptable)
	assert.Equal(t, 0, diag.OAuthTokenStorage.Sealed)
	assert.Equal(t, "", diag.OAuthTokenStorage.KeyID)
	for _, w := range diag.RuntimeWarnings {
		assert.NotContains(t, w, "eps")
		assert.NotContains(t, w, "zeta")
	}
}

func TestDoctor_OAuthTokenStorageOutputHasNoKeyMaterial(t *testing.T) {
	const secretRef = "/very/secret/path/to/oauth-key-file"
	st := mixedState()
	st.CompactAdvised = true
	rt := &oauthStorageRuntime{mockRuntimeOperations: newMockRuntime(), st: st}
	cfg := &config.Config{OAuthTokenEncryptionKey: "${file:" + secretRef + "}"}
	svc := NewService(rt, cfg, "", &mockEventEmitter{}, nil, nopSugar()).(*service)

	diag, err := svc.Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	b, err := json.Marshal(diag)
	require.NoError(t, err)
	assert.NotContains(t, string(b), secretRef, "the key reference is never printed")
	assert.NotContains(t, string(b), "oauth-key-file")
}

func TestDoctor_NoStorageSourceLeavesSectionOut(t *testing.T) {
	svc := doctorService(t, &config.Config{}, nil)
	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	assert.Nil(t, diag.OAuthTokenStorage)
}

func TestDoctor_UndecryptableServerIsNotAParameterMismatch(t *testing.T) {
	rt := &oauthStorageRuntime{mockRuntimeOperations: newMockRuntime(), st: mixedState()}
	rt.servers = []map[string]interface{}{{
		"name":       "delta",
		"last_error": "stored OAuth token cannot be decrypted: sealed with a different key",
		"error_code": "MCPX_OAUTH_TOKEN_UNDECRYPTABLE",
		"health": map[string]interface{}{
			"level": "unhealthy", "admin_state": "enabled", "action": "configure",
			"detail": "stored OAuth token cannot be decrypted: sealed with a different key",
		},
	}}
	diag, err := newOAuthStorageService(t, rt).Doctor(context.Background())
	require.NoError(t, err)
	assert.Empty(t, diag.OAuthIssues, "the configure action of an undecryptable row is not an OAuth provider parameter mismatch")
	assert.Empty(t, diag.OAuthRequired)
}
