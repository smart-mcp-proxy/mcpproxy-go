import XCTest
@testable import MCPProxy

/// Covers the pure deep-link URL builder the tray menu relies on. The
/// "Install server…" action (MCP-37b) opens the Web UI marketplace at
/// `<base>/ui/repositories`; these assertions pin that path plus the existing
/// deep-link patterns ("Open Web UI", server approve, sensitive activity) so a
/// future change to `webUIURL` can't silently break any of them.
final class TrayMenuDeepLinkTests: XCTestCase {

    private let base = "http://127.0.0.1:8080"

    func testInstallServerDeepLink() {
        XCTAssertEqual(
            TrayMenu.webUIURL(base: base, path: "repositories")?.absoluteString,
            "http://127.0.0.1:8080/ui/repositories"
        )
    }

    func testOpenWebUIRootDeepLink() {
        XCTAssertEqual(
            TrayMenu.webUIURL(base: base)?.absoluteString,
            "http://127.0.0.1:8080/ui/"
        )
    }

    func testServerAndActivityDeepLinks() {
        XCTAssertEqual(
            TrayMenu.webUIURL(base: base, path: "servers/github")?.absoluteString,
            "http://127.0.0.1:8080/ui/servers/github"
        )
        XCTAssertEqual(
            TrayMenu.webUIURL(base: base, path: "activity?sensitive=true")?.absoluteString,
            "http://127.0.0.1:8080/ui/activity?sensitive=true"
        )
    }
}
