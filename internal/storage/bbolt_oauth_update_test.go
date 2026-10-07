package storage

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Spec 113 FR-006: a token write and a DCR-credentials write that interleave
// must not lose each other's fields.
func TestUpdateOAuthToken_ConcurrentWithClientCredentials(t *testing.T) {
	db := newTestDB(t)
	const key = "srv_0123456789abcdef"
	created := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := db.SaveOAuthToken(&OAuthTokenRecord{
		ServerName: key, DisplayName: "srv", AccessToken: "at-0", RefreshToken: "rt-0",
		Created: created, ClientID: "cid-0", ClientSecret: "sec-0", CallbackPort: 1234, RedirectURI: "http://127.0.0.1:1234/cb",
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				err := db.UpdateOAuthToken(key, func(rec *OAuthTokenRecord) error {
					rec.AccessToken = fmt.Sprintf("at-%d", i)
					rec.RefreshToken = fmt.Sprintf("rt-%d", i)
					return nil
				})
				if err != nil {
					t.Error(err)
				}
				return
			}
			if err := db.UpdateOAuthClientCredentials(key, fmt.Sprintf("cid-%d", i), "sec", 1234, "http://127.0.0.1:1234/cb"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	rec, err := db.GetOAuthToken(key)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ClientID == "" || rec.ClientSecret == "" || rec.CallbackPort != 1234 || rec.RedirectURI == "" {
		t.Fatalf("DCR fields lost: %+v", rec)
	}
	if rec.DisplayName != "srv" || !rec.Created.Equal(created) {
		t.Fatalf("display name / created lost: %q %v", rec.DisplayName, rec.Created)
	}
	if rec.AccessToken == "" || rec.RefreshToken == "" {
		t.Fatalf("token fields lost: %+v", rec)
	}
}

func TestUpdateOAuthToken_CreatesAndAborts(t *testing.T) {
	db := newTestDB(t)
	const key = "new_0123456789abcdef"

	if err := db.UpdateOAuthToken(key, func(rec *OAuthTokenRecord) error {
		if rec.ServerName != key {
			t.Errorf("new record key = %q", rec.ServerName)
		}
		rec.AccessToken = "at"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := db.GetOAuthToken(key)
	if err != nil || rec.AccessToken != "at" || rec.Created.IsZero() || rec.Updated.IsZero() {
		t.Fatalf("created record = %+v, err %v", rec, err)
	}

	sentinel := errors.New("abort")
	if err := db.UpdateOAuthToken(key, func(rec *OAuthTokenRecord) error {
		rec.AccessToken = "changed"
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if rec, _ := db.GetOAuthToken(key); rec.AccessToken != "at" {
		t.Fatalf("aborted mutate was persisted: %q", rec.AccessToken)
	}

	if err := db.UpdateOAuthToken(key, func(*OAuthTokenRecord) error { return ErrSkipOAuthTokenUpdate }); err != nil {
		t.Fatalf("skip sentinel must be swallowed, got %v", err)
	}
}

// Spec 113 FR-009: compare-and-clear never clears a registration that
// replaced the one the failed request used.
func TestClearOAuthClientCredentialsIf(t *testing.T) {
	db := newTestDB(t)
	const key = "dcr_0123456789abcdef"
	if err := db.SaveOAuthToken(&OAuthTokenRecord{ServerName: key, AccessToken: "at", RefreshToken: "rt-new", ClientID: "new-client", ClientSecret: "s", CallbackPort: 9, RedirectURI: "r"}); err != nil {
		t.Fatal(err)
	}

	cleared, err := db.ClearOAuthClientCredentialsIf(key, "old-client", "rt-new")
	if err != nil || cleared {
		t.Fatalf("mismatch must not clear: cleared=%v err=%v", cleared, err)
	}
	if rec, _ := db.GetOAuthToken(key); rec.ClientID != "new-client" {
		t.Fatalf("registration was cleared: %+v", rec)
	}

	// A login that reused the client id but saved a new grant is not the
	// grant the failed request used (FR-006a).
	cleared, err = db.ClearOAuthClientCredentialsIf(key, "new-client", "rt-old")
	if err != nil || cleared {
		t.Fatalf("grant mismatch must not clear: cleared=%v err=%v", cleared, err)
	}

	cleared, err = db.ClearOAuthClientCredentialsIf(key, "new-client", "rt-new")
	if err != nil || !cleared {
		t.Fatalf("match must clear: cleared=%v err=%v", cleared, err)
	}
	rec, _ := db.GetOAuthToken(key)
	if rec.ClientID != "" || rec.ClientSecret != "" || rec.CallbackPort != 0 || rec.RedirectURI != "" || rec.AccessToken != "at" {
		t.Fatalf("unexpected record after clear: %+v", rec)
	}

	if cleared, err := db.ClearOAuthClientCredentialsIf("absent", "x", ""); err != nil || cleared {
		t.Fatalf("absent record: cleared=%v err=%v", cleared, err)
	}
}

// A DCR client_id the authorization server rejects at /authorize is cleared
// with a compare-and-clear on the client id alone: the login has no grant to
// compare, and a concurrent login that saved a new registration (a new id)
// must never be wiped.
func TestClearOAuthClientCredentialsIfClientID(t *testing.T) {
	db := newTestDB(t)
	const key = "dcr_fedcba9876543210"
	if err := db.SaveOAuthToken(&OAuthTokenRecord{ServerName: key, AccessToken: "at", RefreshToken: "rt", ClientID: "new-client", ClientSecret: "s", CallbackPort: 9, RedirectURI: "r"}); err != nil {
		t.Fatal(err)
	}

	cleared, err := db.ClearOAuthClientCredentialsIfClientID(key, "dead-client")
	if err != nil || cleared {
		t.Fatalf("mismatch must not clear: cleared=%v err=%v", cleared, err)
	}
	if rec, _ := db.GetOAuthToken(key); rec.ClientID != "new-client" {
		t.Fatalf("registration was cleared: %+v", rec)
	}
	if cleared, err := db.ClearOAuthClientCredentialsIfClientID(key, ""); err != nil || cleared {
		t.Fatalf("empty expected id must not clear: cleared=%v err=%v", cleared, err)
	}

	cleared, err = db.ClearOAuthClientCredentialsIfClientID(key, "new-client")
	if err != nil || !cleared {
		t.Fatalf("match must clear: cleared=%v err=%v", cleared, err)
	}
	rec, _ := db.GetOAuthToken(key)
	if rec.ClientID != "" || rec.ClientSecret != "" || rec.CallbackPort != 0 || rec.RedirectURI != "" || rec.AccessToken != "at" || rec.RefreshToken != "rt" {
		t.Fatalf("unexpected record after clear: %+v", rec)
	}

	if cleared, err := db.ClearOAuthClientCredentialsIfClientID("absent", "x"); err != nil || cleared {
		t.Fatalf("absent record: cleared=%v err=%v", cleared, err)
	}
}
