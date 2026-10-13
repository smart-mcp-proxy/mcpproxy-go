import XCTest
@testable import MCPProxy

/// Spec 109-m (FR-090, T144, M3): the six terminology enums, read from the ONE
/// golden `internal/contracts/testdata/terminology.json` that Go
/// (`TestSpec109TerminologyGolden`) and vitest (`spec109-terminology.spec.ts`)
/// also decode. A value Go adds fails here until the native tables learn it;
/// a word that differs from the Web UI's fails here too.
final class Spec109TerminologyTests: XCTestCase {

    private struct Family: Decodable {
        let values: [String]
        let labels: [String: String]?
        let rank: [String: Int]?
        let cli: [String]?
    }

    private var goldenURL: URL {
        // native/macos/MCPProxy/MCPProxyTests/<this file> -> repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("internal/contracts/testdata/terminology.json")
    }

    private func family(_ name: String) throws -> Family {
        let data = try Data(contentsOf: goldenURL)
        let all = try JSONDecoder().decode([String: Family].self, from: data.strippingComment())
        return try XCTUnwrap(all[name], "terminology.json has no family \(name)")
    }

    func testHealthStatusLabelsEqualTheGolden() throws {
        let f = try family("health_status")
        XCTAssertEqual(HealthStatus.statusLabels, f.labels)
        XCTAssertEqual(Set(HealthStatus.statusLabels.keys), Set(f.values))
    }

    func testToolApprovalLabelsEqualTheGoldenAndRetiredNamesAreGone() throws {
        let f = try family("tool_approval")
        for value in f.values {
            XCTAssertEqual(ToolLabels.approvalStatusLabel(value), f.labels?[value], "approval \(value)")
        }
        for retired in ["Awaiting approval", "Pending Approval"] {
            XCTAssertFalse(f.values.map { ToolLabels.approvalStatusLabel($0) }.contains(retired))
        }
    }

    func testTierLabelsEqualTheGolden() throws {
        let f = try family("tier")
        for value in f.values {
            XCTAssertEqual(ToolLabels.tierLabel(value), f.labels?[value], "tier \(value)")
        }
        // The tolerant tier enum knows every value Go emits except `unknown`,
        // which only the review composer produces (it decodes as the raw string).
        for value in f.values where value != "unknown" {
            let tier = ToolTier(wire: value)
            XCTAssertEqual(tier.wire, value)
            XCTAssertEqual(tier.label, f.labels?[value], "ToolTier.label for \(value)")
        }
    }

    func testActivityViewsEqualTheGolden() throws {
        let f = try family("activity_view")
        XCTAssertEqual(ActivityViewMode.allCases.map(\.rawValue), f.values)
        for mode in ActivityViewMode.allCases {
            XCTAssertEqual(mode.label, f.labels?[mode.rawValue], "view \(mode.rawValue)")
        }
        XCTAssertEqual(f.cli, f.values.filter { $0 != "sessions" })
    }

    /// macOS words for each presence state. A value Go adds fails the key-set
    /// check below until a label is chosen here and in `stateLabel`.
    private let presenceLabels: [String: String] = [
        "connected_seen": "Connected",
        "connected_never_seen": "Connected — awaiting first use",
        "installed": "Installed",
        "not_installed": "Not installed",
        "other": "Observed",
    ]

    func testClientPresenceStatesEachHaveADedicatedLabel() throws {
        let f = try family("client_presence")
        XCTAssertEqual(Set(presenceLabels.keys), Set(f.values), "the macOS presence label table must cover exactly the Go states")
        for value in f.values {
            let json = """
            {"id":"x","display_name":"X","kind":"supported","state":"\(value)","installed":false,
             "connected":false,"active_sessions":0,"calls_24h":0}
            """
            let record = try JSONDecoder().decode(ClientPresenceRecord.self, from: Data(json.utf8))
            XCTAssertEqual(record.state, value)
            XCTAssertEqual(record.stateLabel, presenceLabels[value], "presence \(value)")
        }
    }

    func testAttentionKindsDecodeAndKeepTheirRank() throws {
        let f = try family("attention_kind")
        let ranks = try XCTUnwrap(f.rank)
        for kind in f.values {
            let json = """
            {"id":"\(kind):server:x","kind":"\(kind)","rank":\(ranks[kind] ?? -1),
             "subject":{"type":"server","id":"x","name":"x"},"summary":"s",
             "fix":{"verb":"review","label":"Review","target":"/review"},
             "since":"2026-10-02T10:00:00Z"}
            """
            let item = try JSONDecoder().decode(AttentionItem.self, from: Data(json.utf8))
            XCTAssertEqual(item.kind, kind)
            XCTAssertEqual(item.rank, ranks[kind])
        }
        let ordered = f.values.compactMap { ranks[$0] }
        XCTAssertEqual(ordered, ordered.sorted(), "kinds are listed in rank order")
    }
}

private extension Data {
    /// terminology.json carries a top-level `_comment` string next to the
    /// families; drop it so the file decodes as [String: Family].
    func strippingComment() throws -> Data {
        guard var object = try JSONSerialization.jsonObject(with: self) as? [String: Any] else { return self }
        object.removeValue(forKey: "_comment")
        return try JSONSerialization.data(withJSONObject: object)
    }
}
