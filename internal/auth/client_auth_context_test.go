package auth

import (
	"reflect"
	"testing"
	"time"
)

// TestAgentTokenAuthContext_PinnedStructs pins EVERY field of the context a
// client credential and a regular agent token produce, so moving
// (*AgentToken).AuthContext onto ClientAuthContext (Spec 132 R3) cannot change
// what the mcp_cli_ and mcp_agt_ paths have always produced.
func TestAgentTokenAuthContext_PinnedStructs(t *testing.T) {
	rl := &TokenRateLimit{PerMinute: 30, Burst: 5}
	client := &AgentToken{
		Name: ClientTokenName("cursor"), TokenPrefix: "mcp_cli_old1", MatchedPrefix: "mcp_cli_new2",
		MatchedGeneration: "gen-abc", AllowedServers: []string{"*"},
		Permissions: []string{PermRead, PermWrite, PermDestructive},
		Kind:        KindClient, ClientID: "cursor", ProfilePin: "dev", ProfileMode: ProfileModeLocked,
		RateLimit: rl, ExpiresAt: time.Now().Add(time.Hour),
	}
	want := &AuthContext{
		Type: AuthTypeAgent, AgentName: "client-cursor", TokenPrefix: "mcp_cli_new2", TokenGeneration: "gen-abc",
		AllowedServers: []string{"*"}, Permissions: []string{PermRead, PermWrite, PermDestructive},
		ProfilePin: "dev", TokenKind: KindClient, ClientID: "cursor", ProfileMode: ProfileModeLocked,
		RateLimit: &TokenRateLimit{PerMinute: 30, Burst: 5}, CredentialKind: CredentialKindAgentToken,
	}
	if got := client.AuthContext(); !reflect.DeepEqual(got, want) {
		t.Fatalf("client credential context drifted:\n got %+v\nwant %+v", got, want)
	}

	regular := &AgentToken{
		Name: "ci", TokenPrefix: "mcp_agt_abcd", AllowedServers: []string{"srv"}, Permissions: []string{PermRead},
		ProfilePin: "p", UserID: "u1", OwnerEmail: "a@b.c", OwnerProvider: "github", OwnerRole: "user",
	}
	wantReg := &AuthContext{
		Type: AuthTypeAgent, AgentName: "ci", TokenPrefix: "mcp_agt_abcd", AllowedServers: []string{"srv"},
		Permissions: []string{PermRead}, ProfilePin: "p", Email: "a@b.c", Provider: "github", Role: "user",
		UserID: "u1", TokenKind: KindAgent, CredentialKind: CredentialKindAgentToken,
	}
	if got := regular.AuthContext(); !reflect.DeepEqual(got, wantReg) {
		t.Fatalf("agent token context drifted:\n got %+v\nwant %+v", got, wantReg)
	}
	if got := client.AuthContext(); got.RateLimit == rl {
		t.Fatal("the context must not alias the record's rate limit")
	}
}

// TestClientAuthContext_BrowserLogin: a browser login is the same caller a
// client credential is, apart from kind, grant id, prefix and generation.
func TestClientAuthContext_BrowserLogin(t *testing.T) {
	ac := ClientAuthContext(ClientIdentity{
		ClientID: "cursor", ProfilePin: "dev", ProfileMode: ProfileModeSwitchable,
		CredentialKind: CredentialKindOAuth, GrantID: "G1", TokenPrefix: "mcp_oat_abcd", TokenGeneration: "G1",
	})
	want := &AuthContext{
		Type: AuthTypeAgent, AgentName: "client-cursor", TokenPrefix: "mcp_oat_abcd", TokenGeneration: "G1",
		AllowedServers: []string{"*"}, Permissions: []string{PermRead, PermWrite, PermDestructive},
		ProfilePin: "dev", TokenKind: KindClient, ClientID: "cursor", ProfileMode: ProfileModeSwitchable,
		CredentialKind: CredentialKindOAuth, GrantID: "G1",
	}
	if !reflect.DeepEqual(ac, want) {
		t.Fatalf("browser-login context:\n got %+v\nwant %+v", ac, want)
	}
	if !ac.IsClientCredential() || ac.IsAdmin() || ac.IsUser() {
		t.Fatal("a browser login is a client credential and nothing more")
	}
}

func TestCredentialKind_IsScopedClientCredential(t *testing.T) {
	for k, want := range map[CredentialKind]bool{
		CredentialKindAgentToken: true, CredentialKindOAuth: true, CredentialKindAPIKey: false,
		CredentialKindSocket: false, CredentialKindCookie: false, CredentialKindBearerJWT: false,
		CredentialKindAnonymous: false, "": false,
	} {
		if got := k.IsScopedClientCredential(); got != want {
			t.Errorf("%q.IsScopedClientCredential() = %v, want %v", k, got, want)
		}
	}
}
