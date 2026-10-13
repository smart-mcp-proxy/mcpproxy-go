package oauth

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func newTestBolt(t *testing.T) *storage.BoltDB {
	t.Helper()
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Spec 113 FR-006: SaveToken used to read the record and write it back in two
// transactions, so a DCR registration written in between was lost.
func TestPersistentTokenStore_SaveTokenDoesNotLoseDCRCredentials(t *testing.T) {
	db := newTestBolt(t)
	const name, url = "dcr-race", "https://dcr-race.example.com/mcp"
	key := GenerateServerKey(name, url)
	store := NewPersistentTokenStore(name, url, db)

	for round := 0; round < 3; round++ {
		if err := db.DeleteOAuthToken(key); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if i%2 == 0 {
					errs[i] = store.SaveToken(context.Background(), &client.Token{
						AccessToken: fmt.Sprintf("at-%d", i), RefreshToken: "rt", TokenType: "Bearer",
						ExpiresAt: time.Now().Add(time.Hour),
					})
					return
				}
				errs[i] = db.UpdateOAuthClientCredentials(key, "cid", "sec", 4242, "http://127.0.0.1:4242/cb")
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d writer %d: %v", round, i, err)
			}
		}

		rec, err := db.GetOAuthToken(key)
		if err != nil {
			t.Fatal(err)
		}
		if rec.AccessToken == "" || rec.RefreshToken != "rt" {
			t.Fatalf("round %d: token fields lost by a concurrent DCR write: %+v", round, rec.AccessToken != "")
		}
		if rec.ClientID != "cid" || rec.CallbackPort != 4242 || rec.RedirectURI == "" {
			t.Fatalf("round %d: DCR credentials lost by a concurrent SaveToken: client_id=%q port=%d", round, rec.ClientID, rec.CallbackPort)
		}
	}
}
