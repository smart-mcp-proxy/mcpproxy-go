package storage

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T011 (FR-011, SC-003): with the key option explicitly nil, the
// bytes stored for a token save, a DCR save and a refresh-style update are
// exactly json.Marshal of the record, as before the feature. This guards
// unchanged behaviour, so its only red state before the implementation is
// the missing WithOAuthTokenKey option.
func TestOAuthPlaintextGolden(t *testing.T) {
	db := newSealTestDB(t, nil)
	zero := commitview.CredentialBinding{}

	steps := []struct {
		name  string
		write func() error
	}{
		{"token save", func() error {
			return db.SaveOAuthToken(&OAuthTokenRecord{ServerName: sealKey, DisplayName: sealSrv, AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer"})
		}},
		{"DCR save", func() error {
			_, err := db.SaveOAuthClientIfBound(sealKey, sealSrv, zero, OAuthClientSaveAlways, "cid", "cs", 4242, "http://127.0.0.1:4242/cb")
			return err
		}},
		{"refresh update", func() error {
			_, err := db.UpdateOAuthTokenIfBound(sealKey, sealSrv, zero, func(rec *OAuthTokenRecord) error {
				rec.AccessToken, rec.RefreshToken = "at-2", "rt-2"
				return nil
			})
			return err
		}},
	}
	for _, s := range steps {
		if err := s.write(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		raw := rawOAuthValue(t, db.db, sealKey)
		if shape, _ := tokenseal.Classify(raw); shape != tokenseal.ShapePlaintext {
			t.Fatalf("%s: stored shape %v, want plaintext", s.name, shape)
		}
		rec, err := db.GetOAuthToken(sealKey)
		if err != nil {
			t.Fatalf("%s: read back: %v", s.name, err)
		}
		want, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, want) {
			t.Fatalf("%s: stored bytes differ from json.Marshal of the record\n got %s\nwant %s", s.name, raw, want)
		}
	}
}
