import XCTest
@testable import MCPProxy

// Spec 109 FR-027 / FR-028 (T017): approval-state labels and tier labels must
// equal the terminology table in
// specs/109-ux-navigation-consistency/spec.md#terminology-binding-for-every-surface
// exactly — this is the Swift half of the cross-surface parity test (Web
// vitest asserts the same table from contracts.ts).
final class ToolLabelsTests: XCTestCase {
    func testApprovalStatusLabels() {
        XCTAssertEqual(ToolLabels.approvalStatusLabel("approved"), "Approved")
        XCTAssertEqual(ToolLabels.approvalStatusLabel("pending"), "New, needs review")
        XCTAssertEqual(ToolLabels.approvalStatusLabel("changed"), "Changed, needs review")
    }

    func testApprovalStatusLabelsDropTheOldWording() {
        for label in ["approved", "pending", "changed"].map(ToolLabels.approvalStatusLabel) {
            XCTAssertFalse(label.lowercased().contains("awaiting"), "\(label) still carries the retired 'awaiting' wording")
            XCTAssertFalse(label == "Pending Approval", "pending must not read as the old 'Pending Approval'")
        }
    }

    func testApprovalStatusLabelHandlesMissingOrUnknownGracefully() {
        XCTAssertEqual(ToolLabels.approvalStatusLabel(nil), "Unknown")
        XCTAssertEqual(ToolLabels.approvalStatusLabel(""), "")
    }

    func testTierLabels() {
        XCTAssertEqual(ToolLabels.tierLabel("read"), "Read")
        XCTAssertEqual(ToolLabels.tierLabel("write"), "Write")
        XCTAssertEqual(ToolLabels.tierLabel("destructive"), "Destructive")
        XCTAssertEqual(ToolLabels.tierLabel("unannotated"), "Unannotated")
        XCTAssertEqual(ToolLabels.tierLabel("unknown"), "Unknown")
    }

    func testTierLabelDefaultsToUnannotatedForMissingValue() {
        // A tool with no tier from an older core, or a nil field, is never
        // silently shown as "read" (X11: tier used to be computed as a
        // fallback-to-read locally — that is exactly the bug FR-028 removes).
        XCTAssertEqual(ToolLabels.tierLabel(nil), "Unannotated")
    }

    func testServerToolDecodesTierFromTheBackendPayload() throws {
        let json = """
        {"name": "delete_file", "description": "d", "server_name": "fs", "tier": "destructive", "approval_status": "approved"}
        """.data(using: .utf8)!
        let tool = try JSONDecoder().decode(ServerTool.self, from: json)
        XCTAssertEqual(tool.tier, "destructive")
    }

    func testSearchToolDecodesTierFromTheBackendPayload() throws {
        let json = """
        {"name": "read_file", "description": "d", "server_name": "fs", "tier": "read"}
        """.data(using: .utf8)!
        let tool = try JSONDecoder().decode(SearchTool.self, from: json)
        XCTAssertEqual(tool.tier, "read")
    }

    // Review round 1 (low): ToolsView.toolRow used to render the tier badge
    // only `if let tier = row.tier`, so a `nil` tier (an older core that does
    // not send the field yet — an explicitly supported skew, tray and core
    // update independently) rendered NO badge at all. The Web UI's equivalent
    // (`tool.tier || 'unannotated'`) always renders one, defaulting to
    // "Unannotated" — this is that same fallback made reachable from a real
    // row instead of only from ToolLabels.tierLabel(nil) in isolation.
    func testToolRowDisplayTierFallsBackToUnannotatedForAnOlderCore() {
        let row = ToolsView.ToolRow(server: "fs", name: "read_file", description: "d", score: nil, tier: nil)
        XCTAssertEqual(row.displayTier, "unannotated")
        XCTAssertEqual(ToolLabels.tierLabel(row.displayTier), "Unannotated")
    }

    func testToolRowDisplayTierPassesThroughARealTier() {
        let row = ToolsView.ToolRow(server: "fs", name: "delete_file", description: "d", score: nil, tier: "destructive")
        XCTAssertEqual(row.displayTier, "destructive")
    }
}
