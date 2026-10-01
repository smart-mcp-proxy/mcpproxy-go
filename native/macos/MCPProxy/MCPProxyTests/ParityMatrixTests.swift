import XCTest
@testable import MCPProxy

/// Spec 108-l (FR-051, L4, T121): the macOS half of the parity-matrix check.
/// The macOS cells of specs/108-profiles-v3/parity-matrix.json name sidebar
/// destinations (`nav:`), toolbar "+" items (`add:`) and in-app routes
/// (`approute:`). They resolve here through the REAL Swift enums, the only place
/// the symbol table lives; `a11y:` and `symbol:` ids are resolved against the
/// source by TestProfilesV3ParityMatrixResolves (Go). A tick whose destination
/// does not exist therefore fails this test (rows 11, 20 and 24 were the
/// round-4 regression cases).
final class ParityMatrixTests: XCTestCase {

    private struct Cell: Decodable { let status: String; let ids: [String] }
    private struct Row: Decodable { let row: Int; let cells: [String: Cell] }
    private struct Matrix: Decodable { let rows: [Row] }

    private func loadMatrix() throws -> Matrix {
        // native/macos/MCPProxy/MCPProxyTests/<this file> → repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        let data = try Data(contentsOf: url.appendingPathComponent("specs/108-profiles-v3/parity-matrix.json"))
        return try JSONDecoder().decode(Matrix.self, from: data)
    }

    // MARK: the symbol tables

    private var sidebarCases: Set<String> { Set(SidebarItem.allCases.map { "\($0)" }) }
    private var addMenuCases: Set<String> { Set(AddMenuItem.allCases.map { "\($0)" }) }

    /// An exhaustive switch: adding an AppRoute case without naming it here is a
    /// compile error, so the route table below cannot silently go stale.
    private func routeName(_ route: AppRoute) -> String {
        switch route {
        case .home: return "home"
        case .profiles: return "profiles"
        case .profileEditor: return "profileEditor"
        case .clients: return "clients"
        case .clientDetail: return "clientDetail"
        case .connectSheet: return "connectSheet"
        case .upgradeAdminKeys: return "upgradeAdminKeys"
        case .tools: return "tools"
        case .servers: return "servers"
        case .serverDetail: return "serverDetail"
        case .reviewQueue: return "reviewQueue"
        case .settings: return "settings"
        case .explain: return "explain"
        }
    }

    private var routeCases: Set<String> {
        let samples: [AppRoute] = [
            .home(filter: nil), .profiles, .profileEditor(name: "work", focusTool: nil),
            .clients(tab: .clients, filter: nil), .clientDetail(id: "cursor"),
            .connectSheet(clientId: nil), .upgradeAdminKeys, .tools(filter: nil),
            .servers(filter: nil), .serverDetail(name: "github"), .reviewQueue,
            .settings(.requireMCPAuth), .explain(subject: .anonymous, tool: nil),
        ]
        return Set(samples.map(routeName))
    }

    /// nil when the id resolves, else why not.
    private func unresolved(_ id: String) -> String? {
        guard let (kind, name) = id.split(separator: ":", maxSplits: 1).map(String.init).splitPair() else {
            return "malformed identifier \(id)"
        }
        switch kind {
        case "nav": return sidebarCases.contains(name) ? nil : "no SidebarItem case \(name)"
        case "add": return addMenuCases.contains(name) ? nil : "no AddMenuItem case \(name)"
        case "approute": return routeCases.contains(name) ? nil : "no AppRoute case \(name)"
        case "a11y", "symbol": return nil // resolved against source by the Go test
        default: return "kind \(kind) is not a macOS identifier"
        }
    }

    // MARK: tests

    func testEveryMacOSNavigationIdentifierResolves() throws {
        let matrix = try loadMatrix()
        var navigationIds = 0
        for row in matrix.rows {
            guard let cell = row.cells["macos"], cell.status == "must" else { continue }
            XCTAssertFalse(cell.ids.isEmpty, "row \(row.row): a tick with no macOS identifier")
            for id in cell.ids {
                if let why = unresolved(id) { XCTFail("row \(row.row): \(id): \(why)") }
                if id.hasPrefix("nav:") || id.hasPrefix("add:") || id.hasPrefix("approute:") { navigationIds += 1 }
            }
        }
        XCTAssertGreaterThanOrEqual(navigationIds, 6, "the walk saw too few navigation ids: the reader is broken")
    }

    func testTheRegressionRowsNameTheirDestinations() throws {
        let matrix = try loadMatrix()
        func ids(_ row: Int) -> [String] { matrix.rows.first { $0.row == row }?.cells["macos"]?.ids ?? [] }
        // Row 7: Clients list; row 8/9: picker and bulk move live on it.
        XCTAssertTrue(ids(7).contains("nav:clients"))
        // Row 20: deep links land on Clients, Tools and Servers through AppRoute.
        for route in ["approute:clients", "approute:tools", "approute:servers", "approute:home"] {
            XCTAssertTrue(ids(20).contains(route), "row 20 lacks \(route)")
        }
        // Row 1: the Profiles destination and its place in the sidebar.
        XCTAssertTrue(ids(1).contains("nav:profiles"))
        XCTAssertTrue(sidebarCases.contains("profiles") && sidebarCases.contains("clients"))
    }

    func testTheComparisonCanFail() {
        XCTAssertNotNil(unresolved("nav:nope"))
        XCTAssertNotNil(unresolved("add:nope"))
        XCTAssertNotNil(unresolved("approute:nope"))
        XCTAssertNotNil(unresolved("cmd:profile list"), "a CLI id on the macOS surface")
        XCTAssertNil(unresolved("nav:profiles"))
        XCTAssertNil(unresolved("approute:clients"))
        // The toolbar "+" menu is the same four items the Web "+ Add" menu offers.
        XCTAssertEqual(addMenuCases, ["server", "client", "token", "profile"])
    }
}

private extension Array where Element == String {
    func splitPair() -> (String, String)? { count == 2 ? (self[0], self[1]) : nil }
}
