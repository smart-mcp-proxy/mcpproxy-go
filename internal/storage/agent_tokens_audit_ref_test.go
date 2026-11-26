package storage

// Spec 133 §Target: an agent token carries a stable opaque audit ref,
// assigned at create, kept across rotation, given to a legacy token the first
// time a transaction names it; rotation and delete report what the
// transaction itself changed.

import (
	"encoding/json"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func TestAgentTokenAuditRef_AssignedAtCreateKeptAcrossRotation(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	tok, raw := makeTestToken("ci-bot")
	if err := m.CreateAgentToken(tok, raw, testHMACKey); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetAgentTokenByName("ci-bot")
	if !auth.ValidAuditRef(got.AuditRef) {
		t.Fatalf("create must assign an audit ref, got %q", got.AuditRef)
	}
	newRaw, _ := auth.GenerateToken()
	before, after, err := m.RegenerateAgentTokenReport("", "ci-bot", newRaw, testHMACKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before.TokenPrefix != auth.TokenPrefix(raw) || after.TokenPrefix != auth.TokenPrefix(newRaw) {
		t.Fatalf("rotation report prefixes: before %q after %q", before.TokenPrefix, after.TokenPrefix)
	}
	if after.AuditRef != got.AuditRef || before.AuditRef != got.AuditRef {
		t.Fatalf("rotation must keep the ref: %q -> %q", got.AuditRef, after.AuditRef)
	}
}

func TestAgentTokenAuditRef_LegacyTokenGetsOneOnFirstRevoke(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	tok, raw := makeTestToken("legacy")
	if err := m.CreateAgentToken(tok, raw, testHMACKey); err != nil {
		t.Fatal(err)
	}
	// Strip the ref, as a pre-133 record would be.
	hash := auth.HashToken(raw, testHMACKey)
	if err := m.db.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(AgentTokensBucket))
		var rec auth.AgentToken
		_ = json.Unmarshal(b.Get([]byte(hash)), &rec)
		rec.AuditRef = ""
		data, _ := json.Marshal(rec)
		return b.Put([]byte(hash), data)
	}); err != nil {
		t.Fatal(err)
	}
	_, after, err := m.RevokeAgentTokenReport("", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.ValidAuditRef(after.AuditRef) {
		t.Fatalf("a legacy token must get a ref the first time a line names it")
	}
	stored, _ := m.GetAgentTokenByName("legacy")
	if stored.AuditRef != after.AuditRef {
		t.Fatalf("the assigned ref must be persisted: %q vs %q", stored.AuditRef, after.AuditRef)
	}
	deleted, err := m.DeleteAgentTokenReport("", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.AuditRef != after.AuditRef {
		t.Fatalf("delete must report the same ref: %q vs %q", deleted.AuditRef, after.AuditRef)
	}
	if again, _ := m.GetAgentTokenByName("legacy"); again != nil {
		t.Fatal("delete report must still delete")
	}
	if _, err := m.DeleteAgentTokenReport("", "legacy"); err != ErrAgentTokenNotFound {
		t.Fatalf("deleting an absent token: %v", err)
	}
}
