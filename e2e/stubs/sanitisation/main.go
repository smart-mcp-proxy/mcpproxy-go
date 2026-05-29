// Command sanitisation is a minimal stdio MCP server used by the Spec 054
// Track B output-sanitisation E2E verification. It exposes two tools whose
// content-trust classification (Spec 035, derived from openWorldHint) differs:
//
//   - leak_untrusted (openWorldHint=true  -> untrusted): returns text that
//     contains a (fake) AWS access key plus an ANSI escape and a bidi-override
//     control char. Used to verify spotlighting (default), redaction (opt-in),
//     control-sequence stripping (opt-in), and block-on-critical (opt-in).
//   - leak_trusted   (openWorldHint=false -> trusted):   returns the same text
//     but, being trusted, must be forwarded byte-identical under default config.
//
// Deterministic and dependency-light so the proxy's sanitisation behaviour can
// be asserted from curl/JSON-RPC.
package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The payload carries a fake-but-pattern-matching AWS key (critical), an ANSI
// SGR sequence, and a U+202E right-to-left override.
const leakyPayload = "result: AKIA1234567890ABCDEF \x1b[31mRED\x1b[0m end\u202ehidden"

func main() {
	s := server.NewMCPServer("sanitisation-stub", "1.0.0")

	untrusted := mcp.NewTool("leak_untrusted",
		mcp.WithDescription("Returns leaky text; open-world (untrusted) tool."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
	s.AddTool(untrusted, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(leakyPayload), nil
	})

	// leak_big returns a payload large enough to trip truncation + read_cache
	// caching, with the secret near the top so it survives the visible head.
	bigUntrusted := mcp.NewTool("leak_big",
		mcp.WithDescription("Returns a large leaky payload (untrusted) to exercise the read_cache path."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
	s.AddTool(bigUntrusted, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// A large JSON array so the truncator record-paginates it and writes the
		// FULL payload to read_cache. The secret is in the first record.
		var b strings.Builder
		b.WriteString(`[{"id":0,"data":"SECRET AKIA1234567890ABCDEF"}`)
		for i := 1; i < 800; i++ {
			b.WriteString(`,{"id":`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`,"data":"filler record to force truncation and read_cache storage"}`)
		}
		b.WriteString(`]`)
		return mcp.NewToolResultText(b.String()), nil
	})

	trusted := mcp.NewTool("leak_trusted",
		mcp.WithDescription("Returns the same leaky text; closed-world (trusted) tool."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	)
	s.AddTool(trusted, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(leakyPayload), nil
	})

	if err := server.ServeStdio(s); err != nil {
		panic(err)
	}
}
