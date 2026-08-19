import Foundation

/// Canonical, user-facing project links (discussion #948). Kept in sync with the
/// Go (`internal/branding`) and Web UI (`frontend/src/config/links.ts`) copies.
///
/// Users arrive via Homebrew / plugins / AI recommendations and often never see
/// the GitHub repo, so the running app — the tray especially, its main surface —
/// must carry the way back to the project.
enum ProjectLinks {
    static let homepage = URL(string: "https://mcpproxy.app")!
    static let github = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go")!
    static let docs = URL(string: "https://docs.mcpproxy.app")!
    static let issues = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/issues")!
    static let discussions = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/discussions")!
}
