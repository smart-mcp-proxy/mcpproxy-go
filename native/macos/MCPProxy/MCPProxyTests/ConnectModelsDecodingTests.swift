import XCTest
@testable import MCPProxy

/// Wire-format tests for the Connect Client form's models (spec 091 T012).
///
/// Every fixture here is JSON the core actually emits — synthesized, never
/// fetched — so the whole Swift track runs without a live core.
final class ConnectModelsDecodingTests: XCTestCase {

    private func decodeStatus(_ json: String) throws -> APIClient.ClientStatus {
        try JSONDecoder().decode(APIClient.ClientStatus.self, from: Data(json.utf8))
    }

    private func decodePreview(_ json: String) throws -> ConnectPreviewModel {
        try JSONDecoder().decode(ConnectPreviewModel.self, from: Data(json.utf8))
    }

    // MARK: - ClientStatus (extended)

    func testClientStatusDecodesTheExtendedFields() throws {
        let status = try decodeStatus("""
        {"id":"claude-code","name":"Claude Code",
         "config_path":"/Users/x/.claude.json","exists":true,"connected":true,
         "supported":true,"icon":"claude-code","server_name":"mcpproxy",
         "access_state":"accessible"}
        """)

        XCTAssertEqual(status.clientId, "claude-code")
        XCTAssertEqual(status.icon, "claude-code")
        XCTAssertEqual(status.serverName, "mcpproxy")
        XCTAssertEqual(status.accessState, .accessible)
        XCTAssertNil(status.remediation)
    }

    func testClientStatusDecodesDeniedAccessWithRemediation() throws {
        let status = try decodeStatus("""
        {"id":"cursor","name":"Cursor","config_path":"/Users/x/.cursor/mcp.json",
         "exists":true,"connected":false,"supported":true,"icon":"cursor",
         "access_state":"denied",
         "remediation":"Grant MCPProxy access to App Data in System Settings › Privacy & Security."}
        """)

        XCTAssertEqual(status.accessState, .denied)
        XCTAssertEqual(
            status.remediation,
            "Grant MCPProxy access to App Data in System Settings › Privacy & Security."
        )
    }

    /// A core older than spec 091 sends none of the four additive fields; the
    /// row must still decode rather than blanking the whole list.
    func testClientStatusToleratesACoreWithoutTheExtendedFields() throws {
        let status = try decodeStatus("""
        {"id":"gemini","name":"Gemini CLI","config_path":"/Users/x/.gemini/settings.json",
         "exists":false,"connected":false,"supported":true}
        """)

        XCTAssertNil(status.icon)
        XCTAssertNil(status.serverName)
        XCTAssertNil(status.accessState)
        // A known client whose old core sent no `icon` still resolves its symbol
        // through the client id; only a genuinely unknown identity falls all the
        // way through to the generic symbol (asserted below).
        XCTAssertEqual(status.symbolName, "sparkles")
    }

    /// A core newer than the app can report a client this build has never heard
    /// of. It renders by name with the default icon and stays fully functional
    /// (FR-009), so nothing about the decode may be id-conditional.
    func testUnknownClientIDDecodesGenerically() throws {
        let status = try decodeStatus("""
        {"id":"brand-new-client","name":"Brand New Client",
         "config_path":"/Users/x/.brandnew/config.json","exists":true,
         "connected":false,"supported":true,"icon":"brand-new-client",
         "access_state":"accessible"}
        """)

        XCTAssertEqual(status.displayName, "Brand New Client")
        XCTAssertEqual(status.symbolName, "app.connected.to.app.below.fill")
        XCTAssertTrue(status.supported)
    }

    /// An unrecognised access_state string must not fail the decode.
    func testUnknownAccessStateDecodesAsUnknown() throws {
        let status = try decodeStatus("""
        {"id":"codex","name":"Codex","config_path":"/x","exists":true,
         "connected":false,"supported":true,"access_state":"future-state"}
        """)

        XCTAssertEqual(status.accessState, .unknown)
    }

    // MARK: - ConnectPreviewModel — the three new fields

    private static let replacePreviewJSON = """
    {"client":"claude-code","config_path":"/Users/x/.claude.json","format":"json",
     "server_key":"mcpServers","server_name":"mcpproxy",
     "entry":{"type":"http","url":"http://127.0.0.1:8080/mcp"},
     "entry_text":"\\"mcpproxy\\": {\\n  \\"type\\": \\"http\\"\\n}",
     "entry_exists":true,"contains_api_key":true,"access_state":"accessible",
     "existing_entry_summary":{"entry_name":"old-proxy","type":"http",
       "endpoint":"http://127.0.0.1:9090/mcp","command":null,
       "header_names":["X-API-Key"],"env_names":[]},
     "precondition_token":"9f2c…opaque","connect_refusal":null}
    """

    func testPreviewDecodesTheThreeNewFields() throws {
        let preview = try decodePreview(Self.replacePreviewJSON)

        XCTAssertEqual(preview.configPath, "/Users/x/.claude.json")
        XCTAssertEqual(preview.serverName, "mcpproxy")
        XCTAssertTrue(preview.entryExists)
        XCTAssertTrue(preview.containsAPIKey)
        XCTAssertEqual(preview.accessState, .accessible)
        XCTAssertEqual(preview.preconditionToken, "9f2c…opaque")
        XCTAssertNil(preview.connectRefusal)

        let summary = try XCTUnwrap(preview.existingEntrySummary)
        XCTAssertEqual(summary.entryName, "old-proxy")
        XCTAssertEqual(summary.type, "http")
        XCTAssertEqual(summary.endpoint, "http://127.0.0.1:9090/mcp")
        XCTAssertNil(summary.command)
        XCTAssertEqual(summary.headerNames, ["X-API-Key"])
        XCTAssertEqual(summary.envNames, [])
    }

    /// Go omits empty slices as `null` (or drops them); neither may produce nil
    /// arrays the view would have to special-case.
    func testSummaryTreatsMissingNameArraysAsEmpty() throws {
        let preview = try decodePreview("""
        {"config_path":"/x","server_name":"mcpproxy","entry_text":"…",
         "entry_exists":true,"contains_api_key":false,"access_state":"accessible",
         "existing_entry_summary":{"entry_name":"mcpproxy","type":"stdio",
           "command":"/usr/local/bin/mcpproxy"},
         "precondition_token":"tok"}
        """)

        let summary = try XCTUnwrap(preview.existingEntrySummary)
        XCTAssertEqual(summary.headerNames, [])
        XCTAssertEqual(summary.envNames, [])
        XCTAssertEqual(summary.command, "/usr/local/bin/mcpproxy")
        XCTAssertNil(summary.endpoint)
    }

    /// A pre-091 core sends no token, no summary and no refusal. Decoding must
    /// tolerate that; the model decides separately what it may still write.
    func testPreviewToleratesALegacyCoreWithoutTheNewFields() throws {
        let preview = try decodePreview("""
        {"client":"cursor","config_path":"/x","server_name":"mcpproxy",
         "entry_text":"…","entry_exists":false,"contains_api_key":false,
         "access_state":"accessible"}
        """)

        XCTAssertNil(preview.preconditionToken)
        XCTAssertNil(preview.existingEntrySummary)
        XCTAssertNil(preview.connectRefusal)
        XCTAssertEqual(preview.changeKind, .add)
    }

    // MARK: - changeKind derivation (all six cases)

    private func preview(
        entryExists: Bool = false,
        accessState: String = "accessible",
        refusal: String? = nil,
        summaryName: String? = nil,
        containsAPIKey: Bool = false
    ) throws -> ConnectPreviewModel {
        let summary = summaryName.map {
            """
            ,"existing_entry_summary":{"entry_name":"\($0)","type":"http",
              "endpoint":"http://127.0.0.1:9090/mcp","header_names":[],"env_names":[]}
            """
        } ?? ""
        let refusalField = refusal.map { ",\"connect_refusal\":\"\($0)\"" } ?? ""
        return try decodePreview("""
        {"client":"c","config_path":"/x","server_name":"mcpproxy","entry_text":"…",
         "entry_exists":\(entryExists),"contains_api_key":\(containsAPIKey),
         "access_state":"\(accessState)","precondition_token":"tok"\(summary)\(refusalField)}
        """)
    }

    func testRefusalOutranksEveryOtherClassification() throws {
        let refused = try preview(
            accessState: "absent",
            refusal: "opencode requires an existing config file; create one first"
        )

        XCTAssertEqual(
            refused.changeKind,
            .refused("opencode requires an existing config file; create one first")
        )
        XCTAssertFalse(refused.allowsConnect,
                       "a refusal-bearing preview must never offer Connect")
        XCTAssertNil(refused.safetyNetStatement,
                     "no file will be created, so the create statement must not render")
    }

    func testAbsentConfigIsACreate() throws {
        let create = try preview(accessState: "absent")

        XCTAssertEqual(create.changeKind, .create)
        XCTAssertTrue(create.allowsConnect)
        XCTAssertEqual(
            create.safetyNetStatement,
            "This file does not exist; it will be created, and Undo removes it."
        )
    }

    func testReadableConfigWithoutTheEntryIsAnAdd() throws {
        let add = try preview(entryExists: false, accessState: "accessible")

        XCTAssertEqual(add.changeKind, .add)
        XCTAssertTrue(add.allowsConnect)
        XCTAssertEqual(
            add.safetyNetStatement,
            "A timestamped backup of this file will be created alongside it."
        )
    }

    /// The write adopts an equivalent entry living under a different key; the
    /// summary names that key, and the change kind must carry it so the form
    /// can say what it is really replacing.
    func testExistingEntryIsAReplaceNamedAfterTheAdoptedEntry() throws {
        let replace = try preview(entryExists: true, summaryName: "old-proxy")

        XCTAssertEqual(replace.changeKind, .replace(entryName: "old-proxy"))
        XCTAssertTrue(replace.allowsConnect)
        XCTAssertEqual(
            replace.safetyNetStatement,
            "A timestamped backup of this file will be created alongside it."
        )
    }

    /// Without a summary (older core) the replace still classifies, falling back
    /// to the requested entry name.
    func testReplaceWithoutASummaryFallsBackToTheRequestedEntryName() throws {
        let replace = try preview(entryExists: true)

        XCTAssertEqual(replace.changeKind, .replace(entryName: "mcpproxy"))
    }

    func testMalformedConfigBlocksTheConnectControl() throws {
        let blocked = try preview(accessState: "malformed")

        XCTAssertEqual(blocked.changeKind, .blockedByAccess(.malformed))
        XCTAssertFalse(blocked.allowsConnect)
        XCTAssertNil(blocked.safetyNetStatement)
    }

    func testDeniedConfigBlocksTheConnectControl() throws {
        let blocked = try preview(accessState: "denied")

        XCTAssertEqual(blocked.changeKind, .blockedByAccess(.denied))
        XCTAssertFalse(blocked.allowsConnect)
        XCTAssertNil(blocked.safetyNetStatement)
    }

    // MARK: - Credential notice (FR-004)

    func testCredentialNoticeAppearsOnlyWhenTheEntryEmbedsTheKey() throws {
        let withKey = try preview(accessState: "absent", containsAPIKey: true)
        let withoutKey = try preview(accessState: "absent", containsAPIKey: false)

        XCTAssertNotNil(withKey.credentialNotice)
        XCTAssertEqual(withKey.credentialNotice?.isEmpty, false)
        XCTAssertNil(withoutKey.credentialNotice)
    }
}
