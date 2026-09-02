//go:build server

package serveredition

import (
	"fmt"
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

func setupMultiUserOAuth(deps Dependencies) error {
	if deps.Config == nil || deps.Config.ServerEdition == nil || !deps.Config.ServerEdition.Enabled {
		deps.Logger.Debug("Server multi-user OAuth: not enabled, skipping setup")
		return nil
	}

	cfg := deps.Config.ServerEdition

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
	}

	// Validate server config
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

	// Create session manager
	sessionTTL := cfg.SessionTTL.Duration()
	if sessionTTL == 0 {
		sessionTTL = 24 * time.Hour
	}
	sessionManager := teamsauth.NewSessionManager(userStore, sessionTTL, false) // secure=false for localhost

	// Create OAuth handler
	oauthHandler := teamsauth.NewOAuthHandler(userStore, sessionManager, cfg, hmacKey, deps.Logger)

	// Wire the per-user credential store so IdP subject tokens can be captured at
	// login when teams.store_idp_tokens is enabled (spec 074). The store derives
	// its key from MCPPROXY_CRED_KEY or teams.credential_encryption_key; with no
	// key it is constructed disabled and token capture is silently skipped.
	credStore, err := broker.NewBBoltAESStore(deps.DB, broker.ResolveMasterKey(cfg.CredentialEncryptionKey), deps.Logger.Desugar())
	if err != nil {
		return fmt.Errorf("creating credential store: %w", err)
	}
	oauthHandler.SetCredentialStore(credStore)

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

	// Create auth middleware
	authMiddleware := teamsauth.NewServerEditionAuthMiddleware(sessionManager, userStore, serverEditionConfig, hmacKey, deps.Logger)

	// Register OAuth routes on the router.
	// Login and callback are public (no auth required).
	// These are mounted outside the API key auth group.
	deps.Router.Get("/api/v1/auth/login", oauthHandler.HandleLogin)
	deps.Router.Get("/api/v1/auth/callback", oauthHandler.HandleCallback)

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
	authEndpoints := teamsapi.NewAuthEndpoints(userStore, sessionManager, cfg, hmacKey, deps.Logger)
	configPath := config.GetConfigPath(deps.Config.DataDir)
	adminHandlers := teamsapi.NewAdminHandlers(userStore, nil, sessionManager, cfg.AdminEmails, sharedServers, deps.Config, configPath, deps.ManagementService, deps.Logger)
	adminHandlers.SetTokenRevoker(deps.StorageManager)
	userHandlers := teamsapi.NewUserHandlers(userStore, adminServers, deps.StorageManager, hmacKey, deps.Logger)
	userActivityHandlers := teamsapi.NewUserActivityHandlers(nil, userStore, sharedServers, deps.Logger)
	// Per-user brokered-credential surfaces (spec 074 T8): list connection
	// status, disconnect, and the Path B connect/callback flow. Reuses the same
	// credential store wired into the OAuth login handler above. The audit sink
	// (spec 074 T10) records connect/acquire/refresh/inject events to the existing
	// activity log; a nil StorageManager yields a no-op sink (auditing is
	// best-effort and never blocks brokering).
	brokerAudit := teamsapi.NewActivityAuditSink(deps.StorageManager, deps.Logger)
	credentialHandlers := teamsapi.NewCredentialHandlers(credStore, sharedServers, brokerAudit, deps.Logger)

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
