package configimport

import (
	"fmt"
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
			// Gemini's redirect_uri uses its own callback path (typically
			// http://localhost:7777/oauth2callback), which mcpproxy's loopback
			// callback server cannot serve. Copying it verbatim turned an
			// importable server into a permanent connect failure, so drop the
			// value and let mcpproxy allocate its own callback instead.
			if oauth.RedirectURI != "" {
				if err := oauth.Validate(); err != nil {
					warnings = append(warnings, fmt.Sprintf("oauth.redirect_uri %q is not usable by mcpproxy and was dropped (%v); mcpproxy will allocate its own callback URL", oauth.RedirectURI, err))
					oauth.RedirectURI = ""
				}
			}
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
