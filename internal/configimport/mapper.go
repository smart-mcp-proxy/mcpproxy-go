package configimport

import (
	"fmt"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// MapToServerConfig converts a ParsedServer to MCPProxy's ServerConfig.
// Returns the mapped server config, list of skipped fields, and any warnings.
func MapToServerConfig(parsed *ParsedServer, now time.Time) (*config.ServerConfig, []string, []string) {
	server := &config.ServerConfig{
		Name:        parsed.Name,
		Enabled:     true,
		Quarantined: true, // Security: always quarantine imports
		Created:     now,
	}

	skipped := []string{}
	warnings := append([]string{}, parsed.Warnings...)

	// Map common fields
	if cmd, ok := parsed.Fields["command"].(string); ok && cmd != "" {
		server.Command = cmd
	}

	if args, ok := parsed.Fields["args"].([]string); ok {
		server.Args = args
	}

	if env, ok := parsed.Fields["env"].(map[string]string); ok && len(env) > 0 {
		server.Env = env
	}

	if url, ok := parsed.Fields["url"].(string); ok && url != "" {
		server.URL = url
	}

	if headers, ok := parsed.Fields["headers"].(map[string]string); ok && len(headers) > 0 {
		server.Headers = headers
	}

	if protocol, ok := parsed.Fields["protocol"].(string); ok && protocol != "" {
		server.Protocol = protocol
	}

	// Map working directory (cwd -> WorkingDir)
	if cwd, ok := parsed.Fields["cwd"].(string); ok && cwd != "" {
		server.WorkingDir = cwd
	}

	// Map enabled field (from Codex)
	if enabled, ok := parsed.Fields["enabled"].(bool); ok {
		server.Enabled = enabled
	}

	// Map OAuth configuration based on format
	switch parsed.SourceFormat {
	case FormatCursor:
		if oauth := mapCursorOAuth(parsed.Fields); oauth != nil {
			server.OAuth = oauth
			warnings = append(warnings, "OAuth credentials imported; you may need to reconfigure")
		}
	case FormatGemini:
		if oauth := mapGeminiOAuth(parsed.Fields); oauth != nil {
			// oauth.redirect_uri PINS the callback URL, so a genuinely malformed
			// value (non-loopback host, no explicit port, wrong scheme) copied
			// verbatim turns an importable server into a permanent connect
			// failure. A custom callback path alone (e.g. Gemini's typical
			// http://localhost:7777/oauth2callback) is fine - mcpproxy's own
			// callback server can serve whatever path the pin specifies (issue
			// #1304) - so only a value Validate() actually rejects gets dropped.
			if oauth.RedirectURI != "" {
				if err := oauth.Validate(); err != nil {
					warnings = append(warnings, fmt.Sprintf("oauth.redirect_uri %q is not usable by mcpproxy and was dropped (%v); mcpproxy will allocate its own callback URL", oauth.RedirectURI, err))
					oauth.RedirectURI = ""
				}
			}
			server.OAuth = oauth
			warnings = append(warnings, "OAuth credentials imported; you may need to reconfigure")
		}
	case FormatMCPProxy:
		// Already validated by ParseNativeServer: stored as written.
		if oauth, ok := parsed.Fields["oauth"].(*config.OAuthConfig); ok {
			server.OAuth = oauth
		}
	case FormatClaudeCode:
		oauth, oauthWarnings := mapClaudeCodeOAuth(parsed.Fields)
		warnings = append(warnings, oauthWarnings...)
		if oauth != nil {
			server.OAuth = oauth
			warnings = append(warnings, "OAuth credentials imported; you may need to reconfigure")
		}
	}

	// Track skipped fields
	skipped = append(skipped, getSkippedFields(parsed)...)

	return server, skipped, warnings
}

// mapCursorOAuth extracts OAuth config from Cursor's auth field.
func mapCursorOAuth(fields map[string]interface{}) *config.OAuthConfig {
	auth, ok := fields["auth"].(*CursorAuthConfig)
	if !ok || auth == nil {
		return nil
	}

	if auth.ClientID == "" {
		return nil
	}

	return &config.OAuthConfig{
		ClientID:     auth.ClientID,
		ClientSecret: auth.ClientSecret,
		Scopes:       auth.Scopes,
	}
}

// mapGeminiOAuth extracts OAuth config from Gemini's oauth field.
func mapGeminiOAuth(fields map[string]interface{}) *config.OAuthConfig {
	oauth, ok := fields["oauth"].(*GeminiOAuth)
	if !ok || oauth == nil {
		return nil
	}

	if !oauth.Enabled || oauth.ClientID == "" {
		return nil
	}

	return &config.OAuthConfig{
		ClientID:     oauth.ClientID,
		ClientSecret: oauth.ClientSecret,
		Scopes:       oauth.Scopes,
		RedirectURI:  oauth.RedirectURI,
	}
}

// mapClaudeCodeOAuth extracts OAuth config from Claude Code's oauth field, with
// a warning for everything that could not be carried over.
// Claude Code's dialect: scopes is a space-separated string and the auth
// server is named by authServerMetadataUrl. An empty client_id stays empty:
// mcpproxy falls back to DCR or public-client PKCE, which is what a server
// without a static client needs anyway.
func mapClaudeCodeOAuth(fields map[string]interface{}) (*config.OAuthConfig, []string) {
	src, ok := fields["oauth"].(*ClaudeCodeOAuthConfig)
	if !ok || src == nil {
		return nil, nil
	}

	var warnings []string
	if src.CallbackPort != 0 {
		// mcpproxy has no port-only callback pin; oauth.redirect_uri is the
		// full URL, so half a pin would fail validation.
		warnings = append(warnings, fmt.Sprintf("oauth.callbackPort %d has no mcpproxy equivalent and was dropped; set oauth.redirect_uri in config or the web UI if you need a fixed callback", src.CallbackPort))
	}

	oauth := &config.OAuthConfig{
		ClientID:              src.ClientID,
		Scopes:                strings.Fields(src.Scopes),
		AuthServerMetadataURL: src.AuthServerMetadataURL,
	}
	// The only value here that Validate can reject; its error does not echo it.
	if err := oauth.Validate(); err != nil {
		warnings = append(warnings, fmt.Sprintf("oauth.authServerMetadataUrl is not usable by mcpproxy and was dropped: %v", err))
		oauth.AuthServerMetadataURL = ""
	}
	if oauth.ClientID != "" {
		warnings = append(warnings, "Claude Code does not store the OAuth client secret in its config; set oauth.client_secret if this client has one")
	}

	if oauth.ClientID == "" && len(oauth.Scopes) == 0 && oauth.AuthServerMetadataURL == "" {
		return nil, warnings
	}
	return oauth, warnings
}

// getSkippedFields returns a list of fields that couldn't be mapped.
func getSkippedFields(parsed *ParsedServer) []string {
	skipped := []string{}

	// Check for format-specific unsupported fields
	switch parsed.SourceFormat {
	case FormatCursor:
		if _, ok := parsed.Fields["envFile"]; ok {
			skipped = append(skipped, "envFile")
		}

	case FormatCodex:
		if _, ok := parsed.Fields["enabled_tools"]; ok {
			skipped = append(skipped, "enabled_tools")
		}
		if _, ok := parsed.Fields["disabled_tools"]; ok {
			skipped = append(skipped, "disabled_tools")
		}

	case FormatMCPProxy:
		if keys, ok := parsed.Fields["skipped"].([]string); ok {
			skipped = append(skipped, keys...)
		}

	case FormatGemini:
		if _, ok := parsed.Fields["includeTools"]; ok {
			skipped = append(skipped, "includeTools")
		}
		if _, ok := parsed.Fields["excludeTools"]; ok {
			skipped = append(skipped, "excludeTools")
		}
	}

	return skipped
}
