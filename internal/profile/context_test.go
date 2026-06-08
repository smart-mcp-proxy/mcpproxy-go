package profile

import (
	"context"
	"testing"
)

// TestProfileScope_Allows covers membership and the nil-receiver allow-all
// semantics: a nil *ProfileScope means the request did not enter via
// /mcp/p/<slug> and must not be filtered (FR-010, data-model §2).
func TestProfileScope_Allows(t *testing.T) {
	scope := NewProfileScope("research", []string{"fs", "web"})

	if !scope.Allows("fs") {
		t.Error("expected in-profile server 'fs' to be allowed")
	}
	if !scope.Allows("web") {
		t.Error("expected in-profile server 'web' to be allowed")
	}
	if scope.Allows("github") {
		t.Error("expected out-of-profile server 'github' to be denied")
	}
	if scope.Allows("") {
		t.Error("expected empty server name to be denied for a non-nil scope")
	}

	// nil receiver ⇒ no profile ⇒ allow-all (the /mcp path).
	var nilScope *ProfileScope
	if !nilScope.Allows("anything") {
		t.Error("expected nil *ProfileScope to allow all servers")
	}
}

// TestProfileScope_EmptyServers verifies the legal "deny everything" profile:
// an empty server list is a non-nil scope that allows nothing.
func TestProfileScope_EmptyServers(t *testing.T) {
	scope := NewProfileScope("locked", nil)
	if scope == nil {
		t.Fatal("NewProfileScope must return a non-nil scope even for empty servers")
	}
	if scope.Allows("fs") {
		t.Error("an empty-servers profile must deny every server")
	}
}

// TestProfileScope_ContextRoundTrip verifies WithProfileScope / FromContext, and
// that a bare context yields nil (no profile).
func TestProfileScope_ContextRoundTrip(t *testing.T) {
	if got := ProfileScopeFromContext(context.Background()); got != nil {
		t.Errorf("bare context must yield nil scope, got %+v", got)
	}

	scope := NewProfileScope("deploy", []string{"github"})
	ctx := WithProfileScope(context.Background(), scope)
	got := ProfileScopeFromContext(ctx)
	if got == nil {
		t.Fatal("expected scope from context, got nil")
	}
	if got.Name != "deploy" {
		t.Errorf("expected Name 'deploy', got %q", got.Name)
	}
	if !got.Allows("github") || got.Allows("fs") {
		t.Error("round-tripped scope lost its membership set")
	}
}
