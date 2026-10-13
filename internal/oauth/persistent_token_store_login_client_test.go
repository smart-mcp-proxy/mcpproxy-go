package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
)

// A login's code exchange must store its token and the client registration it
// was issued to in ONE transaction. Before, SaveToken kept whatever client was
// stored, so a login whose save was delayed past another login's completion
// left its refresh token next to the other login's client_id.
func TestPersistentTokenStore_LoginSaveWritesTokenAndClientTogether(t *testing.T) {
	db := newTestBolt(t)
	const name, url = "login-pair", "https://login-pair.example.com/mcp"
	key := GenerateServerKey(name, url)
	store := NewPersistentTokenStore(name, url, db)
	tok := func(rt string) *client.Token {
		return &client.Token{AccessToken: "at-" + rt, RefreshToken: rt, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	}
	assertPair := func(wantClient, wantSecret, wantRT string, wantPort int, wantRedirect string) {
		t.Helper()
		rec, err := db.GetOAuthToken(key)
		if err != nil {
			t.Fatal(err)
		}
		if rec.ClientID != wantClient || rec.ClientSecret != wantSecret || rec.RefreshToken != wantRT ||
			rec.CallbackPort != wantPort || rec.RedirectURI != wantRedirect {
			t.Fatalf("stored client=%q secret=%q rt=%q port=%d redirect=%q; want %q %q %q %d %q",
				rec.ClientID, rec.ClientSecret, rec.RefreshToken, rec.CallbackPort, rec.RedirectURI,
				wantClient, wantSecret, wantRT, wantPort, wantRedirect)
		}
	}

	// Login B completes first.
	ctxB := WithLoginClient(context.Background(), LoginClient{ClientID: "client-b", ClientSecret: "sec-b", CallbackPort: 2222, RedirectURI: "http://127.0.0.1:2222/cb"})
	if err := store.SaveToken(ctxB, tok("rt-b")); err != nil {
		t.Fatal(err)
	}
	assertPair("client-b", "sec-b", "rt-b", 2222, "http://127.0.0.1:2222/cb")

	// Login A's delayed save lands afterwards: its own client goes with it.
	ctxA := WithLoginClient(context.Background(), LoginClient{ClientID: "client-a", ClientSecret: "sec-a", CallbackPort: 1111, RedirectURI: "http://127.0.0.1:1111/cb"})
	if err := store.SaveToken(ctxA, tok("rt-a")); err != nil {
		t.Fatal(err)
	}
	assertPair("client-a", "sec-a", "rt-a", 1111, "http://127.0.0.1:1111/cb")

	// A login that knows no callback port or redirect keeps the stored ones.
	ctxC := WithLoginClient(context.Background(), LoginClient{ClientID: "client-c"})
	if err := store.SaveToken(ctxC, tok("rt-c")); err != nil {
		t.Fatal(err)
	}
	assertPair("client-c", "", "rt-c", 1111, "http://127.0.0.1:1111/cb")

	// A save outside a login (refresh) still preserves the stored client.
	if err := store.SaveToken(context.Background(), tok("rt-d")); err != nil {
		t.Fatal(err)
	}
	assertPair("client-c", "", "rt-d", 1111, "http://127.0.0.1:1111/cb")

	// An empty login client id carries no registration: client preserved.
	if err := store.SaveToken(WithLoginClient(context.Background(), LoginClient{}), tok("rt-e")); err != nil {
		t.Fatal(err)
	}
	assertPair("client-c", "", "rt-e", 1111, "http://127.0.0.1:1111/cb")
}
