package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3 T051 (FR-032, US4): `mcpproxy auth tokens status|encrypt|decrypt`.

const (
	tokensAccessMarker  = "TOKENS-ACCESS-MARKER-c91d"
	tokensRefreshMarker = "TOKENS-REFRESH-MARKER-c91d"
)

func tokensKeyB64(seed byte) string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i) + seed
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func tokensKey(t *testing.T, seed byte) *tokenseal.Key {
	t.Helper()
	k, err := tokenseal.ParseKey(tokensKeyB64(seed))
	require.NoError(t, err)
	return k
}

type tokensFixture struct {
	dataDir string
	cfg     *config.Config
}

func newTokensFixture(t *testing.T, keyB64 string) tokensFixture {
	t.Helper()
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.OAuthTokenEncryptionKey = keyB64
	return tokensFixture{dataDir: dir, cfg: cfg}
}

func tokensServerKey(name string) string {
	return oauth.GenerateServerKey(name, "https://"+name+".example.invalid/mcp")
}

// seed opens config.db with key (nil = plaintext), writes the named servers'
// rows and closes it again.
func (f tokensFixture) seed(t *testing.T, key *tokenseal.Key, names ...string) {
	t.Helper()
	m, err := storage.NewManager(f.dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(key))
	require.NoError(t, err)
	for _, n := range names {
		require.NoError(t, m.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
			ServerName: tokensServerKey(n), DisplayName: n,
			AccessToken: tokensAccessMarker + "-" + n, RefreshToken: tokensRefreshMarker + "-" + n,
		}))
	}
	require.NoError(t, m.Close())
}

func (f tokensFixture) putRaw(t *testing.T, bucketKey string, v []byte) {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(f.dataDir, "config.db"), 0o600, nil)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.OAuthTokenBucket)).Put([]byte(bucketKey), v)
	}))
}

func (f tokensFixture) rows(t *testing.T, key *tokenseal.Key) map[string]storage.OAuthRowClass {
	t.Helper()
	m, err := storage.NewManager(f.dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(key))
	require.NoError(t, err)
	defer m.Close()
	rows, err := m.ListOAuthTokenRows()
	require.NoError(t, err)
	out := map[string]storage.OAuthRowClass{}
	for _, r := range rows {
		out[storage.OAuthKeyServerName(r.BucketKey)] = r.Class
	}
	return out
}

func (f tokensFixture) rawValue(t *testing.T, bucketKey string) []byte {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(f.dataDir, "config.db"), 0o600, &bbolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db.Close()
	var out []byte
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(bucketKey))...)
		return nil
	}))
	return out
}

func assertNoSecrets(t *testing.T, out string, keyB64 string) {
	t.Helper()
	assert.NotContains(t, out, tokensAccessMarker)
	assert.NotContains(t, out, tokensRefreshMarker)
	if keyB64 != "" {
		assert.NotContains(t, out, keyB64)
		assert.NotContains(t, out, strings.TrimRight(keyB64, "="))
	}
}

func decodeJSON(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(s), &m), s)
	return m
}

func TestAuthTokensStatus_JSONAndText(t *testing.T) {
	keyB64 := tokensKeyB64(1)
	f := newTokensFixture(t, keyB64)
	f.seed(t, tokensKey(t, 1), "alpha", "beta")
	f.seed(t, nil, "gamma")                                                                                           // plaintext row
	f.putRaw(t, tokensServerKey("delta"), []byte("enc:v1:fedcba9876543210:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")) // another key

	var buf bytes.Buffer
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "json"))
	assertNoSecrets(t, buf.String(), keyB64)
	doc := decodeJSON(t, buf.String())
	section := doc["oauth_token_storage"].(map[string]interface{})
	assert.Equal(t, true, section["encryption_enabled"])
	assert.NotEmpty(t, section["key_id"])
	assert.EqualValues(t, 2, section["sealed"])
	assert.EqualValues(t, 1, section["plaintext"])
	assert.EqualValues(t, 1, section["undecryptable"])
	assert.Equal(t, "literal", section["key_source_kind"])
	reasons := section["undecryptable_by_reason"].(map[string]interface{})
	assert.EqualValues(t, 1, reasons["key_mismatch"])
	rows := doc["rows"].([]interface{})
	require.Len(t, rows, 4)
	states := map[string]string{}
	for _, r := range rows {
		row := r.(map[string]interface{})
		states[row["server"].(string)] = row["state"].(string)
	}
	assert.Equal(t, map[string]string{"alpha": "sealed", "beta": "sealed", "gamma": "plaintext", "delta": "undecryptable"}, states)

	buf.Reset()
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "table"))
	text := buf.String()
	assertNoSecrets(t, text, keyB64)
	assert.Contains(t, text, "alpha")
	assert.Contains(t, text, "key_mismatch")
	assert.Contains(t, text, "mcpproxy auth tokens encrypt", "plaintext rows with a key point at the encrypt command")

	buf.Reset()
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "yaml"))
	assertNoSecrets(t, buf.String(), keyB64)
	assert.Contains(t, buf.String(), "oauth_token_storage:")
	assert.Contains(t, buf.String(), "encryption_enabled: true")
}

func TestAuthTokensStatus_NoKeyReportsOffAndRowsAsIs(t *testing.T) {
	f := newTokensFixture(t, "")
	f.seed(t, nil, "alpha")
	var buf bytes.Buffer
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "json"))
	section := decodeJSON(t, buf.String())["oauth_token_storage"].(map[string]interface{})
	assert.Equal(t, false, section["encryption_enabled"])
	assert.EqualValues(t, 1, section["plaintext"])
	assert.Empty(t, section["warnings"])
}

func TestAuthTokensStatus_ReportsCompactionPending(t *testing.T) {
	keyB64 := tokensKeyB64(1)
	f := newTokensFixture(t, keyB64)
	f.seed(t, nil, "alpha")
	var buf bytes.Buffer
	require.NoError(t, authTokensEncrypt(context.Background(), f.cfg, &buf, "table", false))
	buf.Reset()
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "table"))
	assert.Contains(t, buf.String(), "mcpproxy db compact --keep-backup=false")

	buf.Reset()
	require.NoError(t, authTokensStatus(context.Background(), f.cfg, &buf, "json"))
	section := decodeJSON(t, buf.String())["oauth_token_storage"].(map[string]interface{})
	assert.Equal(t, true, section["compaction_advised"])
}

func TestAuthTokensEncrypt_SealsAndIsIdempotent(t *testing.T) {
	keyB64 := tokensKeyB64(1)
	f := newTokensFixture(t, keyB64)
	f.seed(t, nil, "alpha", "beta", "gamma")

	var buf bytes.Buffer
	require.NoError(t, authTokensEncrypt(context.Background(), f.cfg, &buf, "json", false))
	assertNoSecrets(t, buf.String(), keyB64)
	doc := decodeJSON(t, buf.String())
	assert.EqualValues(t, 3, doc["sealed"])
	assert.Equal(t, false, doc["dry_run"])
	assert.Equal(t, map[string]storage.OAuthRowClass{"alpha": "sealed", "beta": "sealed", "gamma": "sealed"}, f.rows(t, tokensKey(t, 1)))
	raw := f.rawValue(t, tokensServerKey("alpha"))
	assert.True(t, bytes.HasPrefix(raw, []byte("enc:v1:")))
	assert.NotContains(t, string(raw), tokensAccessMarker)

	buf.Reset()
	require.NoError(t, authTokensEncrypt(context.Background(), f.cfg, &buf, "json", false), "a second run changes nothing and succeeds")
	assert.EqualValues(t, 0, decodeJSON(t, buf.String())["sealed"])
}

func TestAuthTokensEncrypt_DryRunWritesNothing(t *testing.T) {
	f := newTokensFixture(t, tokensKeyB64(1))
	f.seed(t, nil, "alpha", "beta")
	before := f.rawValue(t, tokensServerKey("alpha"))

	var buf bytes.Buffer
	require.NoError(t, authTokensEncrypt(context.Background(), f.cfg, &buf, "json", true))
	doc := decodeJSON(t, buf.String())
	assert.EqualValues(t, 2, doc["sealed"], "the count a real run would seal")
	assert.Equal(t, true, doc["dry_run"])
	assert.Equal(t, before, f.rawValue(t, tokensServerKey("alpha")))
	assert.Equal(t, map[string]storage.OAuthRowClass{"alpha": "plaintext", "beta": "plaintext"}, f.rows(t, tokensKey(t, 1)))
	m, err := storage.NewManager(f.dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(tokensKey(t, 1)))
	require.NoError(t, err)
	defer m.Close()
	advised, err := m.OAuthCompactAdvised()
	require.NoError(t, err)
	assert.False(t, advised, "a dry run must not set the compaction marker")
}

func TestAuthTokensEncrypt_ExitsOneWhenRowsAreSkippedOrUndecryptable(t *testing.T) {
	f := newTokensFixture(t, tokensKeyB64(1))
	f.seed(t, nil, "alpha")
	f.putRaw(t, tokensServerKey("broken"), []byte("{broken"))

	var buf bytes.Buffer
	err := authTokensEncrypt(context.Background(), f.cfg, &buf, "json", false)
	require.Error(t, err)
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	doc := decodeJSON(t, buf.String())
	assert.EqualValues(t, 1, doc["sealed"], "the good row is sealed and the bad one left alone")
	skipped := doc["skipped"].([]interface{})
	require.Len(t, skipped, 1)
	assert.Equal(t, "malformed", skipped[0].(map[string]interface{})["reason"])
	assert.Equal(t, "broken", skipped[0].(map[string]interface{})["server"])
	assert.Equal(t, []byte("{broken"), f.rawValue(t, tokensServerKey("broken")))
}

func TestAuthTokensEncryptDecrypt_NoKeyIsExit4(t *testing.T) {
	f := newTokensFixture(t, "")
	f.seed(t, nil, "alpha")
	before := f.rawValue(t, tokensServerKey("alpha"))
	var buf bytes.Buffer

	err := authTokensEncrypt(context.Background(), f.cfg, &buf, "table", false)
	require.Error(t, err)
	assert.Equal(t, ExitCodeConfigError, classifyError(err))
	err = authTokensDecrypt(context.Background(), f.cfg, &buf, "table", true, false)
	require.Error(t, err)
	assert.Equal(t, ExitCodeConfigError, classifyError(err))
	assert.Equal(t, before, f.rawValue(t, tokensServerKey("alpha")))
}

func TestAuthTokensDecrypt_RequiresConfirmation(t *testing.T) {
	f := newTokensFixture(t, tokensKeyB64(1))
	f.seed(t, tokensKey(t, 1), "alpha")
	before := f.rawValue(t, tokensServerKey("alpha"))
	var buf bytes.Buffer
	err := authTokensDecrypt(context.Background(), f.cfg, &buf, "table", false, false)
	require.Error(t, err)
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	assert.Contains(t, err.Error(), "--yes")
	assert.Equal(t, before, f.rawValue(t, tokensServerKey("alpha")), "nothing is written without --yes")
}

func TestAuthTokensDecrypt_RewritesEveryRowAsPlaintext(t *testing.T) {
	keyB64 := tokensKeyB64(1)
	f := newTokensFixture(t, keyB64)
	// several hundred rows spanning many leaf pages
	var names []string
	for i := 0; i < 300; i++ {
		names = append(names, fmt.Sprintf("srv%03d", i))
	}
	f.seed(t, tokensKey(t, 1), names...)

	var buf bytes.Buffer
	require.NoError(t, authTokensDecrypt(context.Background(), f.cfg, &buf, "json", true, false))
	assertNoSecrets(t, buf.String(), keyB64)
	doc := decodeJSON(t, buf.String())
	assert.EqualValues(t, 300, doc["decrypted"])
	steps := strings.Join(toStrings(doc["next_steps"]), "\n")
	assert.Contains(t, steps, "db compact --keep-backup=false")
	assert.Contains(t, steps, "config.db.bak")

	// the downgrade simulation: every row decodes with plain json.Unmarshal
	for _, n := range names {
		var rec storage.OAuthTokenRecord
		require.NoError(t, json.Unmarshal(f.rawValue(t, tokensServerKey(n)), &rec), n)
		assert.Equal(t, tokensAccessMarker+"-"+n, rec.AccessToken)
	}
	for _, c := range f.rows(t, nil) {
		assert.Equal(t, storage.OAuthRowPlaintext, c)
	}
}

func toStrings(v interface{}) []string {
	var out []string
	if arr, ok := v.([]interface{}); ok {
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestAuthTokensDecrypt_RefusesUndecryptableRowsUnlessToldToLeaveThem(t *testing.T) {
	f := newTokensFixture(t, tokensKeyB64(1))
	f.seed(t, tokensKey(t, 1), "alpha")
	f.seed(t, tokensKey(t, 9), "other") // sealed under another key
	before := f.rawValue(t, tokensServerKey("alpha"))

	var buf bytes.Buffer
	err := authTokensDecrypt(context.Background(), f.cfg, &buf, "table", true, false)
	require.Error(t, err)
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	assert.Contains(t, err.Error(), "--leave-undecryptable")
	assert.Contains(t, err.Error(), "other")
	assert.Equal(t, before, f.rawValue(t, tokensServerKey("alpha")), "a refusal writes nothing")

	buf.Reset()
	err = authTokensDecrypt(context.Background(), f.cfg, &buf, "json", true, true)
	require.Error(t, err, "rows left sealed still exit non-zero (FR-032)")
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	doc := decodeJSON(t, buf.String())
	assert.EqualValues(t, 1, doc["decrypted"])
	left := doc["undecryptable"].([]interface{})
	require.Len(t, left, 1)
	assert.Equal(t, "other", left[0].(map[string]interface{})["server"])
	assert.Equal(t, "key_mismatch", left[0].(map[string]interface{})["reason"])
	var rec storage.OAuthTokenRecord
	require.NoError(t, json.Unmarshal(f.rawValue(t, tokensServerKey("alpha")), &rec))
}

func TestAuthTokens_LockedDatabaseIsExit3(t *testing.T) {
	f := newTokensFixture(t, tokensKeyB64(1))
	f.seed(t, nil, "alpha")
	holder, err := storage.NewManager(f.dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(tokensKey(t, 1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() }) // after the parallel subtests

	// bbolt waits for its lock before giving up, so run the three in parallel.
	for name, run := range map[string]func(*bytes.Buffer) error{
		"status":  func(b *bytes.Buffer) error { return authTokensStatus(context.Background(), f.cfg, b, "table") },
		"encrypt": func(b *bytes.Buffer) error { return authTokensEncrypt(context.Background(), f.cfg, b, "table", false) },
		"decrypt": func(b *bytes.Buffer) error {
			return authTokensDecrypt(context.Background(), f.cfg, b, "table", true, false)
		},
	} {
		run := run
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := run(&buf)
			require.Error(t, err)
			assert.Equal(t, ExitCodeDBLocked, classifyError(err))
		})
	}
}

func TestAuthTokens_EnvKeyFileSuppliesTheKeyWhenTheConfigReferenceIsUnreadable(t *testing.T) {
	keyB64 := tokensKeyB64(1)
	f := newTokensFixture(t, "${file:"+filepath.Join(t.TempDir(), "only-inside-the-service", "key")+"}")
	f.seed(t, tokensKey(t, 1), "alpha")

	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte(keyB64+"\n"), 0o600))
	t.Setenv(config.EnvOAuthTokenKeyFile, keyFile)

	var buf bytes.Buffer
	require.NoError(t, authTokensDecrypt(context.Background(), f.cfg, &buf, "json", true, false))
	assertNoSecrets(t, buf.String(), keyB64)
	assert.NotContains(t, buf.String(), keyFile, "the key source path is never printed")
	var rec storage.OAuthTokenRecord
	require.NoError(t, json.Unmarshal(f.rawValue(t, tokensServerKey("alpha")), &rec))
}

func TestAuthTokens_UnreadableKeyReferenceIsExit4AndTouchesNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "unmounted", "oauth-key")
	f := newTokensFixture(t, "${file:"+missing+"}")
	f.seed(t, nil, "alpha")
	before := f.rawValue(t, tokensServerKey("alpha"))
	var buf bytes.Buffer
	for name, run := range map[string]func() error{
		"status":  func() error { return authTokensStatus(context.Background(), f.cfg, &buf, "table") },
		"encrypt": func() error { return authTokensEncrypt(context.Background(), f.cfg, &buf, "table", false) },
		"decrypt": func() error { return authTokensDecrypt(context.Background(), f.cfg, &buf, "table", true, false) },
	} {
		err := run()
		require.Error(t, err, name)
		assert.Equal(t, ExitCodeConfigError, classifyError(err), name)
		assert.NotContains(t, err.Error(), missing, name)
	}
	assert.Equal(t, before, f.rawValue(t, tokensServerKey("alpha")))
}

func TestAuthTokensCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"status", "encrypt", "decrypt"} {
		c, _, err := authCmd.Find([]string{"tokens", name})
		require.NoError(t, err)
		assert.Equal(t, name, c.Name())
	}
	c, _, _ := authCmd.Find([]string{"tokens", "encrypt"})
	assert.NotNil(t, c.Flags().Lookup("dry-run"))
	c, _, _ = authCmd.Find([]string{"tokens", "decrypt"})
	assert.NotNil(t, c.Flags().Lookup("yes"))
	assert.NotNil(t, c.Flags().Lookup("leave-undecryptable"))
}
