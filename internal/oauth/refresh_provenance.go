package oauth

import (
	"context"

	"github.com/mark3labs/mcp-go/client"
)

// refreshProvenanceKey carries the provenance of one mcpproxy-driven refresh.
type refreshProvenanceKey struct{}

type refreshProvenance struct {
	refreshToken string
	issuer       string
}

// WithRefreshProvenance marks ctx as mcpproxy's own refresh of refreshToken
// through mcp-go's OAuthHandler.RefreshToken, against issuer: the issuer of
// the metadata the live handler is using right now.
//
// Why (Spec 135 FR-019, research R1): since v1.3 mcp-go refuses a refresh
// whose stored token has no Issuer provenance, and since v1.6 RefreshToken
// reads the token store to find it. mcpproxy's stores do not persist an
// issuer yet (issuer binding is Spec 135 PR-2), so every refresh would be
// refused. Under this mark the stores answer RefreshToken's read from ctx,
// which keeps the pre-bump behaviour (the refresh goes to the handler's
// current metadata, as with mcp-go v1.0) and keeps the read from re-entering
// the refresh flight that is already running. PR-2 replaces the ctx value
// with the row's stored binding.
func WithRefreshProvenance(ctx context.Context, refreshToken, issuer string) context.Context {
	return context.WithValue(ctx, refreshProvenanceKey{}, refreshProvenance{refreshToken: refreshToken, issuer: issuer})
}

// refreshProvenanceToken returns the token RefreshToken's store read gets
// under a WithRefreshProvenance mark, and false without one.
func refreshProvenanceToken(ctx context.Context) (*client.Token, bool) {
	if ctx == nil {
		return nil, false
	}
	p, ok := ctx.Value(refreshProvenanceKey{}).(refreshProvenance)
	if !ok || p.refreshToken == "" {
		return nil, false
	}
	return &client.Token{RefreshToken: p.refreshToken, Issuer: p.issuer}, true
}
