package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// anonymousMarkedCtx returns a context carrying the back-compat
// Anonymous-marked admin AuthContext (auth.AnonymousContext()) — the
// require_mcp_auth:false unrecognised-token branch, distinct from anonCtx's
// bare no-AuthContext-at-all case.
func anonymousMarkedCtx() context.Context {
	return auth.WithAuthContext(context.Background(), auth.AnonymousContext())
}

// TestResolveProfileV3_AnonymousConfinement pins T033 (resolver half; under
// the FR-009a test-only override — the shipped 108-c binary rejects
// anonymous_profile at config-load time, T004a): anonymous callers resolve
// through anonymous_profile for both anonymous branches (no token at all,
// and an authenticated-but-Anonymous context, the require_mcp_auth:false
// unrecognised-token back-compat branch), and a dangling anonymous_profile
// denies all rather than falling through to "none" (which would grant
// unconfined reach).
func TestResolveProfileV3_AnonymousConfinement(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	cfg := proxy.currentConfig()
	cfg.AnonymousProfile = "work-readonly"
	idx := proxy.profileIndexFor(cfg)

	t.Run("no credential at all", func(t *testing.T) {
		res := proxy.ResolveProfileV3(anonCtx(), idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourceAnonymous), res.Source)
		assert.Equal(t, "work-readonly", res.Base)
	})

	t.Run("unrecognised token, require_mcp_auth off (Anonymous-marked admin context)", func(t *testing.T) {
		res := proxy.ResolveProfileV3(anonymousMarkedCtx(), idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourceAnonymous), res.Source)
	})

	t.Run("unset anonymous_profile keeps legacy byte-parity (source none)", func(t *testing.T) {
		cfg2 := proxy.currentConfig()
		cfg2.AnonymousProfile = ""
		idx2 := proxy.profileIndexFor(cfg2)
		res := proxy.ResolveProfileV3(anonCtx(), idx2)
		assert.Equal(t, string(profile.SourceNone), res.Source)
		assert.Nil(t, res.Scope)
	})
}

// TestResolveProfileV3_DanglingAnonymousProfile pins that a dangling
// anonymous_profile denies all rather than granting unconfined ("none")
// reach (FR-020).
func TestResolveProfileV3_DanglingAnonymousProfile(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	cfg := proxy.currentConfig()
	cfg.AnonymousProfile = "no-such-profile"
	idx := proxy.profileIndexFor(cfg)

	res := proxy.ResolveProfileV3(anonCtx(), idx)
	assert.Equal(t, "no-such-profile", res.Name)
	assert.Equal(t, string(profile.SourceAnonymous), res.Source)
	assert.True(t, res.Scope.DeniesAll())
	assert.Nil(t, res.Policy)
}
