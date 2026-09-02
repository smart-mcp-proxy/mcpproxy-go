package auth

import (
	"reflect"
	"testing"
)

// TestAgentTokenAuthContext_CarriesUserID: an agent-token request must carry
// the owning tenant's identity, or its activity cannot be attributed or scoped
// at all (issue #1168 gap b).
//
// BITES: without the fix ac.UserID is "" while the token names an owner.
func TestAgentTokenAuthContext_CarriesUserID(t *testing.T) {
	const owner = "01HTEST000000000000000USER"

	tok := &AgentToken{
		Name:        "ci",
		TokenPrefix: "mcp_agt_abcd",
		Permissions: []string{PermRead},
		UserID:      owner,
	}

	ac := tok.AuthContext()
	if ac == nil {
		t.Fatal("AuthContext() returned nil for a valid token")
	}
	if ac.UserID != owner {
		t.Errorf("UserID = %q, want %q — the agent tier carries no tenant identity", ac.UserID, owner)
	}
	if ac.GetUserID() != owner {
		t.Errorf("GetUserID() = %q, want %q", ac.GetUserID(), owner)
	}

	// Carrying the owner must NOT promote the tier: everything gated on the
	// user tier stays closed, and admin stays closed.
	if ac.Type != AuthTypeAgent {
		t.Errorf("Type = %q, want %q", ac.Type, AuthTypeAgent)
	}
	if ac.IsUser() {
		t.Error("an agent token must not satisfy IsUser(); per-user surfaces gate on it")
	}
	if ac.IsAdmin() {
		t.Error("an agent token must never be admin")
	}
	if ac.CanRevealSecrets() {
		t.Error("an agent token must never be allowed to reveal secrets")
	}
}

// TestAgentTokenAuthContext_FieldParity_Reflective makes the constructor's own
// doc-comment promise ("so no auth path can silently drop a field") into a
// mechanical check: every AgentToken field with a same-named counterpart on
// AuthContext must be carried through. The next field added to AgentToken
// fails here instead of being dropped in silence — which is exactly how UserID
// went missing.
//
// Fields with no AuthContext counterpart (TokenHash, ExpiresAt, CreatedAt,
// LastUsedAt, Revoked) are storage bookkeeping and are skipped. Name maps to
// AgentName and is checked explicitly.
func TestAgentTokenAuthContext_FieldParity_Reflective(t *testing.T) {
	tok := &AgentToken{
		Name:           "ci",
		TokenPrefix:    "mcp_agt_abcd",
		AllowedServers: []string{"github"},
		Permissions:    []string{PermRead},
		UserID:         "01HTEST000000000000000USER",
		ProfilePin:     "work",
	}

	ac := tok.AuthContext()
	if ac == nil {
		t.Fatal("AuthContext() returned nil for a valid token")
	}

	if ac.AgentName != tok.Name {
		t.Errorf("AgentName = %q, want the token's Name %q", ac.AgentName, tok.Name)
	}

	tokVal := reflect.ValueOf(*tok)
	tokType := tokVal.Type()
	acVal := reflect.ValueOf(*ac)
	acType := acVal.Type()

	checked := 0
	for i := 0; i < tokType.NumField(); i++ {
		f := tokType.Field(i)
		acField, ok := acType.FieldByName(f.Name)
		if !ok || acField.Type != f.Type {
			continue // no same-named, same-typed counterpart
		}
		src := tokVal.Field(i)
		if src.IsZero() {
			continue // the fixture does not exercise this field
		}
		checked++
		if !reflect.DeepEqual(src.Interface(), acVal.FieldByName(f.Name).Interface()) {
			t.Errorf("AuthContext() dropped %s: token has %v, context has %v",
				f.Name, src.Interface(), acVal.FieldByName(f.Name).Interface())
		}
	}

	// Guard against the check silently covering nothing (a renamed field, a
	// changed type). TokenPrefix, AllowedServers, Permissions, UserID and
	// ProfilePin all have counterparts today.
	if checked < 5 {
		t.Fatalf("the reflective parity check only covered %d fields; it is no longer proving anything", checked)
	}
}
