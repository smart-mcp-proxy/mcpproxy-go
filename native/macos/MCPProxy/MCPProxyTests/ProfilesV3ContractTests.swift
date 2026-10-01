import XCTest
@testable import MCPProxy

/// Spec 108-k (T111, FR-050): the Swift models decode the SHARED contract
/// fixtures (`internal/profile/testdata/contract/*.json`, FR-052) that the Go
/// side, the Web UI and the CLI also read. This test only reads them; it never
/// edits a fixture.
final class ProfilesV3ContractTests: XCTestCase {

    private var fixtureDirectory: URL {
        // native/macos/MCPProxy/MCPProxyTests/<this file> → repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("internal/profile/testdata/contract")
    }

    private func fixture(_ name: String) throws -> Data {
        try Data(contentsOf: fixtureDirectory.appendingPathComponent(name + ".json"))
    }

    func testTheFixtureDirectoryIsWhereWeExpectIt() {
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixtureDirectory.path),
                      "fixtures moved? expected \(fixtureDirectory.path)")
    }

    func testProfileFullDecodesEveryPolicyField() throws {
        let p = try JSONDecoder().decode(ProfileView.self, from: fixture("profile_full"))
        XCTAssertEqual(p.name, "work-readonly")
        XCTAssertEqual(p.servers, ["github", "notion"])
        XCTAssertEqual(p.title, "Work · Read-only")
        XCTAssertEqual(p.maxTier, "read")
        XCTAssertEqual(p.unannotated, "deny")
        XCTAssertEqual(p.tools?.allow, ["notion:update_page", "github:get_secret_scanning_alert"])
        XCTAssertEqual(p.tools?.deny, ["github:*secret*"])
        XCTAssertEqual(p.tools?.classify, ["github:search_code": "read", "github:list_issues": "write"])
        XCTAssertEqual(p.codeExecution, false)
        XCTAssertEqual(p.managementTools, false)
        XCTAssertEqual(p.switchableTo, ["work-full"])
    }

    /// A pre-v3 profile sets none of the six policy fields, and each decodes as
    /// absent (never as a default value that would round-trip as a setting).
    func testProfileLegacySetsNoPolicyField() throws {
        let p = try JSONDecoder().decode(ProfileView.self, from: fixture("profile_legacy"))
        XCTAssertEqual(p.name, "legacy")
        XCTAssertNil(p.maxTier)
        XCTAssertNil(p.unannotated)
        XCTAssertNil(p.tools)
        XCTAssertNil(p.codeExecution)
        XCTAssertNil(p.managementTools)
        XCTAssertNil(p.switchableTo)
    }

    /// Saving a stored profile back is an identity on the wire (a custom
    /// `Encodable` that omits what is unset): the editor never invents a field.
    func testProfileFullRoundTripsThroughThePayload() throws {
        let data = try fixture("profile_full")
        let original = try JSONSerialization.jsonObject(with: data) as? NSDictionary
        let view = try JSONDecoder().decode(ProfileView.self, from: data)
        let encoded = try ProfileConfigPayload(view).jsonObject() as NSDictionary
        XCTAssertEqual(encoded, original)
    }

    func testProfileLegacyRoundTripsWithoutAddingFields() throws {
        let data = try fixture("profile_legacy")
        let original = try JSONSerialization.jsonObject(with: data) as? NSDictionary
        let view = try JSONDecoder().decode(ProfileView.self, from: data)
        XCTAssertEqual(try ProfileConfigPayload(view).jsonObject() as NSDictionary, original)
    }

    func testEffectiveToolsFixture() throws {
        let rows = try JSONDecoder().decode([EffectiveTool].self, from: fixture("effective_tools"))
        XCTAssertEqual(rows.count, 3)
        XCTAssertEqual(rows[0].fullName, "github:list_issues")
        XCTAssertTrue(rows[0].classificationStale)
        XCTAssertEqual(rows[1].access.reason, "above_tier_cap")
        XCTAssertFalse(rows[1].access.visible)
        XCTAssertEqual(rows[2].intrinsicTier, .unannotated)
        XCTAssertEqual(rows[2].profileTier, .read)
    }

    func testClientRowsFixture() throws {
        let rows = try JSONDecoder().decode([ClientPresenceRecord].self, from: fixtureWithRequiredRowFields("client_rows"))
        XCTAssertEqual(rows.count, 2)
        let cursor = rows[0]
        XCTAssertEqual(cursor.credentialState, .client)
        XCTAssertEqual(cursor.tokenName, "client-cursor")
        XCTAssertEqual(cursor.profile, "work-readonly")
        XCTAssertEqual(cursor.profileTitle, "Work · Read-only")
        XCTAssertEqual(cursor.profileMode, .locked)
        XCTAssertEqual(cursor.profileSource, "pin")
        XCTAssertEqual(cursor.profileMissing, false)
        XCTAssertEqual(cursor.expiresAt, "2027-09-25T00:00:00Z")
        XCTAssertEqual(cursor.blocked24h, 1)
        XCTAssertTrue(cursor.isLocked)
        XCTAssertEqual(rows[1].credentialState, .adminKey)
        XCTAssertFalse(rows[1].hasClientCredential)
    }

    /// The shared fixture carries the 108 ClientView fields only; the 109-h
    /// presence fields (`state`, `installed`, …) come from the base row, so the
    /// test supplies them without touching the fixture.
    private func fixtureWithRequiredRowFields(_ name: String) throws -> Data {
        var rows = try XCTUnwrap(JSONSerialization.jsonObject(with: fixture(name)) as? [[String: Any]])
        for index in rows.indices {
            rows[index]["state"] = "connected_seen"
            rows[index]["installed"] = true
            rows[index]["connected"] = true
            rows[index]["active_sessions"] = 0
            rows[index]["calls_24h"] = 0
        }
        return try JSONSerialization.data(withJSONObject: rows)
    }

    func testActivityAttributionFixtureDecodesIntoAnActivityEntry() throws {
        var record = try XCTUnwrap(JSONSerialization.jsonObject(with: fixture("activity_attributed")) as? [String: Any])
        record["id"] = "a1"
        record["type"] = "tool_call"
        record["status"] = "blocked"
        record["timestamp"] = "2026-09-29T09:00:00Z"
        let entry = try JSONDecoder().decode(ActivityEntry.self, from: JSONSerialization.data(withJSONObject: record))
        XCTAssertEqual(entry.profile, "work-readonly")
        XCTAssertEqual(entry.profileSource, "pin")
        XCTAssertEqual(entry.clientId, "cursor")
        XCTAssertEqual(entry.clientName, "Cursor")
        XCTAssertEqual(entry.tokenName, "client-cursor")
        XCTAssertEqual(entry.profileBlockReason, "profile_tier")
        XCTAssertEqual(entry.callerClientLabel, "cursor", "an id beats the advisory name")
        XCTAssertEqual(entry.callerProfileLabel, "work-readonly · locked by credential")
    }

    /// An advisory client NAME is never shown as an identity: it is marked "~".
    func testAdvisoryClientNameIsMarked() throws {
        let json = #"{"id":"a","type":"tool_call","status":"success","timestamp":"2026-09-29T09:00:00Z","client_name":"Zed"}"#
        let entry = try JSONDecoder().decode(ActivityEntry.self, from: Data(json.utf8))
        XCTAssertEqual(entry.callerClientLabel, "~Zed")
        XCTAssertNil(entry.callerProfileLabel)
    }

    /// The access-explanation fixture (F24 shape: `subject.kind`, shipped by
    /// 108-f and migrated on main by 108-i, I22). A fixture without `subject.kind`
    /// is a failure now, never a skip.
    func testExplainBlockedFixtureDecodesIntoAnAccessExplanation() throws {
        let data = try fixture("explain_blocked")
        let root = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let subject = try XCTUnwrap(root["subject"] as? [String: Any])
        XCTAssertNotNil(subject["kind"], "explain_blocked.json must carry the F24 subject shape")
        let explanation = try JSONDecoder().decode(AccessExplanation.self, from: data)
        XCTAssertEqual(explanation.subject, ExplainSubject(kind: "client", name: "cursor"))
        XCTAssertEqual(explanation.tool, "github:create_issue")
        XCTAssertEqual(explanation.profile, ExplainProfile(name: "work-readonly", source: "pin"))
        XCTAssertEqual(explanation.steps.map(\.step), [
            "credential", "profile", "server_in_scope", "tool_rule", "tier_cap",
            "token_permission", "global_gate", "server_state", "tool_approval",
        ])
        XCTAssertEqual(explanation.steps[4].status, .fail)
        XCTAssertEqual(explanation.steps[5].status, .skip)
        XCTAssertEqual(explanation.verdict, .hidden)
        XCTAssertEqual(explanation.firstFailure, "tier_cap")
        XCTAssertEqual(explanation.fixes.map(\.action), [.allowInProfile, .moveClient])
        XCTAssertEqual(explanation.fixes[0].label, "Allow github:create_issue in Work Read-only")
    }
}
