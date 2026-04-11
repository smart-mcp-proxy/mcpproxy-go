package telemetry

// ErrorCategory is a typed enum of error categories that telemetry will count.
// Only values defined here may be recorded; unknown categories are silently
// dropped by RecordError to prevent free-text error messages from leaking into
// telemetry.
type ErrorCategory string

const (
	ErrCatOAuthRefreshFailed      ErrorCategory = "oauth_refresh_failed"
	ErrCatOAuthTokenExpired       ErrorCategory = "oauth_token_expired"
	ErrCatUpstreamConnectTimeout  ErrorCategory = "upstream_connect_timeout"
	ErrCatUpstreamConnectRefused  ErrorCategory = "upstream_connect_refused"
	ErrCatUpstreamHandshakeFailed ErrorCategory = "upstream_handshake_failed"
	ErrCatToolQuarantineBlocked   ErrorCategory = "tool_quarantine_blocked"
	ErrCatDockerPullFailed        ErrorCategory = "docker_pull_failed"
	ErrCatDockerRunFailed         ErrorCategory = "docker_run_failed"
	ErrCatIndexRebuildFailed      ErrorCategory = "index_rebuild_failed"
	ErrCatConfigReloadFailed      ErrorCategory = "config_reload_failed"
	ErrCatSocketBindFailed        ErrorCategory = "socket_bind_failed"
)

var validErrorCategories = map[ErrorCategory]struct{}{
	ErrCatOAuthRefreshFailed:      {},
	ErrCatOAuthTokenExpired:       {},
	ErrCatUpstreamConnectTimeout:  {},
	ErrCatUpstreamConnectRefused:  {},
	ErrCatUpstreamHandshakeFailed: {},
	ErrCatToolQuarantineBlocked:   {},
	ErrCatDockerPullFailed:        {},
	ErrCatDockerRunFailed:         {},
	ErrCatIndexRebuildFailed:      {},
	ErrCatConfigReloadFailed:      {},
	ErrCatSocketBindFailed:        {},
}

// IsValidErrorCategory reports whether the given category is in the fixed enum.
func IsValidErrorCategory(c ErrorCategory) bool {
	_, ok := validErrorCategories[c]
	return ok
}
