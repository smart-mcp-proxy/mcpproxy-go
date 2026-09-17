//go:build server

package serveredition

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsapi "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/api"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func init() {
	Register(Feature{
		Name:  "multiuser-oauth",
		Setup: setupMultiUserOAuth,
	})
}

// credentialMasterKey resolves the credential-store master key from the
// already-defaulted ServerEditionConfig. It deliberately does NOT call
// broker.ResolveMasterKey: cfg.CredentialEncryptionKey already went through
// config.ServerEditionConfig.ApplyDefaults(), which gives the explicit
// config value precedence and falls back to MCPPROXY_CRED_KEY only when the
// config value is empty — matching docs/configuration/config-file.md's
// "An explicit value wins over the environment" for this key.
// broker.ResolveMasterKey has the OPPOSITE precedence (env wins) by its own
// separate, deliberate design for its own callers; calling it again here on
// top of the already-resolved value would silently discard an explicit
// server_edition.credential_encryption_key whenever MCPPROXY_CRED_KEY is
// also set, contradicting the documented precedence for this key
// (cross-review round 5, chunk 3 P2).
func credentialMasterKey(cfg *config.ServerEditionConfig) string {
	return cfg.CredentialEncryptionKey
}

func setupMultiUserOAuth(deps Dependencies) error {
	if deps.Config == nil || deps.Config.ServerEdition == nil || !deps.Config.ServerEdition.Enabled {
		deps.Logger.Debug("Server multi-user OAuth: not enabled, skipping setup")
		return nil
	}

	// Spec 107 FR-033 residual: an earlier release with `store_idp_tokens: true`
	// persisted each user's IdP access + offline refresh token in the
	// credential bucket under the bare userID. The writer is gone and nothing
	// reads, lists or deletes those rows through any door, so sweep them here
	// — by key, no decryption, so it needs no encryption key — and BEFORE any
	// fallible step: config validation, bucket creation, the HMAC key and
	// credential-store construction (a key that is set but malformed) can all
	// fail this setup, which the caller only logs, and the sweep must not be
	// lost behind such a failure. Hygiene only: a failed sweep is logged and
	// never keeps the server from coming up.
	if purged, perr := broker.PurgeLegacyIDPSubjectTokens(deps.DB); perr != nil {
		deps.Logger.Warnw("failed to purge legacy IdP subject-token rows from the credential store", "error", perr)
	} else if purged > 0 {
		deps.Logger.Infow("purged legacy IdP subject-token rows left by store_idp_tokens (removed in Spec 107)", "rows", purged)
	}

	// deps.Config is the runtime's live/desired *config.Config — the pointer
	// PATCH /api/v1/config marshals as its merge base and the next write-back
	// persists. The derived values ApplyDefaults fills below (the MCPPROXY_CRED_KEY
	// fallback, the Microsoft "common" tenant, the TTLs) must never land in
	// that document, so they are applied to a clone and the clone is what every
	// handler constructed here receives (Spec 107 FR-039).
	cfg := deps.Config.ServerEdition.Clone()

	// Create user store. Constructing it is infallible; EnsureBuckets below is
	// not, which is why the owner gate is installed against the store BEFORE
	// any fallible step runs.
	userStore := users.NewUserStore(deps.DB)

	// The LIVE view of the whole configuration and of the server-edition
	// block. Both are read through deps.ConfigProvider on every decision —
	// the configuration is hot-reloadable, so anything captured here is only
	// true of the process's first moment: a server the admin adds afterwards,
	// an admin_emails demotion (#1169) or an `access` group-map edit (Spec 107
	// FR-039 part 3) must all take effect on the NEXT request. Falls back to
	// the boot pointers when no provider was supplied (tests, embedders with
	// no config service) or when a reload dropped the block, which is the
	// previous behaviour and never widens anything.
	liveConfig := func() *config.Config {
		if deps.ConfigProvider != nil {
			if live := deps.ConfigProvider(); live != nil {
				return live
			}
		}
		return deps.Config
	}
	serverEditionConfig := teamsauth.ServerEditionConfigProvider(func() *config.ServerEditionConfig {
		if live := liveConfig(); live != nil && live.ServerEdition != nil {
			return live.ServerEdition
		}
		return cfg
	})
	adminServers := teamsapi.AdminServersProvider(func() []*config.ServerConfig {
		if live := liveConfig(); live != nil {
			return live.Servers
		}
		return nil
	})

	// The per-user door handlers, and with them THE entitlement predicate
	// (Spec 107 FR-004): entitledServerNamesFor inside teamsapi.UserHandlers
	// is the only place that decides which servers a tenant may see, use,
	// mint against, connect to or diagnose. It is constructed here, before
	// any fallible step, because the agent-token owner resolution below
	// needs it. deps.StorageManager may be nil in tests; the handlers
	// tolerate that (token doors answer "not available").
	userHandlers := teamsapi.NewUserHandlers(userStore, adminServers, deps.StorageManager, nil, deps.Logger)
	userHandlers.SetServerEditionConfigProvider(serverEditionConfig)

	// Agent tokens outlive the sessions of the user who minted them, and
	// nothing re-checked that user afterwards: a disabled account's tokens
	// kept authenticating, carrying its UserID into every downstream
	// authorisation. The single owner resolution (Spec 107 FR-004, replacing
	// the owner gate + scope resolver pair) answers, from ONE user-store read
	// per authentication: is the owner still active, who are they (email,
	// provider, live role from admin_emails), and what is their entitlement
	// NOW — so every HTTP or MCP authentication receives a fresh, narrow-only
	// intersection (Spec 106 FR-004), including old wildcard credentials and
	// requests on an existing MCP session, and the group term (FR-009) is
	// applied without a second predicate. It is on the authentication hot
	// path, so it is one keyed store read plus the predicate; it fails closed.
	//
	// This is deliberately the FIRST fallible-adjacent thing this function
	// does. SetupAll's error is only logged by wireServerEditionOAuth — the
	// process comes up either way — so anything installed after a fallible
	// step is absent from a server that is nonetheless serving traffic.
	// Installed last, a failure in config validation, bucket creation,
	// HMAC-key derivation or credential-store construction left agent tokens
	// UNGATED: the control that stops a disabled user's tokens authenticating
	// would have been the one thing the failure removed, which is fail-open
	// on precisely the wrong axis.
	//
	// Installed here it fails CLOSED instead: if EnsureBuckets never ran, the
	// resolver's GetUser errors or answers "no such user", and the owned
	// tokens are refused rather than waved through.
	if deps.StorageManager != nil {
		deps.StorageManager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (storage.OwnerResolution, error) {
			se := serverEditionConfig()
			if se == nil {
				return storage.OwnerResolution{}, fmt.Errorf("server entitlement configuration unavailable")
			}
			return userHandlers.ResolveAgentTokenOwner(userID, granted, se.IsAdminEmail)
		})
	}

	// Spec 107 FR-039: defaults (TTLs, Microsoft tenant, MCPPROXY_CRED_KEY
	// fallback) are applied at boot only, to the clone above; Validate itself
	// never mutates, so the write doors run the same rules without persisting
	// derived values.
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("server config validation: %w", err)
	}

	if err := userStore.EnsureBuckets(); err != nil {
		return fmt.Errorf("creating server buckets: %w", err)
	}

	// Get HMAC key for JWT signing
	hmacKey, err := auth.GetOrCreateHMACKey(deps.DataDir)
	if err != nil {
		return fmt.Errorf("getting HMAC key: %w", err)
	}

	// The LIVE trusted_proxies list (Spec 107 FR-027, edition-neutral, hot-
	// reloadable): every forwarded-header reader below evaluates this per
	// request and never captures the boot slice.
	trustedProxies := config.TrustedProxiesProvider(func() []string {
		if deps.ConfigProvider != nil {
			if live := deps.ConfigProvider(); live != nil {
				return live.TrustedProxies
			}
		}
		return deps.Config.TrustedProxies
	})

	// Front door (Spec 107 FR-025/FR-026): public_url is restart-pinned and
	// resolved here; the Secure policy of the session cookie is auto|true|false
	// over the public_url scheme, in-process TLS and a trusted X-Forwarded-Proto.
	publicURL := strings.TrimSuffix(cfg.PublicURL, "/")
	tlsEnabled := deps.Config.TLS != nil && deps.Config.TLS.Enabled
	securePolicy := teamsauth.CookieSecurePolicy{
		Mode:           cfg.EffectiveSessionCookieSecure(),
		PublicURLHTTPS: cfg.PublicURLIsHTTPS() || tlsEnabled,
		TrustedProxies: trustedProxies,
	}
	if publicURL != "" {
		deps.Logger.Infow("Server edition front door: public_url resolved",
			"public_url", publicURL,
			"callback_url", publicURL+"/api/v1/auth/callback",
			"session_cookie_secure", securePolicy.Mode)
	} else if !config.ListenIsLoopback(deps.Config.Listen) {
		deps.Logger.Warnw("server_edition.public_url is unset while listening on a non-loopback address; set it (or MCPPROXY_PUBLIC_URL) so the OAuth callback URL and the Secure cookie decision do not depend on Host or X-Forwarded-* headers",
			"listen", deps.Config.Listen)
	}
	if securePolicy.Mode == config.SessionCookieSecureFalse {
		deps.Logger.Warnw("server_edition.session_cookie_secure is explicitly false: the session cookie is sent without the Secure attribute; only a plain-http loopback or test deployment should run this way")
	}

	// Create session manager
	sessionTTL := cfg.SessionTTL.Duration()
	if sessionTTL == 0 {
		sessionTTL = 24 * time.Hour
	}
	sessionManager := teamsauth.NewSessionManagerWithPolicy(userStore, sessionTTL, securePolicy)

	// Create OAuth handler. It resolves the identity provider once from the
	// boot block (discovery stays lazy) and derives each login's role from the
	// LIVE admin_emails through the same provider (Spec 107 T044).
	oauthHandler := teamsauth.NewOAuthHandler(userStore, sessionManager, serverEditionConfig, hmacKey, deps.Logger)
	oauthHandler.SetTrustedProxiesProvider(trustedProxies)

	// The per-user credential store backs the oauth_connect flow (spec 074
	// Path B): credentials a user connects are stored here, encrypted under
	// MCPPROXY_CRED_KEY or server_edition.credential_encryption_key. With no key
	// it is constructed disabled and the connect surface reports so. Nothing
	// injects a stored credential into a proxied request (Spec 107 FR-034).
	credStore, err := broker.NewBBoltAESStore(deps.DB, credentialMasterKey(cfg), deps.Logger.Desugar())
	if err != nil {
		return fmt.Errorf("creating credential store: %w", err)
	}

	// Create auth middleware
	authMiddleware := teamsauth.NewServerEditionAuthMiddleware(sessionManager, userStore, serverEditionConfig, hmacKey, deps.Logger)

	// Register OAuth routes on the router.
	// Login and callback are public (no auth required).
	// These are mounted outside the API key auth group.
	deps.Router.Get("/api/v1/auth/login", oauthHandler.HandleLogin)
	deps.Router.Get("/api/v1/auth/callback", oauthHandler.HandleCallback)
	// The public edition probe (Spec 107 FR-030, T053) is mounted beside
	// login/callback, outside every auth group, only on this enabled block.
	authEndpoints := teamsapi.NewAuthEndpoints(userStore, sessionManager, cfg, hmacKey, deps.Logger)
	authEndpoints.RegisterPublicRoutesWithPrefix(deps.Router, "/api/v1")

	// Shared servers are the main config servers (admin-configured). The boot
	// slice is handed to the handlers that still take one; every entitlement
	// decision reads the LIVE list through adminServers above.
	sharedServers := deps.Config.Servers

	// Spec 107 FR-007: an access-map entry that names no configured server is
	// a boot warning and a `doctor` finding (config.DoctorFindings), never a
	// refusal — the server may be added later, and until then the entry
	// grants nothing.
	if unknown := cfg.Access.UnknownAccessServerNames(sharedServers); len(unknown) > 0 {
		deps.Logger.Warnw(config.AccessUnknownServerNamesFinding(unknown), "unknown_servers", unknown)
	}

	// All server edition endpoints that require session cookie or JWT authentication.
	// Mounted outside the API key group so session cookies work.
	configPath := config.GetConfigPath(deps.Config.DataDir)
	adminHandlers := teamsapi.NewAdminHandlers(userStore, nil, sessionManager, cfg.AdminEmails, sharedServers, deps.Config, configPath, deps.ManagementService, deps.Logger)
	adminHandlers.SetSharingUpdater(deps.SetServerShared)
	adminHandlers.SetAdminServersProvider(adminServers)
	if deps.StorageManager != nil {
		adminHandlers.SetTokenRevoker(deps.StorageManager)
		adminHandlers.SetAgentTokenStore(deps.StorageManager)
	}
	userHandlers.SetHMACKey(hmacKey)
	userActivityHandlers := teamsapi.NewUserActivityHandlers(nil, userStore, sharedServers, deps.Logger)
	userActivityHandlers.SetAdminServersProvider(adminServers)
	userActivityHandlers.SetEntitlement(userHandlers)
	// Per-user brokered-credential surfaces (spec 074 T8): list connection
	// status, disconnect, and the Path B connect/callback flow. Reuses the same
	// credential store wired into the OAuth login handler above. The audit sink
	// (spec 074 T10) records connect/acquire/refresh/inject events to the existing
	// activity log; a nil StorageManager yields a no-op sink (auditing is
	// best-effort and never blocks brokering).
	brokerAudit := teamsapi.NewActivityAuditSink(deps.StorageManager, deps.Logger)
	credentialHandlers := teamsapi.NewCredentialHandlers(credStore, sharedServers, brokerAudit, deps.Logger)
	credentialHandlers.SetAdminServersProvider(adminServers)
	credentialHandlers.SetEntitlement(userHandlers)
	credentialHandlers.SetFrontDoor(publicURL, trustedProxies)

	deps.Router.Group(func(r chi.Router) {
		r.Use(authMiddleware.Middleware())
		r.Post("/api/v1/auth/logout", oauthHandler.HandleLogout)
		authEndpoints.RegisterRoutesWithPrefix(r, "/api/v1")
		adminHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
		userHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
		userActivityHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
		credentialHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	})

	deps.Logger.Infow("Server multi-user OAuth initialized",
		"provider", cfg.OAuth.Provider,
		"admin_emails", cfg.AdminEmails,
	)

	return nil
}
