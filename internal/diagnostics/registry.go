package diagnostics

// registry holds every known error code. Populated in init(). Every entry
// MUST be validated by catalog_test.go (message + >=1 fix_step + docs_url).
var registry = map[Code]CatalogEntry{}

func init() {
	seedSTDIO()
	seedOAUTH()
	seedHTTP()
	seedDOCKER()
	seedCONFIG()
	seedQUARANTINE()
	seedNETWORK()
	seedUNKNOWN()
}

func docsURL(c Code) string {
	return "docs/errors/" + string(c) + ".md"
}

func register(e CatalogEntry) {
	registry[e.Code] = e
}

// --- STDIO ---------------------------------------------------------------

func seedSTDIO() {
	register(CatalogEntry{
		Code:        STDIOSpawnENOENT,
		Severity:    SeverityError,
		UserMessage: "The configured command for this stdio server was not found on PATH.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Check which interpreter is on PATH", Command: "which npx && which uvx && which python3"},
			{Type: FixStepLink, Label: "Install the missing tool", URL: docsURL(STDIOSpawnENOENT)},
			{Type: FixStepButton, Label: "Show last server log lines", FixerKey: "stdio_show_last_logs"},
		},
		DocsURL: docsURL(STDIOSpawnENOENT),
	})
	register(CatalogEntry{
		Code:        STDIOSpawnEACCES,
		Severity:    SeverityError,
		UserMessage: "Permission denied executing the configured command.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Check file mode", Command: "ls -l <command-path>"},
			{Type: FixStepLink, Label: "Fix permissions", URL: docsURL(STDIOSpawnEACCES)},
		},
		DocsURL: docsURL(STDIOSpawnEACCES),
	})
	register(CatalogEntry{
		Code:        STDIOExitNonzero,
		Severity:    SeverityError,
		UserMessage: "The stdio server process exited with a non-zero status before handshake completed.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Show last server log lines", FixerKey: "stdio_show_last_logs"},
			{Type: FixStepLink, Label: "Troubleshooting guide", URL: docsURL(STDIOExitNonzero)},
		},
		DocsURL: docsURL(STDIOExitNonzero),
	})
	register(CatalogEntry{
		Code:        STDIOHandshakeTimeout,
		Severity:    SeverityError,
		UserMessage: "The stdio server did not complete the MCP handshake within the expected time.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Show last server log lines", FixerKey: "stdio_show_last_logs"},
			{Type: FixStepLink, Label: "Check MCP compatibility", URL: docsURL(STDIOHandshakeTimeout)},
		},
		DocsURL: docsURL(STDIOHandshakeTimeout),
	})
	register(CatalogEntry{
		Code:        STDIOHandshakeInvalid,
		Severity:    SeverityError,
		UserMessage: "The stdio server responded, but the MCP handshake frame was malformed.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "MCP protocol compatibility", URL: docsURL(STDIOHandshakeInvalid)},
			{Type: FixStepButton, Label: "Show last server log lines", FixerKey: "stdio_show_last_logs"},
		},
		DocsURL: docsURL(STDIOHandshakeInvalid),
	})
}

// --- OAUTH ---------------------------------------------------------------

func seedOAUTH() {
	register(CatalogEntry{
		Code:        OAuthRefreshExpired,
		Severity:    SeverityError,
		UserMessage: "The OAuth refresh token has expired; you need to log in again.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Log in again", FixerKey: "oauth_reauth", Destructive: true},
			{Type: FixStepLink, Label: "Why refresh tokens expire", URL: docsURL(OAuthRefreshExpired)},
		},
		DocsURL: docsURL(OAuthRefreshExpired),
	})
	register(CatalogEntry{
		Code:        OAuthRefresh403,
		Severity:    SeverityError,
		UserMessage: "The OAuth provider rejected the refresh token (403). The token was likely revoked or the client configuration changed.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Log in again", FixerKey: "oauth_reauth", Destructive: true},
			{Type: FixStepLink, Label: "Troubleshooting 403 refresh", URL: docsURL(OAuthRefresh403)},
		},
		DocsURL: docsURL(OAuthRefresh403),
	})
	register(CatalogEntry{
		Code:        OAuthDiscoveryFailed,
		Severity:    SeverityError,
		UserMessage: "Could not discover the OAuth metadata endpoint for this server.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "OAuth resource auto-detection", URL: docsURL(OAuthDiscoveryFailed)},
			{Type: FixStepCommand, Label: "Check connectivity to the issuer", Command: "curl -sS <issuer>/.well-known/oauth-authorization-server"},
		},
		DocsURL: docsURL(OAuthDiscoveryFailed),
	})
	register(CatalogEntry{
		Code:        OAuthCallbackTimeout,
		Severity:    SeverityWarn,
		UserMessage: "The OAuth browser callback did not complete in time.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Retry log in", FixerKey: "oauth_reauth", Destructive: true},
			{Type: FixStepLink, Label: "Callback troubleshooting", URL: docsURL(OAuthCallbackTimeout)},
		},
		DocsURL: docsURL(OAuthCallbackTimeout),
	})
	register(CatalogEntry{
		Code:        OAuthCallbackMismatch,
		Severity:    SeverityError,
		UserMessage: "The OAuth callback redirect URI did not match the expected value.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Redirect URI persistence guide", URL: docsURL(OAuthCallbackMismatch)},
		},
		DocsURL: docsURL(OAuthCallbackMismatch),
	})
}

// --- HTTP ----------------------------------------------------------------

func seedHTTP() {
	register(CatalogEntry{
		Code:        HTTPDNSFailed,
		Severity:    SeverityError,
		UserMessage: "DNS lookup failed for the configured server URL.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Test DNS resolution", Command: "dig <hostname>"},
			{Type: FixStepLink, Label: "DNS troubleshooting", URL: docsURL(HTTPDNSFailed)},
		},
		DocsURL: docsURL(HTTPDNSFailed),
	})
	register(CatalogEntry{
		Code:        HTTPTLSFailed,
		Severity:    SeverityError,
		UserMessage: "TLS verification failed when connecting to the server.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Inspect the server certificate", Command: "openssl s_client -connect <host>:443 -showcerts </dev/null"},
			{Type: FixStepLink, Label: "TLS debugging", URL: docsURL(HTTPTLSFailed)},
		},
		DocsURL: docsURL(HTTPTLSFailed),
	})
	register(CatalogEntry{
		Code:        HTTPUnauth,
		Severity:    SeverityError,
		UserMessage: "The server returned 401 Unauthorized.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Log in again (OAuth)", FixerKey: "oauth_reauth", Destructive: true},
			{Type: FixStepLink, Label: "Authentication guide", URL: docsURL(HTTPUnauth)},
		},
		DocsURL: docsURL(HTTPUnauth),
	})
	register(CatalogEntry{
		Code:        HTTPForbidden,
		Severity:    SeverityError,
		UserMessage: "The server returned 403 Forbidden.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Check token scopes", URL: docsURL(HTTPForbidden)},
		},
		DocsURL: docsURL(HTTPForbidden),
	})
	register(CatalogEntry{
		Code:        HTTPNotFound,
		Severity:    SeverityError,
		UserMessage: "The server returned 404 at the configured URL.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Verify the MCP endpoint path", URL: docsURL(HTTPNotFound)},
		},
		DocsURL: docsURL(HTTPNotFound),
	})
	register(CatalogEntry{
		Code:        HTTPServerErr,
		Severity:    SeverityWarn,
		UserMessage: "The server returned a 5xx response.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Upstream status page", URL: docsURL(HTTPServerErr)},
		},
		DocsURL: docsURL(HTTPServerErr),
	})
	register(CatalogEntry{
		Code:        HTTPConnRefuse,
		Severity:    SeverityError,
		UserMessage: "Connection refused by the server at the configured URL.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Test reachability", Command: "curl -v <server-url>"},
			{Type: FixStepLink, Label: "Connectivity checklist", URL: docsURL(HTTPConnRefuse)},
		},
		DocsURL: docsURL(HTTPConnRefuse),
	})
}

// --- DOCKER --------------------------------------------------------------

func seedDOCKER() {
	register(CatalogEntry{
		Code:        DockerDaemonDown,
		Severity:    SeverityError,
		UserMessage: "The Docker daemon is not reachable. stdio isolation cannot run.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Check Docker status", Command: "docker info"},
			{Type: FixStepLink, Label: "Install/start Docker", URL: docsURL(DockerDaemonDown)},
		},
		DocsURL: docsURL(DockerDaemonDown),
	})
	register(CatalogEntry{
		Code:        DockerImagePullFailed,
		Severity:    SeverityError,
		UserMessage: "Docker failed to pull the isolation image.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Pull manually", Command: "docker pull <image>"},
			{Type: FixStepLink, Label: "Offline installation", URL: docsURL(DockerImagePullFailed)},
		},
		DocsURL: docsURL(DockerImagePullFailed),
	})
	register(CatalogEntry{
		Code:        DockerNoPermission,
		Severity:    SeverityError,
		UserMessage: "The current user lacks permission to talk to the Docker socket.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Add user to docker group (Linux)", Command: "sudo usermod -aG docker $USER && newgrp docker"},
			{Type: FixStepLink, Label: "Permission fixes per platform", URL: docsURL(DockerNoPermission)},
		},
		DocsURL: docsURL(DockerNoPermission),
	})
	register(CatalogEntry{
		Code:        DockerSnapAppArmor,
		Severity:    SeverityWarn,
		UserMessage: "snap-installed Docker with AppArmor blocks mcpproxy's scanner. Either switch Docker flavour or disable the scanner for this server.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Switch to non-snap Docker (Desktop/Colima/rootless)", URL: docsURL(DockerSnapAppArmor)},
			{Type: FixStepButton, Label: "Disable scanner for this server (dry-run)", FixerKey: "server_disable_scanner", Destructive: true},
		},
		DocsURL: docsURL(DockerSnapAppArmor),
	})
}

// --- CONFIG --------------------------------------------------------------

func seedCONFIG() {
	register(CatalogEntry{
		Code:        ConfigDeprecatedField,
		Severity:    SeverityWarn,
		UserMessage: "The configuration uses a deprecated field that will be removed in a future release.",
		FixSteps: []FixStep{
			{Type: FixStepButton, Label: "Preview migration (dry-run)", FixerKey: "config_migrate_deprecated", Destructive: true},
			{Type: FixStepLink, Label: "Migration notes", URL: docsURL(ConfigDeprecatedField)},
		},
		DocsURL: docsURL(ConfigDeprecatedField),
	})
	register(CatalogEntry{
		Code:        ConfigParseError,
		Severity:    SeverityError,
		UserMessage: "mcpproxy could not parse the configuration file.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Validate JSON", Command: "jq . ~/.mcpproxy/mcp_config.json"},
			{Type: FixStepLink, Label: "Config reference", URL: docsURL(ConfigParseError)},
		},
		DocsURL: docsURL(ConfigParseError),
	})
	register(CatalogEntry{
		Code:        ConfigMissingSecret,
		Severity:    SeverityError,
		UserMessage: "The configuration references a secret that is not defined.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "List secrets", Command: "mcpproxy secret list"},
			{Type: FixStepLink, Label: "Secret references", URL: docsURL(ConfigMissingSecret)},
		},
		DocsURL: docsURL(ConfigMissingSecret),
	})
}

// --- QUARANTINE ----------------------------------------------------------

func seedQUARANTINE() {
	register(CatalogEntry{
		Code:        QuarantinePendingApproval,
		Severity:    SeverityWarn,
		UserMessage: "This server has tools pending security approval; they will not run until approved.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Open quarantine panel", URL: docsURL(QuarantinePendingApproval)},
		},
		DocsURL: docsURL(QuarantinePendingApproval),
	})
	register(CatalogEntry{
		Code:        QuarantineToolChanged,
		Severity:    SeverityWarn,
		UserMessage: "One or more tools changed since last approval; re-approval is required (rug-pull protection).",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Review the diff", URL: docsURL(QuarantineToolChanged)},
		},
		DocsURL: docsURL(QuarantineToolChanged),
	})
}

// --- NETWORK -------------------------------------------------------------

func seedNETWORK() {
	register(CatalogEntry{
		Code:        NetworkProxyMisconfig,
		Severity:    SeverityWarn,
		UserMessage: "System HTTP proxy variables appear misconfigured.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Print proxy env", Command: "env | grep -i proxy"},
			{Type: FixStepLink, Label: "Proxy configuration", URL: docsURL(NetworkProxyMisconfig)},
		},
		DocsURL: docsURL(NetworkProxyMisconfig),
	})
	register(CatalogEntry{
		Code:        NetworkOffline,
		Severity:    SeverityError,
		UserMessage: "Network appears to be offline.",
		FixSteps: []FixStep{
			{Type: FixStepCommand, Label: "Ping", Command: "ping -c 2 1.1.1.1"},
			{Type: FixStepLink, Label: "Offline troubleshooting", URL: docsURL(NetworkOffline)},
		},
		DocsURL: docsURL(NetworkOffline),
	})
}

// --- UNKNOWN -------------------------------------------------------------

func seedUNKNOWN() {
	register(CatalogEntry{
		Code:        UnknownUnclassified,
		Severity:    SeverityError,
		UserMessage: "mcpproxy could not classify this failure. Please file a bug report so we can add a specific code.",
		FixSteps: []FixStep{
			{Type: FixStepLink, Label: "Report a bug", URL: "https://github.com/smartmcpproxy/mcpproxy-go/issues/new?template=bug_report.md"},
		},
		DocsURL: docsURL(UnknownUnclassified),
	})
}
