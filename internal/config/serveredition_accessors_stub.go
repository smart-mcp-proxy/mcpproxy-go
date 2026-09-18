//go:build !server

package config

// Personal-build stubs of the server-edition accessors (Spec 107 T052): the
// block is an opaque carrier here (FR-040), so nothing in it is interpreted.

// ServerEditionEnabled is always false on the personal build.
func ServerEditionEnabled(_ *Config) bool { return false }

// EffectiveRequireMCPAuth is the configured value on the personal build.
func EffectiveRequireMCPAuth(cfg *Config) bool {
	return cfg != nil && cfg.RequireMCPAuth
}

// RequireMCPAuthOverridden is always false on the personal build.
func RequireMCPAuthOverridden(_ *Config) bool { return false }

// IdPProviderFamily is always "" on the personal build.
func IdPProviderFamily(_ *Config) string { return "" }

// PublicURL is always "" on the personal build.
func PublicURL(_ *Config) string { return "" }

// ServerEditionRestartProjection returns the opaque carrier itself on the
// personal build (Spec 107 FR-040): the block is canonical JSON that is never
// interpreted here, so DetectConfigChanges compares its bytes and reports ANY
// difference as `server_edition`, restart-pinned. A nil block marshals to
// `null`, so absent-vs-absent compares equal and absent-vs-present does not.
func ServerEditionRestartProjection(cfg *Config) any {
	if cfg == nil {
		return (*ServerEditionConfig)(nil)
	}
	return cfg.ServerEdition
}

// ServerEditionAdminEmails is always nil on the personal build: the carrier
// is not interpreted, so there is no live key to compare.
func ServerEditionAdminEmails(_ *Config) []string { return nil }

// ServerEditionAccessProjection is always nil on the personal build: the
// carrier is not interpreted, so there is no live access block to compare
// (any change inside the carrier is `server_edition`, restart-pinned).
func ServerEditionAccessProjection(_ *Config) any { return nil }

// ServerEditionRestartReason is the single reason the personal build can
// give — it cannot tell which key inside the opaque block moved.
func ServerEditionRestartReason(_, _ *Config) string {
	return "server_edition changed - the block is opaque on the personal edition and is read at startup"
}

// MergeServerEditionRestartGated pins the WHOLE opaque carrier to `live` on
// the personal build (cross-review round 1, chunk 3 P1): nothing inside it is
// interpreted, so every key is restart-pinned by the same rule
// ServerEditionRestartProjection uses — there is no hot admin_emails
// exception here (that is server-build-only, #1169).
func MergeServerEditionRestartGated(live, _ *ServerEditionConfig) *ServerEditionConfig {
	return live
}
