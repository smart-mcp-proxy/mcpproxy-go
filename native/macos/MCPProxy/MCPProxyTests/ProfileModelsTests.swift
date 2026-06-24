import XCTest
@testable import MCPProxy

/// Decoding tests for the Profiles v2 T5 tray switcher models. The JSON mirrors
/// the wire shape produced by the Go REST handlers `GET /api/v1/profiles` and
/// `GET|PUT /api/v1/profiles/active` (see internal/httpapi/profiles.go).
final class ProfileModelsTests: XCTestCase {

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(T.self, from: data)
    }

    func testDecodeProfilesListResponse() throws {
        let json = """
        {
            "profiles": [
                {"name": "research", "servers": ["research-srv"], "tool_count": 3},
                {"name": "deploy", "servers": ["deploy-srv", "ci-srv"], "tool_count": 5}
            ]
        }
        """
        let resp = try decode(ProfilesListResponse.self, from: json)
        XCTAssertEqual(resp.profiles.count, 2)
        XCTAssertEqual(resp.profiles[0].name, "research")
        XCTAssertEqual(resp.profiles[0].servers, ["research-srv"])
        XCTAssertEqual(resp.profiles[0].toolCount, 3)
        XCTAssertEqual(resp.profiles[1].toolCount, 5)
        // Identifiable id is the slug, so SwiftUI/menu keys stay stable.
        XCTAssertEqual(resp.profiles[1].id, "deploy")
    }

    func testDecodeActiveProfileResponse() throws {
        let set = try decode(ActiveProfileResponse.self, from: #"{"active_profile": "research"}"#)
        XCTAssertEqual(set.activeProfile, "research")

        // Empty string is the valid "all servers" sentinel, not nil.
        let cleared = try decode(ActiveProfileResponse.self, from: #"{"active_profile": ""}"#)
        XCTAssertEqual(cleared.activeProfile, "")
    }

    func testProfileSummaryEquatableDrivesMenuRefresh() throws {
        // AppState only repaints when the decoded profile set actually changes;
        // equality must be value-based for that guard to work.
        let a = ProfileSummary(name: "research", servers: ["s1"], toolCount: 3)
        let b = ProfileSummary(name: "research", servers: ["s1"], toolCount: 3)
        let c = ProfileSummary(name: "research", servers: ["s1"], toolCount: 4)
        XCTAssertEqual(a, b)
        XCTAssertNotEqual(a, c)
    }
}
