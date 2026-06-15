package transport

import "strings"

// BrokeredAuth carries a per-user resolved upstream credential to inject into an
// outbound HTTP/SSE request (spec 074, FR-016/FR-017). The server edition's
// credential broker resolves the per-user token and hands it down as plain data
// so the edition-neutral transport layer can inject it without importing any
// server-only package.
//
// Injection REPLACES any inbound or statically-configured header of the same
// name: the gateway/IdP token is never forwarded to the upstream (FR-017).
type BrokeredAuth struct {
	// Header is the outbound header the credential is injected into
	// (default "Authorization").
	Header string
	// Format is the value template; the literal substring "{token}" is replaced
	// with Token (default "Bearer {token}").
	Format string
	// Token is the resolved per-user credential.
	Token string
}

// tokenPlaceholder is the substring in Format replaced with the resolved token.
const tokenPlaceholder = "{token}"

// HeaderValue renders the outbound header value from Format, substituting the
// resolved token for the "{token}" placeholder.
func (b *BrokeredAuth) HeaderValue() string {
	return strings.ReplaceAll(b.Format, tokenPlaceholder, b.Token)
}

// EffectiveHeaders returns the outbound header set for a request, injecting the
// brokered per-user credential when one is supplied.
//
// The returned map is always a fresh copy — callers must never mutate the
// server config's header map. When brokered is non-nil, any header in base
// whose name matches brokered.Header case-insensitively is dropped before the
// resolved credential is set, so the configured/inbound auth is REPLACED rather
// than merged or forwarded (FR-017). When brokered is nil, base is returned
// unchanged (as a copy).
func EffectiveHeaders(base map[string]string, brokered *BrokeredAuth) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		if brokered != nil && strings.EqualFold(k, brokered.Header) {
			// Drop the inbound/configured auth header — replaced below so the
			// gateway/IdP token is never forwarded to the upstream (FR-017).
			continue
		}
		out[k] = v
	}
	if brokered != nil {
		out[brokered.Header] = brokered.HeaderValue()
	}
	return out
}
