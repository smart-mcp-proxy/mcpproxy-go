package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// The `profiles` admin MCP tool (Spec 108-h, FR-017/FR-037): profile CRUD,
// classification, client bindings and the access explainer for an
// administrator agent. It calls the same services as the REST routes and
// returns the same `data` objects and error bodies (httpapi.ProfilesErrorBody
// and the httpapi admin views), so the two surfaces cannot drift.
//
// Registration is deliberately NOT through buildManagementTools(), which returns
// nothing under read_only_mode / disable_management: the read operations stay
// available there and the mutating ones refuse per call from the live config.

const profilesToolName = "profiles"

// profilesUnknownToolText is the uniform refusal of a caller the tool is hidden
// from; it is the text upstream_servers answers, so a hidden tool and a
// nonexistent one look alike.
const profilesUnknownToolText = "unknown tool: " + profilesToolName

// profilesAdminViews is what the tool needs from the REST server: the admin read
// views and the decorated client rows (internal/httpapi/admin_views.go and
// clients_views.go). *httpapi.Server implements it; SetAdminViews installs it
// where the REST server is wired.
type profilesAdminViews interface {
	ClientsSupported() bool
	ProfileListData(ctx context.Context) (*runtime.ProfileList, error)
	ProfileViewData(ctx context.Context, name string) (*runtime.ProfileView, error)
	EffectiveToolsData(ctx context.Context, name, client, server, reason string) (*runtime.EffectiveToolsResult, error)
	ExplainAccess(ctx context.Context, q httpapi.ExplainRequest) (*runtime.AccessExplanation, error)
	ClientsListData(ctx context.Context, profileFilter, clientFilter string) (map[string]any, error)
	ClientBindingData(ctx context.Context, clientID string) (map[string]any, error)
}

var _ profilesAdminViews = (*httpapi.Server)(nil)

// SetAdminViews installs the REST server's admin views behind the `profiles`
// tool (called next to httpAPIServer.SetClientsService in server.go).
func (p *MCPProxyServer) SetAdminViews(v profilesAdminViews) {
	p.profilesViewsMu.Lock()
	p.profilesViews = v
	p.profilesViewsMu.Unlock()
}

func (p *MCPProxyServer) adminViews() profilesAdminViews {
	p.profilesViewsMu.RLock()
	defer p.profilesViewsMu.RUnlock()
	return p.profilesViews
}

// buildProfilesTool is the tool definition. Description: three sentences.
func buildProfilesTool() mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("Administrator tool to manage profiles and client bindings; every operation takes the REST argument names and returns the REST data (/api/v1/profiles, /clients, /access/explain). " +
			"Operations: list, get, create, update (full replace), delete, rename, classify, assign (one client to a profile, or bulk from_profile to to_profile), list_clients, effective_tools, explain. " +
			"Writes are refused under read_only_mode or disable_management and by the binding guard (binding_bypassable_without_auth); errors carry the REST error body."),
		mcp.WithTitleAnnotation("Manage profiles and client bindings"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	}
	return mcp.NewTool(profilesToolName, append(opts, profilesToolOptions()...)...)
}

// buildProfilesServerTool wraps buildProfilesTool for the three builders that
// register it (default, call-tool and code-execution servers).
func (p *MCPProxyServer) buildProfilesServerTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{Tool: buildProfilesTool(), Handler: p.handleProfiles}
}

// --- visibility (FR-017) -------------------------------------------------------

// profilesToolAccess decides whether the caller may see and call `profiles`;
// it is the shared administrator-tool predicate (mcp_admin_access.go), which
// the Spec 115 `credentials` tool uses verbatim.
func (p *MCPProxyServer) profilesToolAccess(ctx context.Context) (admin, visible bool) {
	return p.adminToolAccess(ctx)
}

// filterProfilesTool drops `profiles` from a tools/list the caller may not see
// it in. It runs first in filterProfileV3Tools, and mcp-go re-runs the filter at
// tools/call, so a hidden tool cannot be called either.
func (p *MCPProxyServer) filterProfilesTool(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	found := false
	for _, tool := range tools {
		if tool.Name == profilesToolName {
			found = true
			break
		}
	}
	if !found {
		return tools
	}
	if _, visible := p.profilesToolAccess(ctx); visible {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != profilesToolName {
			out = append(out, tool)
		}
	}
	return out
}

// --- errors --------------------------------------------------------------------

// profilesArgError is a refusal of the tool's own arguments, before any service
// runs. It renders as {error, field?}.
type profilesArgError struct {
	msg, field string
}

func (e *profilesArgError) Error() string { return e.msg }

func missingArg(op, name string) error {
	return &profilesArgError{msg: fmt.Sprintf("%s: missing required argument %q", op, name), field: name}
}

func invalidArg(name, detail string) error {
	return &profilesArgError{msg: fmt.Sprintf("invalid argument %q: %s", name, detail), field: name}
}

// profilesErrorText renders err as the tool's error text: the REST error body
// without the `success` and `request_id` envelope.
func profilesErrorText(err error) string {
	var argErr *profilesArgError
	var body map[string]any
	if errors.As(err, &argErr) {
		body = map[string]any{"error": argErr.msg}
		if argErr.field != "" {
			body["field"] = argErr.field
		}
	} else {
		_, body = httpapi.ProfilesErrorBody(err)
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return `{"error":"profiles operation failed"}`
	}
	return string(raw)
}

// --- handler -------------------------------------------------------------------

// handleProfiles dispatches one `profiles` operation.
func (p *MCPProxyServer) handleProfiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.recordMCPSurface()
	p.recordBuiltinTool(profilesToolName)
	// Defence in depth: the tool filter already hides the tool, but a handler
	// reached another way must refuse the same way. Only an administrator
	// credential the profile hides it from is recorded; for anyone else there is
	// nothing to attribute.
	if admin, visible := p.profilesToolAccess(ctx); !visible {
		if admin {
			p.recordProfileManagementRefusal(ctx, profilesToolName)
		}
		return mcp.NewToolResultError(profilesUnknownToolText), nil
	}

	start := time.Now()
	sessionID := sessionIDFromContext(ctx)
	requestID := mintCorrelationID(profilesToolName)
	args := activityArgsFromRequest(request)

	data, err := p.runProfilesOperation(ctx, request.GetArguments())

	var result *mcp.CallToolResult
	var responseText, errText string
	if err != nil {
		errText = profilesErrorText(err)
		result = mcp.NewToolResultError(errText)
	} else {
		raw, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			errText = profilesErrorText(marshalErr)
			result = mcp.NewToolResultError(errText)
		} else {
			responseText = string(raw)
			result = mcp.NewToolResultText(responseText)
		}
	}
	if errText != "" {
		p.emitActivityInternalToolCall(ctx, profilesToolName, "", "", "", sessionID, requestID, "error", errText, time.Since(start).Milliseconds(), args, nil, nil, "")
	} else {
		p.emitActivityInternalToolCall(ctx, profilesToolName, "", "", "", sessionID, requestID, "success", "", time.Since(start).Milliseconds(), args, redactBuiltinResponseForActivity(responseText), nil, "")
	}
	return result, nil
}

// profilesActor names who acts. The MCP request's credential kind is already
// stamped by the auth middleware; a tray connection is the socket kind exactly
// as actorFromRequest treats it on REST.
func profilesActor(ctx context.Context) runtime.Actor {
	a := runtime.ActorFromContext(ctx, profile.SurfaceMCP)
	if transport.GetConnectionSource(ctx) == transport.ConnectionSourceTray {
		a.Kind = string(auth.CredentialKindSocket)
	}
	return a
}

func (p *MCPProxyServer) runtimeForProfiles() *runtime.Runtime {
	if p.mainServer == nil {
		return nil
	}
	return p.mainServer.runtime
}

// runProfilesOperation validates the operation and its arguments and calls the
// service. It returns the REST `data` object for the operation.
func (p *MCPProxyServer) runProfilesOperation(ctx context.Context, args map[string]any) (any, error) {
	op, _, err := argString(args, "operation")
	if err != nil {
		return nil, err
	}
	if op == "" {
		return nil, &profilesArgError{msg: `missing required argument "operation"`, field: "operation"}
	}
	valid := false
	for _, name := range profilesOperations {
		valid = valid || name == op
	}
	if !valid {
		return nil, &profilesArgError{
			msg:   fmt.Sprintf("unknown operation %q; valid: %s", op, strings.Join(profilesOperations, ", ")),
			field: "operation",
		}
	}
	// The global gates are read per call from the LIVE config, never from the
	// construction-time config, so a hot reload applies without re-registering
	// the tool. The texts are byte-equal to upstream_servers'.
	if profilesMutatingOps[op] {
		if cfg := p.currentConfig(); cfg != nil {
			if cfg.ReadOnlyMode {
				return nil, &profilesArgError{msg: "Operation not allowed in read-only mode"}
			}
			if cfg.DisableManagement {
				return nil, &profilesArgError{msg: "Server management is disabled for security"}
			}
		}
	}

	views := p.adminViews()
	rt := p.runtimeForProfiles()
	if views == nil || rt == nil {
		return nil, runtime.ErrEvaluatorUnavailable
	}

	switch op {
	case "list":
		return views.ProfileListData(ctx)
	case "get":
		name, err := requireString(args, op, "name")
		if err != nil {
			return nil, err
		}
		return views.ProfileViewData(ctx, name)
	case "create":
		cfg, err := profileConfigFromArgs(args)
		if err != nil {
			return nil, err
		}
		if cfg.Name == "" {
			return nil, missingArg(op, "name")
		}
		return rt.ProfilesService().Create(ctx, profilesActor(ctx), cfg)
	case "update":
		cfg, err := profileConfigFromArgs(args)
		if err != nil {
			return nil, err
		}
		if cfg.Name == "" {
			return nil, missingArg(op, "name")
		}
		return rt.ProfilesService().Update(ctx, profilesActor(ctx), cfg.Name, cfg)
	case "delete":
		name, err := requireString(args, op, "name")
		if err != nil {
			return nil, err
		}
		reassign, _, err := argString(args, "reassign_to")
		if err != nil {
			return nil, err
		}
		force, _, err := argBool(args, "force")
		if err != nil {
			return nil, err
		}
		return rt.ProfilesService().Delete(ctx, profilesActor(ctx), name, reassign, force)
	case "rename":
		name, err := requireString(args, op, "name")
		if err != nil {
			return nil, err
		}
		newName, err := requireString(args, op, "new_name")
		if err != nil {
			return nil, err
		}
		return rt.ProfilesService().Rename(ctx, profilesActor(ctx), name, newName)
	case "classify":
		name, err := requireString(args, op, "name")
		if err != nil {
			return nil, err
		}
		tool, err := requireString(args, op, "tool")
		if err != nil {
			return nil, err
		}
		tier, present, err := argString(args, "tier")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, missingArg(op, "tier")
		}
		return rt.ProfilesService().Classify(ctx, profilesActor(ctx), name, tool, tier)
	case "assign":
		return p.profilesAssign(ctx, rt, views, args)
	case "list_clients":
		return p.profilesListClients(ctx, views, args)
	case "effective_tools":
		name, err := requireString(args, op, "name")
		if err != nil {
			return nil, err
		}
		client, _, err := argString(args, "client")
		if err != nil {
			return nil, err
		}
		server, _, err := argString(args, "server")
		if err != nil {
			return nil, err
		}
		reason, _, err := argString(args, "reason")
		if err != nil {
			return nil, err
		}
		return views.EffectiveToolsData(ctx, name, client, server, reason)
	default: // explain
		var q httpapi.ExplainRequest
		for _, f := range []struct {
			key string
			dst *string
		}{{"client", &q.Client}, {"token", &q.Token}, {"profile", &q.Profile}, {"tool", &q.Tool}} {
			v, _, err := argString(args, f.key)
			if err != nil {
				return nil, err
			}
			*f.dst = v
		}
		anonymous, _, err := argBool(args, "anonymous")
		if err != nil {
			return nil, err
		}
		q.Anonymous = anonymous
		return views.ExplainAccess(ctx, q)
	}
}

// profilesAssign is `assign`: one client to a profile, or every client of one
// profile to another. The two forms never mix.
func (p *MCPProxyServer) profilesAssign(ctx context.Context, rt *runtime.Runtime, views profilesAdminViews, args map[string]any) (any, error) {
	client, _, err := argString(args, "client")
	if err != nil {
		return nil, err
	}
	from, hasFrom, err := argString(args, "from_profile")
	if err != nil {
		return nil, err
	}
	to, hasTo, err := argString(args, "to_profile")
	if err != nil {
		return nil, err
	}
	modeArg, hasMode, err := argString(args, "mode")
	if err != nil {
		return nil, err
	}
	var mode *string
	if hasMode {
		mode = &modeArg
	}
	bulk := hasFrom || hasTo
	switch {
	case client != "" && bulk:
		return nil, &profilesArgError{msg: "assign: use either client or from_profile/to_profile, not both", field: "client"}
	case client == "" && !bulk:
		return nil, missingArg("assign", "client")
	}
	if !views.ClientsSupported() {
		// The server edition has no client credentials.
		return nil, profile.ErrUnknownClient
	}
	clients := rt.ClientsService()
	if clients == nil {
		return nil, runtime.ErrEvaluatorUnavailable
	}

	if !bulk {
		target, hasProfile, err := argString(args, "profile")
		if err != nil {
			return nil, err
		}
		if !hasProfile {
			return nil, &profilesArgError{msg: `assign: missing required argument "profile" (use "" for All servers)`, field: "profile"}
		}
		if _, err := clients.SetBinding(ctx, profilesActor(ctx), client, target, mode); err != nil {
			return nil, err
		}
		return views.ClientBindingData(ctx, client)
	}

	if !hasFrom {
		return nil, &profilesArgError{msg: `assign: missing required argument "from_profile" (use "" for All servers)`, field: "from_profile"}
	}
	if !hasTo {
		return nil, &profilesArgError{msg: `assign: missing required argument "to_profile" (use "" for All servers)`, field: "to_profile"}
	}
	if cfg := p.currentConfig(); cfg != nil && to != "" && !configHasProfileNamed(cfg, to) {
		return nil, &runtime.ValidationError{Field: "to_profile", Message: fmt.Sprintf("unknown profile %q", to)}
	}
	moved, skipped, err := clients.BulkAssign(ctx, profilesActor(ctx), from, to, mode)
	if err != nil {
		return nil, err
	}
	if moved == nil {
		moved = []string{}
	}
	if skipped == nil {
		skipped = []runtime.Skipped{}
	}
	return struct {
		Moved   []string          `json:"moved"`
		Skipped []runtime.Skipped `json:"skipped"`
	}{moved, skipped}, nil
}

func configHasProfileNamed(cfg *config.Config, name string) bool {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return true
		}
	}
	return false
}

// profilesListClients is `list_clients`: the REST list without the routing
// object (a Web/macOS endpoint picker) and without the config paths, which
// identify files on the host and have no use for an agent (H3).
func (p *MCPProxyServer) profilesListClients(ctx context.Context, views profilesAdminViews, args map[string]any) (any, error) {
	profileFilter, _, err := argString(args, "profile")
	if err != nil {
		return nil, err
	}
	clientFilter, _, err := argString(args, "client")
	if err != nil {
		return nil, err
	}
	if !views.ClientsSupported() {
		return nil, profile.ErrUnknownClient
	}
	data, err := views.ClientsListData(ctx, profileFilter, clientFilter)
	if err != nil {
		return nil, err
	}
	delete(data, "routing")
	if rows, ok := data["clients"].([]any); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				delete(m, "config_path")
				delete(m, "display_path")
			}
		}
	}
	return data, nil
}

// --- argument decoding ---------------------------------------------------------

// argString reads a string argument; present reports whether the key was sent
// at all (an empty string is a value, for example "All servers").
func argString(args map[string]any, key string) (val string, present bool, err error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", true, invalidArg(key, "must be a string")
	}
	return s, true, nil
}

func argBool(args map[string]any, key string) (val, present bool, err error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return false, false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, true, invalidArg(key, "must be true or false")
	}
	return b, true, nil
}

func requireString(args map[string]any, op, key string) (string, error) {
	v, _, err := argString(args, key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", missingArg(op, key)
	}
	return v, nil
}

// profileConfigFromArgs builds a config.ProfileConfig from the arguments the
// same way the REST body decodes: only the ProfileConfig keys are taken, through
// encoding/json, so a missing code_execution or management_tools is nil, a
// missing switchable_to is nil while [] is an explicit none, and tools is an
// object (FR-037). Unknown keys inside `tools` are refused as on REST.
func profileConfigFromArgs(args map[string]any) (config.ProfileConfig, error) {
	fields := map[string]any{}
	known := map[string]bool{"operation": true}
	for _, a := range profilesOpArgs {
		known[a.name] = true
	}
	for _, name := range profileConfigJSONFields() {
		known[name] = true
		if v, ok := args[name]; ok {
			fields[name] = v
		}
	}
	// REST decodes the body with DisallowUnknownFields; a top-level typo such as
	// max_teir must not be dropped silently and leave the profile wider than asked.
	var unknown []string
	for key := range args {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return config.ProfileConfig{}, invalidArg(unknown[0], "unknown argument")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return config.ProfileConfig{}, invalidArg("profile", err.Error())
	}
	var cfg config.ProfileConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			field, _, _ := strings.Cut(typeErr.Field, ".")
			return config.ProfileConfig{}, invalidArg(field, fmt.Sprintf("expected %s", typeErr.Type))
		}
		return config.ProfileConfig{}, invalidArg("tools", err.Error())
	}
	return cfg, nil
}
