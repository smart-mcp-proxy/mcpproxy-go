import XCTest
@testable import MCPProxy

/// Decoding tests for the Profiles models (Spec 108-k K3). The JSON mirrors the
/// wire shape of `GET /api/v1/profiles` (internal/runtime/profiles_types.go).
final class ProfileModelsTests: XCTestCase {

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        try JSONDecoder().decode(T.self, from: Data(jsonString.utf8))
    }

    /// The pre-108 payload `{name, servers, tool_count}` still decodes: every
    /// v3 field is simply absent.
    func testAVersionTwoProfilePayloadStillDecodes() throws {
        let json = """
        {"profiles": [
            {"name": "research", "servers": ["research-srv"], "tool_count": 3},
            {"name": "deploy", "servers": ["deploy-srv", "ci-srv"], "tool_count": 5}
        ]}
        """
        let resp = try decode(ProfilesListResponse.self, from: json)
        XCTAssertEqual(resp.profiles.map(\.name), ["research", "deploy"])
        XCTAssertEqual(resp.profiles[0].servers, ["research-srv"])
        XCTAssertEqual(resp.profiles[0].toolCount, 3)
        XCTAssertEqual(resp.profiles[1].id, "deploy", "Identifiable id is the slug")
        XCTAssertNil(resp.profiles[0].usedBy, "used_by absent means not disclosed")
        XCTAssertNil(resp.profiles[0].toolCounts)
        XCTAssertNil(resp.profiles[0].switchableTo)
        XCTAssertNil(resp.anonymousProfile)
    }

    func testTheFullVersionThreePayloadDecodes() throws {
        let json = """
        {"profiles": [{
            "name": "work-readonly", "title": "Work · Read-only", "description": "d",
            "servers": ["github", "notion"], "max_tier": "read", "unannotated": "deny",
            "tools": {"allow": ["notion:update_page"], "deny": ["github:*secret*"], "classify": {"github:search_code": "read"}},
            "code_execution": false, "management_tools": false, "switchable_to": ["work-full"],
            "effective_servers": ["github", "notion"], "effective_unannotated": "deny",
            "effective_code_execution": false, "is_legacy": false,
            "tool_counts": {"read": 4, "write": 0, "destructive": 0, "unannotated_hidden": 2},
            "tool_count": 6, "calls_24h": 10, "blocked_24h": 1,
            "used_by": {"clients": [{"id": "cursor", "mode": "locked"}], "tokens": ["ci"], "anonymous_profile": false}
        }], "anonymous_profile": "work-readonly"}
        """
        let resp = try decode(ProfilesListResponse.self, from: json)
        let p = try XCTUnwrap(resp.profiles.first)
        XCTAssertEqual(p.displayTitle, "Work · Read-only")
        XCTAssertEqual(p.maxTier, "read")
        XCTAssertEqual(p.tools?.classify["github:search_code"], "read")
        XCTAssertEqual(p.toolCounts?.unannotatedHidden, 2)
        XCTAssertEqual(p.usedBy?.clients.first, UsedByClient(id: "cursor", mode: .locked))
        XCTAssertEqual(p.usedBy?.tokens, ["ci"])
        XCTAssertEqual(p.switchableTo, ["work-full"])
        XCTAssertEqual(p.codeExecution, false)
        XCTAssertEqual(resp.anonymousProfile, "work-readonly")
    }

    /// A value a newer core adds must not fail the whole response (R3).
    func testAnUnknownEnumValueDecodesIntoAFallbackCase() throws {
        let json = #"{"profiles": [{"name": "p", "used_by": {"clients": [{"id": "x", "mode": "quantum"}]}}]}"#
        let resp = try decode(ProfilesListResponse.self, from: json)
        XCTAssertEqual(resp.profiles[0].usedBy?.clients[0].mode, .unknown("quantum"))

        let tier = try decode(EffectiveTool.self, from: #"{"server":"s","tool":"t","intrinsic_tier":"exotic","profile_tier":"read","access":{"visible":true,"callable":true,"reason":""}}"#)
        XCTAssertEqual(tier.intrinsicTier, .unknown("exotic"))
        let state = try decode(CredentialState.self, from: #""rotating""#)
        XCTAssertEqual(state, .unknown("rotating"))
        XCTAssertFalse(state.canBind)
        XCTAssertEqual(try decode(CredentialState.self, from: #""admin_key""#), .adminKey)
    }

    func testProfileViewEquatableDrivesRefresh() throws {
        // AppState only repaints when the decoded profile set actually changes;
        // equality must be value-based for that guard to work.
        let a = ProfileView(name: "research", servers: ["s1"], toolCount: 3)
        let b = ProfileView(name: "research", servers: ["s1"], toolCount: 3)
        let c = ProfileView(name: "research", servers: ["s1"], toolCount: 4)
        XCTAssertEqual(a, b)
        XCTAssertNotEqual(a, c)
    }

    func testServersOnlyProfileIsLabelledFromIsLegacy() throws {
        let p = try decode(ProfileView.self, from: #"{"name":"legacy","servers":["github"],"is_legacy":true}"#)
        XCTAssertTrue(p.isServersOnly)
        XCTAssertEqual(p.maxTierLabel, "No cap")
    }

    func testClientRowsDecodeTheBindingFieldsAndOlderRowsStillDecode() throws {
        let json = """
        {"clients": [
          {"id":"cursor","display_name":"Cursor","kind":"supported","state":"connected_seen","installed":true,"connected":true,
           "active_sessions":0,"calls_24h":0,"credential_state":"client","token_name":"client-cursor","profile":"ro",
           "profile_title":"Read only","profile_mode":"locked","profile_source":"pin","expires_at":"2027-09-25T00:00:00Z","blocked_24h":2},
          {"id":"codex","display_name":"Codex","kind":"supported","state":"installed","installed":true,"connected":false,
           "active_sessions":0,"calls_24h":0}
        ], "warnings": [{"code":"client_holds_admin_key","severity":"warn","client_id":"codex","message":"m",
           "action":{"kind":"upgrade_admin_key_holders"}}]}
        """
        let resp = try decode(ClientsResponse.self, from: json)
        let cursor = resp.clients[0]
        XCTAssertTrue(cursor.hasClientCredential)
        XCTAssertTrue(cursor.isLocked)
        XCTAssertEqual(cursor.bindingLabel, "Read only · locked")
        XCTAssertEqual(cursor.blocked24h, 2)
        let codex = resp.clients[1]
        XCTAssertNil(codex.credentialState, "a pre-108 core sends no credential_state")
        XCTAssertFalse(codex.hasClientCredential)
        XCTAssertNil(codex.bindingLabel)
        XCTAssertEqual(resp.warnings?.first?.action?.kind, .upgradeAdminKeyHolders)
    }
}
