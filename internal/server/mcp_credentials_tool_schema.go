package server

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// The input schema of the `credentials` admin tool (Spec 115,
// contracts/mcp-credentials-tool.md). Descriptions are one line each: the
// schema is listed for every administrator session, so its size is
// tool-surface cost (SC-006).

const credentialsToolName = "credentials"

// credentialsOperations is the `operation` enum, in documentation order.
var credentialsOperations = []string{"list", "get", "create_client", "create_token", "revoke"}

// credentialsMutatingOps are refused under read_only_mode and disable_management.
var credentialsMutatingOps = map[string]bool{"create_client": true, "create_token": true, "revoke": true}

// credentialsArgs are every argument the tool knows, with its schema.
var credentialsArgs = []struct {
	name string
	desc string
	enum []string
}{
	{name: "client", desc: "create_client, get, revoke: custom client id (lower-case letters, digits, '-' or '_', at most 56 characters)."},
	{name: "token", desc: "get, revoke: agent token name."},
	{name: "name", desc: "create_token: new token name (letters, digits, '_' or '-'; not starting with client-)."},
	{name: "display_name", desc: "create_client: display name, at most 64 characters."},
	{name: "profile", desc: "create_client, create_token: existing profile to lock or pin to (required, never All servers). list: keep only this profile."},
	{name: "mode", enum: []string{"locked", "switchable"}, desc: "create_client: binding mode; omitted = locked."},
	{name: "expires_in", desc: "create_client, create_token: required lifetime such as 30m, 4h or 7d; at most 365d. At most 24h is shown as a task lease."},
	{name: "purpose", desc: "create_client, create_token: optional task brief and assumptions shown to the user, at most 500 characters; not enforced."},
	{name: "kind", enum: []string{"client", "token", "all"}, desc: "list: credential kind; omitted = all."},
	{name: "state", enum: []string{"active", "expired", "revoked", "all"}, desc: "list: credential state; omitted = all."},
}

// credentialsOpArgs lists the arguments each operation accepts (besides
// `operation`); any other known or unknown key is invalid_argument.
var credentialsOpArgs = map[string]map[string]bool{
	"list":          {"kind": true, "profile": true, "state": true},
	"get":           {"client": true, "token": true},
	"revoke":        {"client": true, "token": true},
	"create_client": {"client": true, "profile": true, "expires_in": true, "mode": true, "display_name": true, "purpose": true},
	"create_token":  {"name": true, "profile": true, "expires_in": true, "purpose": true},
}

// credentialsKnownArgs is every argument name the tool may echo in an error.
func credentialsKnownArgs() map[string]bool {
	known := map[string]bool{"operation": true}
	for _, a := range credentialsArgs {
		known[a.name] = true
	}
	return known
}

func credentialsToolOptions() []mcp.ToolOption {
	return []mcp.ToolOption{func(t *mcp.Tool) {
		if t.InputSchema.Properties == nil {
			t.InputSchema.Properties = map[string]any{}
		}
		t.InputSchema.Properties["operation"] = map[string]any{
			"type": "string", "enum": credentialsOperations, "description": "Operation to run.",
		}
		for _, a := range credentialsArgs {
			schema := map[string]any{"type": "string", "description": a.desc}
			if a.enum != nil {
				schema["enum"] = a.enum
			}
			t.InputSchema.Properties[a.name] = schema
		}
		t.InputSchema.Required = append(t.InputSchema.Required, "operation")
		t.InputSchema.AdditionalProperties = false
	}}
}

// buildCredentialsTool is the tool definition.
func buildCredentialsTool() mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("Administrator tool to issue, inspect and revoke worker credentials: custom client credentials (create_client) and profile-pinned agent tokens (create_token). " +
			"Every credential is bound to an existing profile with an explicit expiry; the secret is returned once, in the create result only, and never by list/get. " +
			"Writes are refused under read_only_mode or disable_management and by the binding guard (binding_bypassable_without_auth); errors are JSON {code, error, field?}."),
		mcp.WithTitleAnnotation("Issue and revoke worker credentials"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	}
	return mcp.NewTool(credentialsToolName, append(opts, credentialsToolOptions()...)...)
}
