package storage

import (
	"encoding/base64"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 SC-005 (informational): what sealing costs on the read path.
// Run: go test -run '^$' -bench BenchmarkGetOAuthToken -benchmem ./internal/storage/

func benchGetOAuthToken(b *testing.B, key *tokenseal.Key) {
	b.Helper()
	db, err := NewBoltDB(b.TempDir(), zap.NewNop().Sugar(), WithOAuthTokenKey(key))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })

	const name = "bench_0123456789abcdef"
	rec := &OAuthTokenRecord{
		ServerName:   name,
		AccessToken:  strings.Repeat("a", 600),
		RefreshToken: strings.Repeat("r", 300),
		TokenType:    "Bearer",
		ClientID:     "client",
		ClientSecret: strings.Repeat("s", 40),
		RedirectURI:  "http://127.0.0.1:12345/oauth/callback",
	}
	if err := db.SaveOAuthToken(rec); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := db.GetOAuthToken(name)
		if err != nil || got == nil || got.AccessToken == "" {
			b.Fatalf("GetOAuthToken: %v %v", got, err)
		}
	}
}

func BenchmarkGetOAuthTokenPlaintext(b *testing.B) { benchGetOAuthToken(b, nil) }

func BenchmarkGetOAuthTokenSealed(b *testing.B) {
	key, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		b.Fatal(err)
	}
	benchGetOAuthToken(b, key)
}
