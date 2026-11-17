package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T016 (SC-001): after the sweep and a compaction, no seeded access
// token, refresh token or client secret is anywhere in the database file; a
// login written while the key is set never reaches the file at all. The
// no-key control proves the scan sees the markers when they are there.

func rawFileMarkers(prefix string, i int) []string {
	return []string{
		fmt.Sprintf("%sACCESS-MARKER-%d", prefix, i),
		fmt.Sprintf("%sREFRESH-MARKER-%d", prefix, i),
		fmt.Sprintf("%sSECRET-MARKER-%d", prefix, i),
	}
}

func seedRawFileRows(t *testing.T, dir string) []string {
	t.Helper()
	db := openSealTestDB(t, dir, nil)
	var markers []string
	for i := 0; i < 3; i++ {
		m := rawFileMarkers("SEED-", i)
		markers = append(markers, m...)
		if err := db.SaveOAuthToken(&OAuthTokenRecord{
			ServerName: fmt.Sprintf("srv%d_0123456789abcdef", i), AccessToken: m[0], RefreshToken: m[1],
			ClientID: "cid", ClientSecret: m[2], TokenType: "Bearer",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return markers
}

// loginWrite stores a token and a DCR client the way a login does.
func loginWrite(t *testing.T, db *BoltDB, markers []string) {
	t.Helper()
	const key = "login_0123456789abcdef"
	zero := commitview.CredentialBinding{}
	if _, err := db.SaveOAuthClientIfBound(key, "login", zero, OAuthClientSaveAlways, "cid", markers[2], 4242, "http://127.0.0.1:4242/cb"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateOAuthTokenIfBound(key, "login", zero, func(rec *OAuthTokenRecord) error {
		rec.AccessToken, rec.RefreshToken = markers[0], markers[1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func fileHas(t *testing.T, path string, markers []string) (present []string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range markers {
		if bytes.Contains(b, []byte(m)) {
			present = append(present, m)
		}
	}
	return present
}

func compactInto(t *testing.T, src, dst string) {
	t.Helper()
	s, err := bbolt.Open(src, 0600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := bbolt.Open(dst, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bbolt.Compact(d, s, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthTokensNotInRawFile(t *testing.T) {
	run := func(t *testing.T, k *tokenseal.Key) (live, compacted, seed, login []string) {
		dir := t.TempDir()
		seed = seedRawFileRows(t, dir)
		m, err := NewManager(dir, zap.NewNop().Sugar(), WithOAuthTokenKey(k))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.SealPlaintextOAuthTokens(context.Background()); err != nil {
			t.Fatal(err)
		}
		login = rawFileMarkers("LOGIN-", 0)
		loginWrite(t, m.GetBoltDB(), login)
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "config.db")
		live = fileHas(t, path, login)
		out := filepath.Join(t.TempDir(), "compacted.db")
		compactInto(t, path, out)
		compacted = fileHas(t, out, append(append([]string(nil), seed...), login...))
		return live, compacted, seed, login
	}

	t.Run("control without a key", func(t *testing.T) {
		live, compacted, seed, login := run(t, nil)
		if len(live) != len(login) {
			t.Fatalf("control: login markers in the live file %v, want all of %v (scan is vacuous)", live, login)
		}
		if len(compacted) != len(seed)+len(login) {
			t.Fatalf("control: compacted file holds %v, want every marker", compacted)
		}
	})
	t.Run("with a key", func(t *testing.T) {
		live, compacted, _, _ := run(t, sealTestKey(t, 1))
		if len(live) != 0 {
			t.Fatalf("login written with a key reached the live file in plaintext: %v", live)
		}
		if len(compacted) != 0 {
			t.Fatalf("markers left in the compacted file: %v", compacted)
		}
	})
}
