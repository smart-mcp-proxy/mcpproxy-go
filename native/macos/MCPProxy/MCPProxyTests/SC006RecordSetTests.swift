import XCTest
@testable import MCPProxy

/// Spec 108-l (SC-006, L8, T122): the macOS half of the cross-surface record-set
/// test. `internal/httpapi/testdata/sc006_activity_recordsets.json` holds, for one
/// seeded dataset, the query and the ids of every (profile, client, token,
/// status) combination (generated and checked against an independent reference
/// filter by `TestSC006ActivityRecordSets`). For each combination the native
/// filter opened from the combination's URL parameters must send exactly its
/// REST query, and the Activity list built from the server's answer must show
/// exactly its ids.
final class SC006RecordSetTests: XCTestCase {

    private struct RecordSet: Decodable {
        let urlQuery: String
        let restQuery: String
        let ids: [String]
        enum CodingKeys: String, CodingKey {
            case urlQuery = "url_query", restQuery = "rest_query", ids
        }
    }
    private struct Golden: Decodable { let recordsets: [RecordSet] }

    private func golden() throws -> Golden {
        // native/macos/MCPProxy/MCPProxyTests/<this file> → repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        let data = try Data(contentsOf: url.appendingPathComponent("internal/httpapi/testdata/sc006_activity_recordsets.json"))
        return try JSONDecoder().decode(Golden.self, from: data)
    }

    private static let scopeKeys = ["profile", "client", "token", "status"]

    /// "client=cursor&status=blocked" → ["client": "cursor", "status": "blocked"].
    private func parameters(_ query: String) -> [String: String] {
        var components = URLComponents()
        components.query = query
        var out: [String: String] = [:]
        for item in components.queryItems ?? [] { out[item.name] = item.value ?? "" }
        return out
    }

    /// The four SC-006 parameters of a request, as the canonical sorted query.
    private func canonical(_ request: ScopeRequest?) -> String {
        let pairs = (request?.query ?? [])
            .filter { Self.scopeKeys.contains($0.name) }
            .sorted { $0.name < $1.name }
        var components = URLComponents()
        components.queryItems = pairs
        // URLComponents leaves "-" alone, which is what the golden spells.
        return components.percentEncodedQuery ?? ""
    }

    private func entries(_ ids: [String], status: String) throws -> [ActivityEntry] {
        let records: [[String: Any]] = ids.enumerated().map { index, id in
            ["id": id, "type": "tool_call", "source": "mcp", "server_name": "srv",
             "tool_name": "tool_\(id)", "status": status,
             "timestamp": String(format: "2026-10-01T09:%02d:00Z", 59 - index)]
        }
        let data = try JSONSerialization.data(withJSONObject: records)
        return try JSONDecoder().decode([ActivityEntry].self, from: data)
    }

    func testTheGoldenHasTheFullMatrix() throws {
        let sets = try golden().recordsets
        XCTAssertEqual(sets.count, 64)
        XCTAssertGreaterThanOrEqual(Set(sets.map { $0.ids.joined(separator: ",") }).count, 12)
    }

    func testEveryCombinationSendsItsQueryAndShowsItsIds() throws {
        let now = Date(timeIntervalSince1970: 1_790_000_000)
        for set in try golden().recordsets {
            let filter = ScopeFilter(query: parameters(set.urlQuery))
            let request = filter.restRequest(for: .activity, scopeFiltersAvailable: true, now: now)
            XCTAssertEqual(request?.path, "/api/v1/activity", set.urlQuery)
            XCTAssertEqual(canonical(request), set.restQuery,
                           "macOS must send exactly the combination \(set.urlQuery.isEmpty ? "(no filter)" : set.urlQuery)")

            // The list the view builds from the server's answer: exactly the ids,
            // in the server's order, whatever the folding does to the rows.
            let status = parameters(set.restQuery)["status"] ?? "success"
            let list = try entries(set.ids, status: status)
            let shown = ActivityFolding.fold(list).flatMap { $0.members.map(\.id) }
            XCTAssertEqual(shown, set.ids, "macOS must show exactly the ids of \(set.urlQuery)")
        }
    }

    /// A native filter that has not been told the core advertises `scope_filters`
    /// sends none of the three scope parameters (hidden until available), which
    /// would silently widen every combination: the golden catches that too.
    func testTheComparisonCanFail() throws {
        let sets = try golden().recordsets
        guard let set = sets.first(where: { $0.restQuery == "client=cursor&status=blocked" }) else {
            return XCTFail("the golden lacks client=cursor&status=blocked")
        }
        let filter = ScopeFilter(query: parameters(set.urlQuery))
        let hidden = filter.restRequest(for: .activity, scopeFiltersAvailable: false, now: Date())
        XCTAssertNotEqual(canonical(hidden), set.restQuery, "without scope_filters the client parameter is not sent")
        XCTAssertEqual(canonical(hidden), "status=blocked")
        let other = sets.first { $0.restQuery == "client=cursor" }
        XCTAssertNotEqual(other?.ids, set.ids, "the golden tells the two combinations apart")
    }
}
