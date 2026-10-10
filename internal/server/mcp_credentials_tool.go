package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The `credentials` admin MCP tool (Spec 115, contracts/mcp-credentials-tool.md):
// issue, inspect and revoke worker credentials (custom client credentials and
// profile-pinned agent tokens) without the CLI or REST. It is a thin adapter
// over runtime.CredentialsService, the same service REST and CLI issuance go
// through, under the SAME visibility predicate as `profiles`
// (adminToolAccess) and the same live read_only_mode/disable_management
// refusals. Like `profiles` it is registered unconditionally and NOT through
// buildManagementTools (list/get stay readable under the gates).
//
// Secret handling (FR-020a, FR-022, FR-023): the whole argument payload is
// size-capped and screened for secret-shaped keys and values BEFORE anything
// else; a screened call's recorded arguments are replaced wholesale by a
// server-built summary; the raw secret appears ONLY in the create result the
// caller receives, and the activity record stores a server-built summary with
// the credential and snippet replaced.

const credentialsUnknownToolText = "unknown tool: " + credentialsToolName

// credentialRedactedMarker replaces the one-time credential in every stored body.
const credentialRedactedMarker = "[REDACTED: one-time credential]"

// credentialInstallNote is the FR-025 provisioning-versus-installation note.
const credentialInstallNote = "mcpproxy did not install this credential anywhere. Give it to the worker over a channel you trust and add it to that worker's MCP client config; it cannot be shown again. Revoke it with credentials revoke when the task ends."

// credentialDeliveryViews is what the tool needs from the REST server to build
// the one-time delivery (snippet, endpoint, deep links). *httpapi.Server
// implements it.
type credentialDeliveryViews interface {
	CredentialSnippet(secret string) httpapi.ClientSnippet
	MCPEndpoint() string
	CredentialUILinks(client, token, profileName string) httpapi.CredentialLinks
}

var _ credentialDeliveryViews = (*httpapi.Server)(nil)

func (p *MCPProxyServer) buildCredentialsServerTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{Tool: buildCredentialsTool(), Handler: p.handleCredentials}
}

// filterCredentialsTool drops `credentials` from a tools/list the caller may
// not see it in (same predicate as `profiles`). mcp-go re-runs the filter at
// tools/call, and the handler re-checks it.
func (p *MCPProxyServer) filterCredentialsTool(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	found := false
	for _, tool := range tools {
		if tool.Name == credentialsToolName {
			found = true
			break
		}
	}
	if !found {
		return tools
	}
	if _, visible := p.adminToolAccess(ctx); visible {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != credentialsToolName {
			out = append(out, tool)
		}
	}
	return out
}

// --- errors --------------------------------------------------------------------

// credentialsToolError is a refusal of the tool's own arguments.
type credentialsToolError struct {
	code, msg, field string
}

func (e *credentialsToolError) Error() string { return e.msg }

// credentialsErrorBody renders err as the tool's JSON error object {code,
// error, field?, ...}. It never includes a secret, a hash, a storage path or a
// Go error chain (FR-020): unknown errors become a fixed text.
func credentialsErrorBody(err error) map[string]any {
	var argErr *credentialsToolError
	var screen *runtime.SecretInputError
	var large *runtime.ArgumentsTooLargeError
	var ce *runtime.CredentialError
	var guard *runtime.BindingGuardError
	var val *runtime.ValidationError
	body := map[string]any{}
	switch {
	case errors.As(err, &argErr):
		body["code"], body["error"] = argErr.code, argErr.msg
		if argErr.field != "" {
			body["field"] = argErr.field
		}
	case errors.As(err, &screen):
		fields := screen.Fields
		if fields == nil {
			fields = []string{}
		}
		body["code"], body["error"], body["field"] = screen.Code(), screen.Error(), screen.Field()
		body["offending_fields"] = fields
		body["unknown_offending_count"] = screen.UnknownCount
	case errors.As(err, &large):
		body["code"], body["error"] = large.Code(), large.Error()
		body["size_bytes"], body["limit_bytes"] = large.Size, runtime.MaxCredentialArgumentsBytes
	case errors.As(err, &ce) && ce.ErrCode != "":
		body["code"], body["error"] = ce.ErrCode, ce.Msg
		if ce.Field != "" {
			body["field"] = ce.Field
		}
		if ce.State != "" {
			body["state"] = ce.State
		}
	case errors.As(err, &guard):
		_, rest := httpapi.ProfilesErrorBody(guard)
		for k, v := range rest {
			body[k] = v
		}
	case errors.As(err, &val):
		body["code"], body["error"] = profile.CredentialErrorCodeInvalidArgument, fmt.Sprintf("invalid argument %q", val.Field)
		body["field"] = val.Field
	case errors.Is(err, storage.ErrClientCredentialConflict):
		body["code"], body["error"], body["field"], body["state"] = profile.CredentialErrorCodeIdentityExists,
			"a regular agent token already holds this client's token name; choose a new id", "client", "conflicting_token"
	default:
		body["code"], body["error"] = profile.CredentialErrorCodeCredentialsUnavailable, "credentials operation failed"
	}
	return body
}

func credentialsErrorText(err error) string {
	raw, mErr := json.Marshal(credentialsErrorBody(err))
	if mErr != nil {
		return `{"code":"credentials_unavailable","error":"credentials operation failed"}`
	}
	return string(raw)
}

// --- handler -------------------------------------------------------------------

// handleCredentials dispatches one `credentials` operation.
func (p *MCPProxyServer) handleCredentials(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.recordMCPSurface()
	p.recordBuiltinTool(credentialsToolName)
	// Defence in depth (FR-011): the filter hides the tool, the handler refuses
	// the same way. Only an administrator credential hidden by its profile is
	// recorded, exactly as for `profiles`.
	if admin, visible := p.adminToolAccess(ctx); !visible {
		if admin {
			p.recordProfileManagementRefusal(ctx, credentialsToolName)
		}
		return mcp.NewToolResultError(credentialsUnknownToolText), nil
	}

	start := time.Now()
	sessionID := sessionIDFromContext(ctx)
	requestID := mintCorrelationID(credentialsToolName)
	args := request.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	recordedArgs, data, auditResponse, err := p.runCredentialsOperation(ctx, args)
	if err != nil {
		errText := credentialsErrorText(err)
		p.emitActivityInternalToolCall(ctx, credentialsToolName, "", "", "", sessionID, requestID, "error", errText, time.Since(start).Milliseconds(), recordedArgs, nil, nil, "")
		return mcp.NewToolResultError(errText), nil
	}
	raw, mErr := json.Marshal(data)
	if mErr != nil {
		errText := credentialsErrorText(mErr)
		p.emitActivityInternalToolCall(ctx, credentialsToolName, "", "", "", sessionID, requestID, "error", errText, time.Since(start).Milliseconds(), recordedArgs, nil, nil, "")
		return mcp.NewToolResultError(errText), nil
	}
	// FR-022: the stored response is server-built, never a redaction of the
	// delivered text. list/get/revoke carry no secret and are stored as-is
	// (through the audit redaction, whose value rule is the backstop).
	var stored interface{}
	if auditResponse != nil {
		stored = auditResponse
	} else {
		stored = redactBuiltinResponseForActivity(string(raw))
	}
	p.emitActivityInternalToolCall(ctx, credentialsToolName, "", "", "", sessionID, requestID, "success", "", time.Since(start).Milliseconds(), recordedArgs, stored, nil, "")
	return mcp.NewToolResultText(string(raw)), nil
}

// screenedSummary is the wholesale replacement of a screened call's arguments
// (data-model §8): no caller key or value survives except a known operation.
func screenedSummary(args map[string]any, reason string, extra map[string]any) map[string]any {
	out := map[string]any{"_screened": reason}
	if op, ok := args["operation"].(string); ok && isCredentialsOperation(op) {
		out["operation"] = op
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func isCredentialsOperation(op string) bool {
	for _, o := range credentialsOperations {
		if o == op {
			return true
		}
	}
	return false
}

// recordableArgs keeps the KNOWN arguments of a call that passed the screen as
// received; an unknown key is never recorded (its name may be caller-chosen),
// only counted. An operation outside the enum is not recorded either.
func recordableArgs(args map[string]any) map[string]any {
	known := credentialsKnownArgs()
	out := map[string]any{}
	unknown := 0
	for k, v := range args {
		if !known[k] {
			unknown++
			continue
		}
		if k == "operation" {
			if op, ok := v.(string); !ok || !isCredentialsOperation(op) {
				continue
			}
		}
		out[k] = v
	}
	if unknown > 0 {
		out["_unknown_argument_count"] = unknown
	}
	return out
}

// runCredentialsOperation returns the arguments to record, the result for the
// caller, the server-built audit response of a create (nil otherwise) and the
// refusal.
func (p *MCPProxyServer) runCredentialsOperation(ctx context.Context, args map[string]any) (map[string]any, any, any, error) {
	// 0a. Size cap: nothing of an oversized payload is retained or scanned.
	if size := runtime.CanonicalArgumentsSize(args); size > runtime.MaxCredentialArgumentsBytes {
		return screenedSummary(args, "oversized input; arguments not stored", map[string]any{"size_bytes": size}),
			nil, nil, &runtime.ArgumentsTooLargeError{Size: size}
	}
	// 0b. Whole-payload secret-shaped input screen, before any other check.
	cfg := p.currentConfig()
	if hit := runtime.NewCredentialScreen(cfg).ScreenArguments(args, credentialsKnownArgs()); hit != nil {
		fields := hit.Fields
		if fields == nil {
			fields = []string{}
		}
		return screenedSummary(args, "secret-shaped input; arguments not stored", map[string]any{
			"offending_fields": fields, "unknown_offending_count": hit.UnknownCount,
		}), nil, nil, hit
	}
	recorded := recordableArgs(args)

	// 1. Operation.
	rawOp, present := args["operation"]
	op, isString := rawOp.(string)
	switch {
	case !present || rawOp == nil || (isString && op == ""):
		return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeMissingArgument, `missing required argument "operation"`, "operation"}
	case !isString:
		return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, `invalid argument "operation": must be a string`, "operation"}
	case !isCredentialsOperation(op):
		return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeUnknownOperation,
			"unknown operation; valid: " + strings.Join(credentialsOperations, ", "), "operation"}
	}

	// 2. Argument set and types: every argument is a string; unknown keys and
	// keys of another operation are refused, never named unless known.
	allowed := credentialsOpArgs[op]
	known := credentialsKnownArgs()
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "operation" {
			continue
		}
		if !allowed[k] {
			field := runtime.UnknownArgumentField
			msg := "invalid argument: unrecognised argument (its name is not echoed)"
			if known[k] {
				field = k
				msg = fmt.Sprintf("invalid argument %q: not accepted by %s", k, op)
			}
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, msg, field}
		}
		if _, ok := args[k].(string); !ok {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, fmt.Sprintf("invalid argument %q: must be a string", k), k}
		}
	}
	str := func(k string) (string, bool) {
		v, ok := args[k].(string)
		return v, ok
	}

	// 3. Live write gates (byte-equal texts to `profiles`).
	if credentialsMutatingOps[op] && cfg != nil {
		if cfg.ReadOnlyMode {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeReadOnlyMode, "Operation not allowed in read-only mode", ""}
		}
		if cfg.DisableManagement {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeManagementDisabled, "Server management is disabled for security", ""}
		}
	}

	rt := p.runtimeForProfiles()
	views := p.adminViews()
	delivery, _ := views.(credentialDeliveryViews)
	if rt == nil || rt.CredentialsService() == nil || views == nil || delivery == nil {
		return recorded, nil, nil, runtime.ErrCredentialsUnavailable
	}
	cs := rt.CredentialsService()
	clientsSupported := views.ClientsSupported()
	unsupported := &credentialsToolError{profile.CredentialErrorCodeUnsupportedEdition, "client credentials are not available in the server edition", "client"}
	actor := profilesActor(ctx)

	switch op {
	case "list":
		kind, _ := str("kind")
		prof, _ := str("profile")
		state, _ := str("state")
		if kind != "" && kind != "client" && kind != "token" && kind != "all" {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, `invalid argument "kind": must be client, token or all`, "kind"}
		}
		if state != "" && state != "active" && state != "expired" && state != "revoked" && state != "all" {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, `invalid argument "state": must be active, expired, revoked or all`, "state"}
		}
		rows, err := cs.List(runtime.CredentialFilter{Kind: kind, Profile: prof, State: state, ExcludeClients: !clientsSupported})
		if err != nil {
			return recorded, nil, nil, err
		}
		return recorded, map[string]any{"credentials": rows, "total": len(rows)}, nil, nil
	case "get", "revoke":
		client, hasClient := str("client")
		token, hasToken := str("token")
		if (hasClient && client != "") == (hasToken && token != "") {
			return recorded, nil, nil, &credentialsToolError{profile.CredentialErrorCodeInvalidArgument, op + ": exactly one of client or token is required", "client"}
		}
		if client != "" && !clientsSupported {
			return recorded, nil, nil, unsupported
		}
		ref := runtime.CredentialRef{Client: client, Token: token}
		if op == "get" {
			v, err := cs.Get(ref)
			if err != nil {
				return recorded, nil, nil, err
			}
			return recorded, map[string]any{"credential": v, "links": delivery.CredentialUILinks(client, token, v.Profile)}, nil, nil
		}
		res, err := cs.Revoke(ctx, actor, ref, false)
		if err != nil {
			return recorded, nil, nil, err
		}
		out := map[string]any{"credential": res.View, "changed": res.Changed}
		if client != "" {
			out["client_config_untouched"] = true
		}
		return recorded, out, nil, nil
	case "create_client":
		if !clientsSupported {
			return recorded, nil, nil, unsupported
		}
		id, _ := str("client")
		prof, hasProfile := str("profile")
		exp, _ := str("expires_in")
		display, _ := str("display_name")
		purpose, _ := str("purpose")
		var mode *string
		if m, ok := str("mode"); ok {
			mode = &m
		}
		res, err := cs.IssueClient(ctx, actor, runtime.IssueClientRequest{
			ID: id, DisplayName: display, Profile: prof, ProfilePresent: hasProfile, Mode: mode,
			ExpiresIn: exp, Expiry: runtime.ExpiryRequired, Purpose: purpose,
			RequireProfile: true, RefuseExistingRecord: true,
		})
		if err != nil {
			return recorded, nil, nil, err
		}
		result, audit := p.credentialDelivery("client", res, delivery, id, "")
		return recorded, result, audit, nil
	default: // create_token
		name, _ := str("name")
		prof, hasProfile := str("profile")
		exp, _ := str("expires_in")
		purpose, _ := str("purpose")
		res, err := cs.IssueToken(ctx, actor, runtime.IssueTokenRequest{
			Name: name, Profile: prof, ProfilePresent: hasProfile, ExpiresIn: exp, Expiry: runtime.ExpiryRequired,
			Purpose: purpose, RequireProfile: true, EnforceGuard: true,
		})
		if err != nil {
			return recorded, nil, nil, err
		}
		result, audit := p.credentialDelivery("token", res, delivery, "", name)
		return recorded, result, audit, nil
	}
}

// credentialDelivery builds the one-time delivery response (FR-004) and its
// server-built audit summary (FR-022): the audit body never derives from the
// delivered text.
func (p *MCPProxyServer) credentialDelivery(key string, res *runtime.IssuedCredentialResult, views credentialDeliveryViews, client, token string) (map[string]any, map[string]any) {
	endpoint := views.MCPEndpoint()
	links := views.CredentialUILinks(client, token, res.View.Profile)
	snippet := views.CredentialSnippet(res.Secret)
	result := map[string]any{
		key:          res.View,
		"credential": res.Secret,
		"snippet":    snippet,
		"delivery": map[string]any{
			"shown_once":       true,
			"endpoint":         endpoint,
			"header_name":      snippet.HeaderName,
			"alternate_header": "Authorization: Bearer <credential>",
			"install_note":     credentialInstallNote,
		},
		"links": links,
	}
	audit := map[string]any{
		key:          res.View,
		"credential": credentialRedactedMarker,
		"snippet":    credentialRedactedMarker,
		"delivery":   map[string]any{"shown_once": true, "endpoint": endpoint, "header_name": snippet.HeaderName},
		"links":      links,
	}
	return result, audit
}
