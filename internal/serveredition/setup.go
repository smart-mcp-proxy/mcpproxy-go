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

	// Agent tokens outlive the sessions of the user who minted them, and nothing
	// re-checked that user afterwards: a disabled account's tokens kept
	// authenticating, carrying its UserID into every downstream authorisation.
	// Gate them on the owner's current state. The gate is on the authentication
	// hot path, so it is a single keyed store read and no more; it fails closed.
	//
	// This is deliberately the FIRST thing this function does. SetupAll's error
	// is only logged by wireServerEditionOAuth — the process comes up either
	// way — so anything installed after a fallible step is absent from a server
	// that is nonetheless serving traffic. Installed last, a failure in config
	// validation, bucket creation, HMAC-key derivation or credential-store
	// construction left agent tokens UNGATED: the control that stops a disabled
	// user's tokens authenticating would have been the one thing the failure
	// removed, which is fail-open on precisely the wrong axis.
	//
	// Installed here it fails CLOSED instead: if EnsureBuckets never ran, the
	// gate's GetUser errors or answers "no such user", and the owned tokens are
	// refused rather than waved through.
	if deps.StorageManager != nil {
		deps.StorageManager.SetAgentTokenOwnerGate(func(userID string) (bool, error) {
			user, err := userStore.GetUser(userID)
			if err != nil {
				return false, err
			}
			if user == nil {
				// The owner is gone from the store entirely. A token for an
				// identity that no longer exists must not authenticate.
				return false, nil
			}
			return !user.Disabled, nil
		})
		// Like the owner gate, install before any fallible setup step. Every
		// HTTP authentication receives a fresh intersection, including old
		// wildcard credentials and requests on an existing MCP session.
		deps.StorageManager.SetAgentTokenScopeResolver(func(userID string, granted []string) ([]string, error) {
			live := deps.Config
			if deps.ConfigProvider != nil {
				live = deps.ConfigProvider()
			}
			if live == nil || live.ServerEdition == nil {
				return nil, fmt.Errorf("server entitlement configuration unavailable")
			}
			user, err := userStore.GetUser(userID)
			if err != nil {
				return nil, err
			}
			if user == nil || user.Disabled {
				return nil, fmt.Errorf("token owner unavailable")
			}
			scope := teamsapi.NewUserHandlers(userStore, func() []*config.ServerConfig {
				return live.Servers
			}, nil, nil, deps.Logger)
			return scope.NarrowTokenServerScope(userID, granted, live.ServerEdition.IsAdminEmail(user.Email))
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

	// The LIVE view of the server-edition block, read through the same provider
	// the admin-servers check uses rather than a second mechanism.
	//
	// `admin_emails` is the sole source of truth for the admin role, and it IS
	// hot-reloadable: the file watcher reloads the whole file (config.LoadFromFile
	// unmarshals `server_edition` in full), `server_edition` is not one of the
	// restart-pinned fields, and the result is republished as the snapshot
	// ConfigProvider reads. Deriving the role from `cfg` — the boot-time pointer
	// — therefore meant a demotion took effect only on the next process restart,
	// which is issue #1169 with a different horizon rather than issue #1169
	// closed.
	//
	// Falls back to the boot block when there is no provider (tests, embedders
	// with no config service) or when a reload dropped the block entirely, which
	// is the previous behaviour and never widens the admin list.
	serverEditionConfig := teamsauth.ServerEditionConfigProvider(func() *config.ServerEditionConfig {
		if deps.ConfigProvider != nil {
			if live := deps.ConfigProvider(); live != nil && live.ServerEdition != nil {
				return live.ServerEdition
			}
		}
		return cfg
	})

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

	// Shared servers are the main config servers (admin-configured).
	sharedServers := deps.Config.Servers

	// The LIVE view of the same thing. The configuration is hot-reloadable, so
	// the slice above is only true of the process's first moment: a server the
	// admin adds afterwards is absent from it forever. Every check that decides
	// what a tenant may name or reach — the personal-server name collision and
	// the token-scope entitlement set in api.UserHandlers — must read through
	// this instead, or a tenant can pre-empt a name the admin has not created
	// yet and walk through the entitlement control on a stale snapshot.
	//
	// Falls back to the boot slice when no provider was supplied (tests, and
	// any embedder that has no config service), which is the previous behaviour.
	adminServers := teamsapi.AdminServersProvider(func() []*config.ServerConfig {
		if deps.ConfigProvider != nil {
			if live := deps.ConfigProvider(); live != nil {
				return live.Servers
			}
		}
		return sharedServers
	})

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
	userHandlers := teamsapi.NewUserHandlers(userStore, adminServers, deps.StorageManager, hmacKey, deps.Logger)
	userActivityHandlers := teamsapi.NewUserActivityHandlers(nil, userStore, sharedServers, deps.Logger)
	userActivityHandlers.SetAdminServersProvider(adminServers)
	// Per-user brokered-credential surfaces (spec 074 T8): list connection
	// status, disconnect, and the Path B connect/callback flow. Reuses the same
	// credential store wired into the OAuth login handler above. The audit sink
	// (spec 074 T10) records connect/acquire/refresh/inject events to the existing
	// activity log; a nil StorageManager yields a no-op sink (auditing is
	// best-effort and never blocks brokering).
	brokerAudit := teamsapi.NewActivityAuditSink(deps.StorageManager, deps.Logger)
	credentialHandlers := teamsapi.NewCredentialHandlers(credStore, sharedServers, brokerAudit, deps.Logger)
	credentialHandlers.SetAdminServersProvider(adminServers)
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
