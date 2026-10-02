import XCTest
@testable import MCPProxy

/// Spec 109-m (FR-091, T148b, M11): the macOS half of the parity-matrix check.
/// The macOS cells of specs/109-ux-navigation-consistency/parity-matrix.json
/// name sidebar destinations (`nav:`), toolbar "+" items (`add:`) and in-app
/// routes (`approute:`). They resolve here through the REAL Swift enums, the
/// only place the symbol table lives; `symbol:` and `a11y:` ids are resolved
/// against the source by TestSpec109ParityMatrixResolves (Go). A tick whose
/// destination does not exist therefore fails this test.
final class Spec109ParityMatrixTests: XCTestCase {

    private struct Cell: Decodable { let status: String; let ids: [String]; let reason: String? }
    private struct Row: Decodable { let row: String; let cells: [String: Cell] }
    private struct Matrix: Decodable { let rows: [Row] }

    private func loadMatrix() throws -> Matrix {
        // native/macos/MCPProxy/MCPProxyTests/<this file> -> repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        let data = try Data(contentsOf: url.appendingPathComponent("specs/109-ux-navigation-consistency/parity-matrix.json"))
        return try JSONDecoder().decode(Matrix.self, from: data)
    }

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
        let parts = id.split(separator: ":", maxSplits: 1).map(String.init)
        guard parts.count == 2 else { return "malformed identifier \(id)" }
        let (kind, name) = (parts[0], parts[1])
        switch kind {
        case "nav": return sidebarCases.contains(name) ? nil : "no SidebarItem case \(name)"
        case "add": return addMenuCases.contains(name) ? nil : "no AddMenuItem case \(name)"
        case "approute": return routeCases.contains(name) ? nil : "no AppRoute case \(name)"
        case "a11y", "symbol": return nil // resolved against source by the Go test
        default: return "kind \(kind) is not a macOS identifier"
        }
    }

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
        XCTAssertGreaterThanOrEqual(navigationIds, 8, "the walk saw too few navigation ids: the reader is broken")
    }

    func testEveryDashOnMacOSCarriesAReason() throws {
        let matrix = try loadMatrix()
        for row in matrix.rows {
            guard let cell = row.cells["macos"], cell.status == "out" else { continue }
            XCTAssertFalse((cell.reason ?? "").isEmpty, "row \(row.row): a macOS dash needs a reason")
        }
    }

    func testTheNamedDestinationsExist() throws {
        let matrix = try loadMatrix()
        func ids(_ row: String) -> [String] { matrix.rows.first { $0.row == row }?.cells["macos"]?.ids ?? [] }
        // Rows 1, 5, 9 and 24: Home, Review Queue, Clients and the grouped sidebar.
        XCTAssertTrue(ids("1").contains("nav:home"))
        XCTAssertTrue(ids("5").contains("nav:review"))
        XCTAssertTrue(ids("9").contains("nav:clients"))
        for item in ["nav:home", "nav:clients", "nav:review", "nav:activity"] {
            XCTAssertTrue(ids("24").contains(item), "row 24 lacks \(item)")
        }
        // Row 26: the toolbar "+" offers Server and Client.
        XCTAssertTrue(ids("26").contains("add:server") && ids("26").contains("add:client"))
        // Row 22a: there is no native Usage destination, by design.
        XCTAssertTrue(matrix.rows.first { $0.row == "22a" }?.cells["macos"]?.ids.isEmpty ?? false)
        XCTAssertFalse(sidebarCases.contains("usage"))
    }

    func testTheComparisonCanFail() {
        XCTAssertNotNil(unresolved("nav:nope"))
        XCTAssertNotNil(unresolved("add:nope"))
        XCTAssertNotNil(unresolved("approute:nope"))
        XCTAssertNotNil(unresolved("cmd:attention"), "a CLI id on the macOS surface")
        XCTAssertNil(unresolved("nav:home"))
        XCTAssertNil(unresolved("approute:reviewQueue"))
    }
}
