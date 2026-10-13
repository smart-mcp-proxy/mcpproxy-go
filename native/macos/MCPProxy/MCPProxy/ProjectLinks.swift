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

/// The credits block of the standard About panel.
///
/// A value rather than inline construction so the one property that matters —
/// that a running app carries a way to REPORT something, not just to read the
/// docs — is testable. Before the 2026-08 UX audit (F14) `ProjectLinks.issues`
/// and `.discussions` were declared and never used anywhere in the app, while
/// the Web UI had /feedback and the CLI had `mcpproxy feedback`.
enum AboutPanelLinks {
    struct Link: Equatable {
        let label: String
        let url: URL
    }

    static let all: [Link] = [
        Link(label: "Homepage", url: ProjectLinks.homepage),
        Link(label: "GitHub", url: ProjectLinks.github),
        Link(label: "Documentation", url: ProjectLinks.docs),
        Link(label: "Report an Issue", url: ProjectLinks.issues),
        Link(label: "Discussions", url: ProjectLinks.discussions),
    ]
}
