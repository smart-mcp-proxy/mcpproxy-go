import XCTest
@testable import MCPProxy

/// Round 2 (spec 109-b) made `.showServerDetail` carry a specific tab (e.g.
/// the Dashboard routing "Add secret" to `.config`), stored in ServersView's
/// `selectedServerInitialTab` @State. That state has no reset on the manual
/// double-click path: a review flagged that opening a server via the
/// notification (landing on `.config`), dismissing, and then double-clicking
/// an unrelated server row would silently reopen on the stale `.config` tab
/// instead of the default `.tools`, because nothing before `selectedServer =
/// server` in the double-click closure touched the tab state.
///
/// ServersView's @State is private and SwiftUI view internals aren't
/// reachable from XCTest here (no ViewInspector/snapshot harness in this
/// package), so this is a source-level regression guard rather than a
/// rendered-view behavioral test: it asserts the double-click closure resets
/// the tab before assigning `selectedServer`, which is what actually fixes
/// the bug.
@MainActor
final class ServersViewRoutingTests: XCTestCase {

    func testManualDoubleClickResetsTheDetailTabToTools() throws {
        let source = try serversViewSource()
        let onDoubleClick = try closureBody(labelled: "onDoubleClick: { server in", in: source)

        guard let resetRange = onDoubleClick.range(of: "selectedServerInitialTab = .tools"),
              let selectRange = onDoubleClick.range(of: "selectedServer = server") else {
            XCTFail("onDoubleClick must reset selectedServerInitialTab and assign selectedServer")
            return
        }
        XCTAssertTrue(resetRange.lowerBound < selectRange.lowerBound,
                      "onDoubleClick must reset selectedServerInitialTab to .tools BEFORE opening the server, " +
                      "or a prior .showServerDetail notification's tab (e.g. .config) leaks into the next manual open")
    }

    // MARK: - Helpers

    private func closureBody(labelled marker: String, in source: String) throws -> String {
        guard let start = source.range(of: marker) else {
            XCTFail("could not find `\(marker)` in ServersView.swift")
            return ""
        }
        // Bounded by the closure's own closing brace pattern used throughout
        // this call site (`},` on its own line ending the argument).
        guard let end = source.range(of: "\n                },", range: start.upperBound..<source.endIndex) else {
            XCTFail("could not find the end of the `\(marker)` closure in ServersView.swift")
            return ""
        }
        return String(source[start.upperBound..<end.lowerBound])
    }

    private func serversViewSource() throws -> String {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()   // MCPProxyTests
            .deletingLastPathComponent()   // package root
        let url = packageRoot.appendingPathComponent("MCPProxy/Views/ServersView.swift")
        XCTAssertTrue(FileManager.default.fileExists(atPath: url.path),
                      "missing source file at \(url.path)")
        return try String(contentsOf: url, encoding: .utf8)
    }
}
