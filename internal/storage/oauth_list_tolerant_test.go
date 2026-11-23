package storage_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T013 (FR-014, FR-015): listing is per-row tolerant, the row
// listing classifies every row exactly once, and orphan cleanup never
// deletes a row it could not decode.
func TestOAuthListTolerant(t *testing.T) {
	a, other := undecTestKey(t, 1), undecTestKey(t, 50)
	m := newKeyedManager(t, a)

	const (
		plainGood  = "good1_0123456789abcdef"
		sealedGood = "good2_0123456789abcdef"
		orphan     = "orphan_0123456789abcdef"
		otherKey   = "good1_1111111111111111" // configured prefix, sealed under another key
		badJSON    = "ghost_2222222222222222" // unconfigured prefix, plaintext that is not JSON
	)
	rec := func(k, display string) *storage.OAuthTokenRecord {
		return &storage.OAuthTokenRecord{ServerName: k, DisplayName: display, AccessToken: "at-" + k}
	}
	plain := func(r *storage.OAuthTokenRecord) []byte {
		b, err := r.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rawPut(t, m, plainGood, plain(rec(plainGood, "good1")))
	rawPut(t, m, sealedGood, sealFor(t, a, sealedGood, rec(sealedGood, "good2")))
	rawPut(t, m, orphan, plain(rec(orphan, "orphan")))
	rawPut(t, m, otherKey, sealFor(t, other, otherKey, rec(otherKey, "good1")))
	rawPut(t, m, badJSON, []byte("{not json"))
	badOther, badJ := rawGet(t, m, otherKey), rawGet(t, m, badJSON)

	recs, err := m.ListOAuthTokens()
	if err != nil {
		t.Fatalf("ListOAuthTokens must skip undecryptable rows, got error: %v", err)
	}
	var keys []string
	for _, r := range recs {
		keys = append(keys, r.ServerName)
	}
	sort.Strings(keys)
	if want := []string{plainGood, sealedGood, orphan}; !reflect.DeepEqual(keys, sortedCopy(want)) {
		t.Fatalf("ListOAuthTokens keys %v, want %v", keys, sortedCopy(want))
	}

	rows, err := m.ListOAuthTokenRows()
	if err != nil {
		t.Fatal(err)
	}
	type cls struct {
		Class  storage.OAuthRowClass
		Reason tokenseal.Reason
		RowKID string
	}
	got := map[string]cls{}
	for _, r := range rows {
		if _, dup := got[r.BucketKey]; dup {
			t.Fatalf("row %s listed twice", r.BucketKey)
		}
		got[r.BucketKey] = cls{r.Class, r.Reason, r.RowKID}
	}
	want := map[string]cls{
		plainGood:  {storage.OAuthRowPlaintext, "", ""},
		sealedGood: {storage.OAuthRowSealed, "", a.ID()},
		orphan:     {storage.OAuthRowPlaintext, "", ""},
		otherKey:   {storage.OAuthRowUndecryptable, tokenseal.ReasonKeyMismatch, other.ID()},
		badJSON:    {storage.OAuthRowUndecryptable, tokenseal.ReasonMalformed, ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListOAuthTokenRows\n got %+v\nwant %+v", got, want)
	}

	rep, err := m.CleanupOrphanedOAuthTokensReport([]string{"good1", "good2"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted != 1 {
		t.Fatalf("deleted %d, want 1 (the decodable orphan)", rep.Deleted)
	}
	if rawGet(t, m, orphan) != nil {
		t.Fatal("decodable orphan not deleted")
	}
	if string(rawGet(t, m, otherKey)) != string(badOther) || string(rawGet(t, m, badJSON)) != string(badJ) {
		t.Fatal("orphan cleanup touched an undecryptable row")
	}
	if !reflect.DeepEqual(rep.UndecryptableKept, sortedCopy([]string{otherKey, badJSON})) {
		t.Fatalf("UndecryptableKept %v", rep.UndecryptableKept)
	}
	if !reflect.DeepEqual(rep.UndecryptableOrphans, []string{badJSON}) {
		t.Fatalf("UndecryptableOrphans %v, want [%s]", rep.UndecryptableOrphans, badJSON)
	}

	// The legacy entry point keeps its signature and the same behaviour.
	n, err := m.CleanupOrphanedOAuthTokens([]string{"good1", "good2"})
	if err != nil || n != 0 {
		t.Fatalf("second cleanup: n=%d err=%v", n, err)
	}
	if rawGet(t, m, otherKey) == nil || rawGet(t, m, badJSON) == nil {
		t.Fatal("CleanupOrphanedOAuthTokens deleted an undecryptable row")
	}
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}
