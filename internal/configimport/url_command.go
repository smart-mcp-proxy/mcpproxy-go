package configimport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwords"
)

// URLParser parses a single pasted http(s):// URL into one remote server
// (Spec 109 FR-064). Preview never contacts the URL — Parse only validates
// its shape.
type URLParser struct{}

// Format implements Parser.
func (p *URLParser) Format() ConfigFormat { return FormatURL }

// Parse implements Parser.
func (p *URLParser) Parse(content []byte) ([]*ParsedServer, error) {
	raw := strings.TrimSpace(string(content))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, &ImportError{
			Type:    "invalid_url",
			Message: fmt.Sprintf("not a valid http(s) URL: %q", raw),
		}
	}

	return []*ParsedServer{{
		Name:         SuggestNameFromURL(u),
		SourceFormat: FormatURL,
		Fields: map[string]interface{}{
			"url":      raw,
			"protocol": "http",
		},
	}}, nil
}

// CommandParser parses a single pasted command line into one local/stdio
// server (Spec 109 FR-064), splitting it with the shared quote-aware
// tokenizer. Preview never executes anything — Parse only tokenizes text.
type CommandParser struct{}

// Format implements Parser.
func (p *CommandParser) Format() ConfigFormat { return FormatCommand }

// Parse implements Parser.
func (p *CommandParser) Parse(content []byte) ([]*ParsedServer, error) {
	raw := strings.TrimSpace(string(content))
	parts, err := shellwords.Split(raw)
	if err != nil {
		return nil, &ImportError{Type: "invalid_command", Message: fmt.Sprintf("could not parse command line: %v", err)}
	}
	if len(parts) == 0 {
		return nil, &ImportError{Type: "invalid_command", Message: "command line is empty"}
	}

	fields := map[string]interface{}{
		"command":  parts[0],
		"protocol": "stdio",
	}
	if len(parts) > 1 {
		fields["args"] = parts[1:]
	}

	return []*ParsedServer{{
		Name:         SuggestNameFromCommand(parts),
		SourceFormat: FormatCommand,
		Fields:       fields,
	}}, nil
}

// SuggestNameFromURL derives a display name from a parsed URL's host, e.g.
// "https://api.githubcopilot.com/mcp/" -> "githubcopilot". Exported so the
// CLI and httpapi preview can reuse it verbatim.
func SuggestNameFromURL(u *url.URL) string {
	host := strings.TrimPrefix(u.Hostname(), "www.")
	if host == "" {
		return "remote-server"
	}
	labels := strings.Split(host, ".")
	// A bare "api." / "mcp." subdomain is not a useful name — prefer the next
	// label (e.g. "api.githubcopilot.com" -> "githubcopilot", not "api").
	if len(labels) > 1 {
		switch labels[0] {
		case "api", "mcp", "www":
			return labels[1]
		}
	}
	return labels[0]
}

// runnerBinaries are package-runner commands whose first non-flag argument is
// a package/module name far more descriptive than the runner itself.
var runnerBinaries = map[string]bool{"npx": true, "uvx": true, "pipx": true}

// SuggestNameFromCommand derives a display name from a tokenized command
// line. For a package runner (npx/uvx/pipx) it prefers the package name over
// the runner binary, e.g. ["npx","-y","@modelcontextprotocol/server-filesystem"]
// -> "server-filesystem". Exported so the CLI and httpapi preview can reuse it
// verbatim.
func SuggestNameFromCommand(parts []string) string {
	if len(parts) == 0 {
		return "pasted-server"
	}
	base := parts[0]
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 && idx+1 < len(base) {
		base = base[idx+1:]
	}

	if runnerBinaries[base] {
		for _, a := range parts[1:] {
			if a == "" || strings.HasPrefix(a, "-") {
				continue
			}
			pkg := a
			if idx := strings.LastIndexByte(pkg, '/'); idx >= 0 && idx+1 < len(pkg) {
				pkg = pkg[idx+1:]
			}
			if idx := strings.IndexByte(pkg, '@'); idx > 0 {
				pkg = pkg[:idx]
			}
			return pkg
		}
	}
	return base
}
