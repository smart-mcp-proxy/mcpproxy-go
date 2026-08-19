// Package branding holds the canonical, user-facing project identifiers and
// URLs (homepage, repo, docs, issues) so every surface that points a user back
// to the project — CLI --version/--help, the MCP serverInfo instructions, docs
// links in errors — uses one source of truth. The macOS tray (Swift) and Web UI
// (TypeScript) keep their own copies of these strings; keep all three in sync.
//
// Added for discussion #948: users arriving via Homebrew / plugins / AI
// recommendations often never see the GitHub repo, so the running app must
// carry the way back to the project.
package branding

const (
	// ProductName is the single product name used everywhere.
	ProductName = "MCPProxy"

	// Homepage is the marketing/landing site.
	Homepage = "https://mcpproxy.app"

	// Repo is the GitHub source repository.
	Repo = "https://github.com/smart-mcp-proxy/mcpproxy-go"

	// Docs is the documentation site.
	Docs = "https://docs.mcpproxy.app"

	// Issues is the GitHub issue tracker (for "Report an issue").
	Issues = "https://github.com/smart-mcp-proxy/mcpproxy-go/issues"

	// Discussions is the GitHub Discussions board.
	Discussions = "https://github.com/smart-mcp-proxy/mcpproxy-go/discussions"
)
