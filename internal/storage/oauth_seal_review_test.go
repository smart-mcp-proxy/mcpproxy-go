package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 review: with encryption OFF, SaveOAuthToken behaves as on main
// (FR-011 / SC-003): a corrupt plaintext row is overwritten, not refused as
// "cannot be decrypted". A row shaped like an envelope is still refused, so
// a save without the key never destroys an encrypted token.
func TestSaveOAuthToken_NoKeyOverwritesCorruptPlaintext(t *testing.T) {
	db := newSealTestDB(t, nil)
	for _, v := range []string{"{not json", "garbage", `{"access_token":42}`} {
		putRawOAuthValue(t, db.db, "srv_x", []byte(v))
		if err := db.SaveOAuthToken(&OAuthTokenRecord{ServerName: "srv_x", AccessToken: "a"}); err != nil {
			t.Fatalf("row %q: save must overwrite a corrupt plaintext row with no key configured: %v", v, err)
		}
		got, err := db.GetOAuthToken("srv_x")
		if err != nil || got.AccessToken != "a" {
			t.Fatalf("row %q: got %+v, %v", v, got, err)
		}
	}

	k := sealTestKey(t, 7)
	for _, v := range [][]byte{
		sealedValue(t, k, "srv_x", &OAuthTokenRecord{ServerName: "srv_x", AccessToken: "secret"}),
		[]byte("enc:v2:0123456789abcdef:AAAA"),
		[]byte("enc:v1:0123456789ABCDEF:AAAA"), // damaged envelope header
	} {
		putRawOAuthValue(t, db.db, "srv_x", v)
		err := db.SaveOAuthToken(&OAuthTokenRecord{ServerName: "srv_x", AccessToken: "a"})
		var ue *ErrOAuthTokenUndecryptable
		if !errors.As(err, &ue) {
			t.Fatalf("row %q: want *ErrOAuthTokenUndecryptable, got %v", v, err)
		}
		if string(rawOAuthValue(t, db.db, "srv_x")) != string(v) {
			t.Fatalf("row %q: envelope must be left byte for byte", v)
		}
	}
}

// With a key configured, a save still refuses every undecodable row
// (FR-013), corrupt plaintext included.
func TestSaveOAuthToken_KeyRefusesCorruptPlaintext(t *testing.T) {
	db := newSealTestDB(t, sealTestKey(t, 7))
	putRawOAuthValue(t, db.db, "srv_x", []byte("{not json"))
	err := db.SaveOAuthToken(&OAuthTokenRecord{ServerName: "srv_x", AccessToken: "a"})
	var ue *ErrOAuthTokenUndecryptable
	if !errors.As(err, &ue) || ue.Reason != tokenseal.ReasonMalformed {
		t.Fatalf("want malformed refusal, got %v", err)
	}
}

// Spec 123 review: the boot sweep seals the stored plaintext, keeping JSON
// fields this binary does not know (a row written by a newer binary), while
// still taking a new revision.
func TestOAuthSweep_KeepsUnknownFields(t *testing.T) {
	k := sealTestKey(t, 7)
	db := newSealTestDB(t, k)
	putRawOAuthValue(t, db.db, "srv_x", []byte(`{"server_name":"srv_x","access_token":"a","token_type":"Bearer","revision":3,"future_field":{"n":1},"another":"v"}`))
	n, err := db.SealPlaintextOAuthTokens(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	stored := rawOAuthValue(t, db.db, "srv_x")
	if shape, _ := tokenseal.Classify(stored); shape != tokenseal.ShapeSealedV1 {
		t.Fatalf("row not sealed: %q", stored)
	}
	pt, err := k.Open(OAuthTokenBucket, "srv_x", stored)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(pt, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["future_field"]) != `{"n":1}` || string(m["another"]) != `"v"` {
		t.Fatalf("unknown fields dropped: %s", pt)
	}
	got, err := db.GetOAuthToken("srv_x")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a" || got.Revision == 0 || got.Revision == 3 {
		t.Fatalf("got %+v; want access token kept and a new revision", got)
	}
	if string(m["revision"]) == "3" {
		t.Fatalf("stored revision not renewed: %s", pt)
	}
}
