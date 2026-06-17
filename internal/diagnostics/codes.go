package diagnostics

// Stable error codes. Once shipped, these constants MUST NOT be renamed.
// See FR-004 in specs/044-diagnostics-taxonomy/spec.md.
//
// Code format: MCPX_<DOMAIN>_<SPECIFIC> where DOMAIN is one of
// OAUTH, STDIO, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK, UNKNOWN.

// STDIO domain — stdio-transport MCP server failures.
const (
	STDIOSpawnENOENT Code = "MCPX_STDIO_SPAWN_ENOENT"
	STDIOSpawnEACCES Code = "MCPX_STDIO_SPAWN_EACCES"
	// STDIOSpawnExecFormat: the stdio binary exists but is the wrong CPU
	// architecture / not an executable format (ENOEXEC — "exec format error").
	// Distinct from a Docker/OCI exec-format failure, which is
	// MCPX_DOCKER_OCI_RUNTIME under the docker-isolation hint.
	STDIOSpawnExecFormat      Code = "MCPX_STDIO_SPAWN_EXEC_FORMAT"
	STDIOExitNonzero          Code = "MCPX_STDIO_EXIT_NONZERO"
	STDIOExitBeforeInitialize Code = "MCPX_STDIO_EXIT_BEFORE_INITIALIZE"
	STDIOHandshakeTimeout     Code = "MCPX_STDIO_HANDSHAKE_TIMEOUT"
	STDIOHandshakeInvalid     Code = "MCPX_STDIO_HANDSHAKE_INVALID"
)

// OAUTH domain — OAuth 2.1 / PKCE flow failures.
//
// OAuthLoginRequired and OAuthReauthRequired are actionable user-states, NOT
// faults: they describe a server waiting for the user to sign in, so they must
// never drive a "file a bug" CTA. Login-required is the first-time sign-in
// (amber/degraded); re-auth-required is a previously-working token that broke
// (red/unhealthy). See MCP-1820.
const (
	OAuthLoginRequired    Code = "MCPX_OAUTH_LOGIN_REQUIRED"
	OAuthReauthRequired   Code = "MCPX_OAUTH_REAUTH_REQUIRED"
	OAuthRefreshExpired   Code = "MCPX_OAUTH_REFRESH_EXPIRED"
	OAuthRefresh403       Code = "MCPX_OAUTH_REFRESH_403"
	OAuthDiscoveryFailed  Code = "MCPX_OAUTH_DISCOVERY_FAILED"
	OAuthCallbackTimeout  Code = "MCPX_OAUTH_CALLBACK_TIMEOUT"
	OAuthCallbackMismatch Code = "MCPX_OAUTH_CALLBACK_MISMATCH"
)

// HTTP domain — HTTP/SSE transport failures.
const (
	HTTPDNSFailed  Code = "MCPX_HTTP_DNS_FAILED"
	HTTPTLSFailed  Code = "MCPX_HTTP_TLS_FAILED"
	HTTPUnauth     Code = "MCPX_HTTP_401"
	HTTPForbidden  Code = "MCPX_HTTP_403"
	HTTPNotFound   Code = "MCPX_HTTP_404"
	HTTPServerErr  Code = "MCPX_HTTP_5XX"
	HTTPConnRefuse Code = "MCPX_HTTP_CONN_REFUSED"
	HTTPTimeout    Code = "MCPX_HTTP_TIMEOUT"
)

// DOCKER domain — Docker isolation subsystem failures.
const (
	DockerDaemonDown      Code = "MCPX_DOCKER_DAEMON_DOWN"
	DockerImagePullFailed Code = "MCPX_DOCKER_IMAGE_PULL_FAILED"
	DockerNoPermission    Code = "MCPX_DOCKER_NO_PERMISSION"
	DockerSnapAppArmor    Code = "MCPX_DOCKER_SNAP_APPARMOR"
	// DockerCLINotFound: isolation was requested but the `docker` binary could
	// not be resolved on the spawn PATH (issue #696 — Docker Desktop installed
	// without the admin-gated CLI shim, or a LaunchAgent's minimal PATH).
	DockerCLINotFound Code = "MCPX_DOCKER_CLI_NOT_FOUND"
	// DockerExecNotFound: the container started but its entrypoint interpreter
	// is missing from the image (e.g. `uvx` absent in `python:3.11`). Distinct
	// from a HOST stdio ENOENT, which is MCPX_STDIO_SPAWN_ENOENT.
	DockerExecNotFound Code = "MCPX_DOCKER_EXEC_NOT_FOUND"
	// DockerOCIRuntime: the OCI runtime (runc) failed to start the container —
	// e.g. an `exec format error` (image/host architecture mismatch).
	DockerOCIRuntime Code = "MCPX_DOCKER_OCI_RUNTIME"
)

// CONFIG domain — configuration parsing and validation failures.
const (
	ConfigDeprecatedField Code = "MCPX_CONFIG_DEPRECATED_FIELD"
	ConfigParseError      Code = "MCPX_CONFIG_PARSE_ERROR"
	ConfigMissingSecret   Code = "MCPX_CONFIG_MISSING_SECRET"
)

// QUARANTINE domain — security quarantine failures.
const (
	QuarantinePendingApproval Code = "MCPX_QUARANTINE_PENDING_APPROVAL"
	QuarantineToolChanged     Code = "MCPX_QUARANTINE_TOOL_CHANGED"
)

// NETWORK domain — network environment failures.
const (
	NetworkProxyMisconfig Code = "MCPX_NETWORK_PROXY_MISCONFIG"
	NetworkOffline        Code = "MCPX_NETWORK_OFFLINE"
)

// UNKNOWN — fallback when no specific classification applies.
const (
	UnknownUnclassified Code = "MCPX_UNKNOWN_UNCLASSIFIED"
)
