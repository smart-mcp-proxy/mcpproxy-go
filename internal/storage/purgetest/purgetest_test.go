package purgetest

import (
	"encoding/json"
	"testing"

	"go.etcd.io/bbolt"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2 review round 8: the oracle must report a history bucket a
// removal left behind even when the identity that keyed it was deleted —
// otherwise AssertPurged would pass a purge that dropped identities and kept
// their hash-keyed call history.
func TestCounts_R8_OrphanedHistoryBucketStillReported(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zaptest.NewLogger(t).Sugar())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	Seed(t, mgr, "acme")
	AssertSeeded(t, mgr, "acme")

	// Delete only acme's identity records, in a test transaction.
	err = mgr.GetDB().Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("server_identities"))
		if b == nil {
			t.Fatal("fixture: no server_identities bucket")
		}
		var keys [][]byte
		_ = b.ForEach(func(k, v []byte) error {
			var id storage.ServerIdentity
			if json.Unmarshal(v, &id) == nil && id.ServerName == "acme" {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		})
		if len(keys) == 0 {
			t.Fatal("fixture: no identity of acme")
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	c := Counts(t, mgr, "acme")
	if c[Identities] != 0 {
		t.Fatalf("fixture: identities left: %d", c[Identities])
	}
	if c[ToolCalls] == 0 {
		t.Fatalf("the orphaned history bucket of acme is not reported: %v", c)
	}
	// A different server's history is not attributed to acme.
	if other := Counts(t, mgr, "other"); other[ToolCalls] != 0 {
		t.Fatalf("history attributed to an unrelated name: %v", other)
	}
}
