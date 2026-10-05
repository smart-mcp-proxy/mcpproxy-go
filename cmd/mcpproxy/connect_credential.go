package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Client credentials in `mcpproxy connect` (Spec 108 FR-024). Connect never
// writes the instance admin API key: with a running daemon the write goes
// through POST /api/v1/connect/{client} (the daemon mints, guards, records and
// notifies); with no daemon the same clients service runs locally over
// config.db with the strictest guard.

var (
	connectProfile    string
	connectLock       bool
	connectSwitchable bool
	connectKeyless    bool
)

func addConnectCredentialFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&connectProfile, "profile", "all",
		`Profile the client's credential binds to: a profile name, or "all" for all servers. `+
			`Default: all servers for a new credential; a reconnect keeps the client's existing binding`)
	cmd.Flags().BoolVar(&connectLock, "lock", false, "Lock the client to its profile: it can never switch (default for a named profile)")
	cmd.Flags().BoolVar(&connectSwitchable, "switchable", false, "Let the client switch profiles within its profile's switchable_to (default for all servers)")
	cmd.Flags().BoolVar(&connectKeyless, "keyless", false, "Write an entry with no credential (only while require_mcp_auth is off; the client is unidentified)")
	cmd.MarkFlagsMutuallyExclusive("lock", "switchable")
}

// connectIntentFromFlags builds the credential intent from the flags, refusing
// the combinations that cannot work before anything is read or written.
func connectIntentFromFlags(cmd *cobra.Command) (connect.CredentialIntent, error) {
	intent := connect.CredentialIntent{Keyless: connectKeyless, Surface: string(profile.SurfaceCLI)}
	profileGiven := cmd.Flags().Changed("profile")
	if profileGiven {
		p := connectProfile
		if strings.EqualFold(p, "all") {
			p = ""
		}
		intent.Profile = &p
	}
	switch {
	case connectLock:
		m := auth.ProfileModeLocked
		intent.Mode = &m
	case connectSwitchable:
		m := auth.ProfileModeSwitchable
		intent.Mode = &m
	}
	if connectKeyless && ((intent.Profile != nil && *intent.Profile != "") || intent.Mode != nil) {
		return intent, newFlagValidationError("--keyless cannot be combined with --profile, --lock or --switchable: an unidentified client has no binding")
	}
	if connectLock && intent.Profile != nil && *intent.Profile == "" {
		return intent, newFlagValidationError("--lock needs a profile: all servers is always switchable")
	}
	return intent, nil
}

// connectBackend performs a connect write for the CLI.
type connectBackend interface {
	connect(clientID, serverName string, force bool, intent connect.CredentialIntent) (*connect.ConnectResult, error)
	close()
	// viaDaemon reports whether the daemon already recorded the connection
	// (so the CLI must not relay it again).
	viaDaemon() bool
}

// newConnectBackend picks the daemon when one is reachable, else the local
// fallback (plan D22).
func newConnectBackend(cfg *config.Config) (connectBackend, error) {
	if client, ok := newDaemonClient(cfg, nil); ok {
		return &daemonConnectBackend{client: client}, nil
	}
	return newOfflineConnectBackend(cfg)
}

// --- daemon ---------------------------------------------------------------

type daemonConnectBackend struct{ client *cliclient.Client }

func (d *daemonConnectBackend) close()          {}
func (d *daemonConnectBackend) viaDaemon() bool { return true }

type connectRequestBody struct {
	ServerName string  `json:"server_name,omitempty"`
	Force      bool    `json:"force,omitempty"`
	Profile    *string `json:"profile,omitempty"`
	Mode       *string `json:"mode,omitempty"`
	Keyless    bool    `json:"keyless,omitempty"`
}

func (d *daemonConnectBackend) connect(clientID, serverName string, force bool, intent connect.CredentialIntent) (*connect.ConnectResult, error) {
	body, err := json.Marshal(connectRequestBody{
		ServerName: serverName, Force: force, Profile: intent.Profile, Mode: intent.Mode, Keyless: intent.Keyless,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := d.client.DoRaw(ctx, http.MethodPost, "/api/v1/connect/"+clientID, body)
	if err != nil {
		return nil, fmt.Errorf("connect request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseConnectResponse(resp.StatusCode, raw, clientID)
}

// parseConnectResponse maps a POST /api/v1/connect/{client} answer to a result
// or to the same typed errors the local path returns, so both render alike.
func parseConnectResponse(status int, raw []byte, clientID string) (*connect.ConnectResult, error) {
	var env struct {
		Success          bool                  `json:"success"`
		Data             connect.ConnectResult `json:"data"`
		Error            string                `json:"error"`
		Action           string                `json:"action"`
		Code             string                `json:"code"`
		Field            string                `json:"field"`
		Bindings         []runtime.BindingRef  `json:"bindings"`
		Fixes            []runtime.GuardFix    `json:"fixes"`
		ConflictingToken string                `json:"conflicting_token"`
		Remediation      string                `json:"remediation"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unexpected response (%d): %s", status, strings.TrimSpace(string(raw)))
	}
	switch {
	case status == http.StatusOK && env.Success:
		res := env.Data
		return &res, nil
	case status == http.StatusConflict && env.Action != "":
		// already_exists / precondition_failed: a result, not a failure.
		res := env.Data
		return &res, nil
	case status == http.StatusConflict && env.Code == profile.ErrorCodeBindingBypassable:
		return nil, &runtime.BindingGuardError{Bindings: env.Bindings, Fixes: env.Fixes}
	case status == http.StatusConflict && env.ConflictingToken != "":
		return nil, &connectConflictError{Token: env.ConflictingToken, Remediation: env.Remediation}
	}
	msg := env.Error
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	return nil, errors.New(msg)
}

// --- offline --------------------------------------------------------------

// offlineConnectBackend runs the clients service locally over config.db when
// no daemon is reachable. Its guard is runtime.StrictOfflineBindingGuard: it
// cannot evaluate the tool-dependent reachability comparison without a
// published snapshot, so while require_mcp_auth is off it refuses any new or
// changed named binding; a reconnect that keeps the recorded binding is
// allowed. Only ever stricter than the daemon's.
type offlineConnectBackend struct {
	sm  *storage.Manager
	svc *connect.Service
}

func (o *offlineConnectBackend) close()          { _ = o.sm.Close() }
func (o *offlineConnectBackend) viaDaemon() bool { return false }

func newOfflineConnectBackend(cfg *config.Config) (*offlineConnectBackend, error) {
	sm, err := storage.NewManager(cfg.DataDir, zap.NewNop().Sugar())
	if err != nil {
		var locked *storage.DatabaseLockedError
		if errors.As(err, &locked) {
			return nil, errors.New("a running mcpproxy holds the database; its socket was not reachable")
		}
		return nil, fmt.Errorf("cannot open the client credential store: %w", err)
	}
	clients := runtime.NewClientsService(runtime.ClientsServiceDeps{
		Store:    sm,
		HMACKey:  func() ([]byte, error) { return auth.GetOrCreateHMACKey(cfg.DataDir) },
		Config:   func() *config.Config { return cfg },
		Guard:    func() runtime.BindingGuard { return runtime.StrictOfflineBindingGuard{} },
		Activity: sm.SaveActivity,
	})
	svc := connect.NewService(cfg.Listen, cfg.APIKey).
		WithRequireMCPAuth(config.EffectiveRequireMCPAuth(cfg)).
		WithCredentialMinter(clients.ConnectMinter())
	return &offlineConnectBackend{sm: sm, svc: svc}, nil
}

func (o *offlineConnectBackend) connect(clientID, serverName string, force bool, intent connect.CredentialIntent) (*connect.ConnectResult, error) {
	intent.ActorKind = "cli_offline"
	intent.Surface = string(profile.SurfaceCLI)
	return o.svc.ConnectWithOptions(clientID, serverName, connect.ConnectOptions{Force: force, Intent: intent})
}

// --- errors and rendering -------------------------------------------------

// connectConflictError is the name-conflict refusal: client-<id> is held by a
// regular agent token, which connect never touches.
type connectConflictError struct{ Token, Remediation string }

func (e *connectConflictError) Error() string {
	return fmt.Sprintf("token name %s is held by a regular agent token", e.Token)
}

// describeConnectFailure turns a connect error into the CLI's message: the
// refusal text, then the fixes or remediation. The result is a
// flagValidationError so it always exits 1 (the message text mentions
// "config", which the string heuristics would otherwise map to exit 4).
func describeConnectFailure(err error, clientID string) error {
	var guardErr *runtime.BindingGuardError
	var conflict *connectConflictError
	var val *runtime.ValidationError
	var busy *runtime.ConnectInProgressError
	var superseded *runtime.CredentialSupersededError
	var b strings.Builder
	switch {
	case errors.As(err, &busy):
		b.WriteString(busy.Error())
	case errors.As(err, &superseded):
		b.WriteString(superseded.Error())
	case errors.As(err, &guardErr):
		b.WriteString(guardErr.Error())
		b.WriteString("\nFixes:")
		for _, fix := range guardErr.Fixes {
			b.WriteString("\n  - ")
			b.WriteString(guardFixText(fix, guardErr))
		}
	case errors.As(err, &conflict):
		b.WriteString(conflict.Error())
		remediation := conflict.Remediation
		if remediation == "" {
			remediation = fmt.Sprintf("revoke or delete token %s, then connect again", conflict.Token)
		}
		b.WriteString("\n" + remediation)
	case errors.Is(err, storage.ErrClientCredentialConflict):
		name := auth.ClientTokenName(clientID)
		fmt.Fprintf(&b, "token name %s is held by a regular agent token\nrevoke or delete token %s, then connect again", name, name)
	case errors.As(err, &val):
		b.WriteString(val.Message)
	default:
		return err
	}
	return flagValidationError{errors.New(b.String())}
}

func guardFixText(fix runtime.GuardFix, guardErr *runtime.BindingGuardError) string {
	name := ""
	if len(guardErr.Bindings) > 0 {
		name = guardErr.Bindings[0].Profile
	}
	return guardFixLine(fix, name)
}

// connectCredentialLine is the credential line of a successful connect:
//
//	Credential: mcp_cli_•••• (token client-cursor, profile ro, locked)
//	Credential: mcp_cli_•••• (token client-cursor, all servers, switchable)
//	Credential: none (keyless)
//
// It is empty when the result carries no credential information (a result
// from a daemon that predates Spec 108).
func connectCredentialLine(r *connect.ConnectResult) string {
	switch {
	case r.Keyless:
		return "Credential: none (keyless)"
	case r.Credential != "":
		scope := "all servers"
		if r.Profile != "" {
			scope = "profile " + r.Profile
		}
		return fmt.Sprintf("Credential: %s (token %s, %s, %s)", r.Credential, r.TokenName, scope, r.Mode)
	}
	return ""
}
