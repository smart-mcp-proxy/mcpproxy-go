package configimport

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrUnknownFormat is returned when the configuration format cannot be detected.
var ErrUnknownFormat = fmt.Errorf("unable to detect configuration format: supported formats are Claude Desktop, Claude Code, Cursor IDE, Codex CLI, Gemini CLI, a pasted URL, or a pasted command line")

// DetectFormat identifies the configuration format from content.
// It tries TOML first (for Codex), then JSON (for all other formats), then
// falls back to the Paste-source formats (FR-064): a single http(s):// URL,
// or a single command line.
//
// This fallback must stay opt-in at the Import() call site (see
// ImportOptions.AllowPasteFallback / DetectFormatStrict) — DetectFormat
// itself keeps trying it unconditionally so its own tests, and the
// interactive Paste tab (the one surface that previews the guess for a
// human to confirm before Add ever mutates anything) can call it directly.
func DetectFormat(content []byte) (*DetectionResult, error) {
	// Try TOML first (Codex uses TOML)
	if result := tryDetectTOML(content); result != nil {
		return result, nil
	}

	// Try JSON (all other formats)
	if result := tryDetectJSON(content); result != nil {
		return result, nil
	}

	// Try the Paste-source fallbacks (FR-064): only single-line input is a
	// candidate — anything spanning multiple lines is neither a URL nor a
	// command line MCPProxy would know how to preview, so it stays unknown
	// rather than guessing.
	if result := tryDetectURLOrCommand(content); result != nil {
		return result, nil
	}

	return nil, ErrUnknownFormat
}

// DetectFormatStrict identifies the configuration format from content,
// trying only TOML and JSON — it never guesses the Paste-tab URL/single-line
// -command fallback DetectFormat does (review round 4 F-E). Import() uses
// this by default and only switches to the full DetectFormat when the
// caller sets ImportOptions.AllowPasteFallback.
//
// Without this split, every import surface — the CLI's `upstream import
// <file>`, a direct REST import call, or the general "Import config" panel —
// shared the exact same guess the interactive Paste tab relies on, but
// without that tab's preview-before-Add step: a plain one-line file that is
// neither JSON nor TOML (a typo, or simply the wrong file) used to return a
// clear "unable to detect configuration format" error; with the fallback
// unconditionally wired in, it was instead silently guessed as a stdio
// command line and — on a non-preview call — added as a real, running
// (quarantined) server with no confirmation step at all.
func DetectFormatStrict(content []byte) (*DetectionResult, error) {
	if result := tryDetectTOML(content); result != nil {
		return result, nil
	}
	if result := tryDetectJSON(content); result != nil {
		return result, nil
	}
	return nil, ErrUnknownFormat
}

// tryDetectURLOrCommand recognises the two Paste-source formats: a bare
// http(s):// URL, or any other single non-empty line (treated as a command
// line — CommandParser is responsible for actually tokenizing it, and rejects
// nonsense at parse time rather than here).
func tryDetectURLOrCommand(content []byte) *DetectionResult {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" || strings.ContainsAny(trimmed, "\n\r") {
		return nil
	}
	// Content that opens like JSON (an object or array) was meant as a
	// config paste, not a shell command — even when it fails to parse (e.g.
	// a typo) or parses but lacks mcpServers. Falling back to "command" here
	// would silently swallow the real error into a nonsense command preview.
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return nil
	}

	if u, err := url.Parse(trimmed); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return &DetectionResult{
			Format:     FormatURL,
			Confidence: "high",
			Indicators: []string{"single_line", "http_scheme"},
		}
	}

	return &DetectionResult{
		Format:     FormatCommand,
		Confidence: "medium",
		Indicators: []string{"single_line", "not_json_or_toml"},
	}
}

// tryDetectTOML attempts to parse content as TOML and detect Codex format.
func tryDetectTOML(content []byte) *DetectionResult {
	var raw map[string]interface{}
	if _, err := toml.Decode(string(content), &raw); err != nil {
		return nil
	}

	// Check for Codex mcp_servers key (underscore, not camelCase)
	if _, ok := raw["mcp_servers"]; ok {
		return &DetectionResult{
			Format:     FormatCodex,
			Confidence: "high",
			Indicators: []string{"toml_format", "mcp_servers_key"},
		}
	}

	return nil
}

// tryDetectJSON attempts to parse content as JSON and detect specific format.
func tryDetectJSON(content []byte) *DetectionResult {
	var raw map[string]interface{}
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil
	}

	// Must have mcpServers key
	servers, ok := raw["mcpServers"]
	if !ok {
		return nil
	}

	serversMap, ok := servers.(map[string]interface{})
	if !ok {
		return nil
	}

	// Check for Claude Desktop indicator: globalShortcut
	if _, hasGlobalShortcut := raw["globalShortcut"]; hasGlobalShortcut {
		return &DetectionResult{
			Format:     FormatClaudeDesktop,
			Confidence: "high",
			Indicators: []string{"json_format", "mcpServers_key", "globalShortcut_key"},
		}
	}

	// Check server-level indicators
	for _, server := range serversMap {
		serverMap, ok := server.(map[string]interface{})
		if !ok {
			continue
		}

		// Gemini: has httpUrl field
		if _, hasHTTPUrl := serverMap["httpUrl"]; hasHTTPUrl {
			return &DetectionResult{
				Format:     FormatGemini,
				Confidence: "high",
				Indicators: []string{"json_format", "mcpServers_key", "httpUrl_field"},
			}
		}

		// Check type field for format-specific values
		if serverType, hasType := serverMap["type"].(string); hasType {
			// Claude Code: has websocket type
			if serverType == "websocket" {
				return &DetectionResult{
					Format:     FormatClaudeCode,
					Confidence: "high",
					Indicators: []string{"json_format", "mcpServers_key", "type_websocket"},
				}
			}

			// Cursor: has streamable-http or streamableHttp type
			if serverType == "streamable-http" || serverType == "streamableHttp" {
				return &DetectionResult{
					Format:     FormatCursor,
					Confidence: "high",
					Indicators: []string{"json_format", "mcpServers_key", "type_streamable_http"},
				}
			}
		}

		// Gemini: has trust field (Gemini-specific)
		if _, hasTrust := serverMap["trust"]; hasTrust {
			return &DetectionResult{
				Format:     FormatGemini,
				Confidence: "medium",
				Indicators: []string{"json_format", "mcpServers_key", "trust_field"},
			}
		}

		// Gemini: has includeTools or excludeTools (Gemini-specific)
		if _, hasIncludeTools := serverMap["includeTools"]; hasIncludeTools {
			return &DetectionResult{
				Format:     FormatGemini,
				Confidence: "medium",
				Indicators: []string{"json_format", "mcpServers_key", "includeTools_field"},
			}
		}
		if _, hasExcludeTools := serverMap["excludeTools"]; hasExcludeTools {
			return &DetectionResult{
				Format:     FormatGemini,
				Confidence: "medium",
				Indicators: []string{"json_format", "mcpServers_key", "excludeTools_field"},
			}
		}

		// Cursor: has auth field with CLIENT_ID
		if auth, hasAuth := serverMap["auth"].(map[string]interface{}); hasAuth {
			if _, hasClientID := auth["CLIENT_ID"]; hasClientID {
				return &DetectionResult{
					Format:     FormatCursor,
					Confidence: "high",
					Indicators: []string{"json_format", "mcpServers_key", "auth_CLIENT_ID"},
				}
			}
		}

		// Cursor: has envFile field
		if _, hasEnvFile := serverMap["envFile"]; hasEnvFile {
			return &DetectionResult{
				Format:     FormatCursor,
				Confidence: "medium",
				Indicators: []string{"json_format", "mcpServers_key", "envFile_field"},
			}
		}
	}

	// Check for Claude Code specific type values that we might have missed
	for _, server := range serversMap {
		serverMap, ok := server.(map[string]interface{})
		if !ok {
			continue
		}

		if serverType, hasType := serverMap["type"].(string); hasType {
			// Claude Code supports: stdio, http, sse, websocket
			// Cursor supports: sse, streamable-http
			// If we see http (not sse, not streamable-http), likely Claude Code
			if serverType == "http" {
				return &DetectionResult{
					Format:     FormatClaudeCode,
					Confidence: "medium",
					Indicators: []string{"json_format", "mcpServers_key", "type_http"},
				}
			}
		}
	}

	// Check for Gemini global mcp config
	if _, hasMCPGlobal := raw["mcp"]; hasMCPGlobal {
		return &DetectionResult{
			Format:     FormatGemini,
			Confidence: "medium",
			Indicators: []string{"json_format", "mcpServers_key", "mcp_global_config"},
		}
	}

	// Default fallback: Cursor IDE is the most generic JSON format
	// Claude Desktop requires command-only servers (no url), check for that
	allStdio := true
	for _, server := range serversMap {
		serverMap, ok := server.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasURL := serverMap["url"]; hasURL {
			allStdio = false
			break
		}
		if _, hasType := serverMap["type"]; hasType {
			allStdio = false
			break
		}
	}

	if allStdio {
		// All servers are stdio-only (command, args, env) - likely Claude Desktop
		return &DetectionResult{
			Format:     FormatClaudeDesktop,
			Confidence: "medium",
			Indicators: []string{"json_format", "mcpServers_key", "all_stdio_servers"},
		}
	}

	// Fallback to Cursor (most generic)
	return &DetectionResult{
		Format:     FormatCursor,
		Confidence: "low",
		Indicators: []string{"json_format", "mcpServers_key", "generic_fallback"},
	}
}
