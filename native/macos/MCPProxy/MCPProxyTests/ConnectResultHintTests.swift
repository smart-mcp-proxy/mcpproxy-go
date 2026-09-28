import XCTest
@testable import MCPProxy

/// Spec 109-b T033: the connect result view model must decode and render
/// `reload_hint` and `display_path` (FR-037/FR-042).
///
/// Every fixture here is JSON the core actually emits — synthesized, never
/// fetched — so this runs without a live core.
final class ConnectResultHintTests: XCTestCase {

    private func decodeResult(_ json: String) throws -> APIClient.ConnectResult {
        try JSONDecoder().decode(APIClient.ConnectResult.self, from: Data(json.utf8))
    }

    private func decodeStatus(_ json: String) throws -> APIClient.ClientStatus {
        try JSONDecoder().decode(APIClient.ClientStatus.self, from: Data(json.utf8))
    }

    private func decodePreview(_ json: String) throws -> ConnectPreviewModel {
        try JSONDecoder().decode(ConnectPreviewModel.self, from: Data(json.utf8))
    }

    // MARK: - ConnectResult

    func testConnectResultDecodesDisplayPathAndReloadHint() throws {
        let result = try decodeResult("""
        {"success":true,"client":"cursor","config_path":"/Users/x/.cursor/mcp.json",
         "display_path":"~/.cursor/mcp.json","server_name":"mcpproxy","action":"created",
         "message":"Successfully connected mcpproxy to cursor",
         "reload_hint":"Reload the Cursor window (or restart Cursor) to load MCPProxy"}
        """)

        XCTAssertTrue(result.success)
        XCTAssertEqual(result.displayPath, "~/.cursor/mcp.json")
        XCTAssertEqual(result.reloadHint, "Reload the Cursor window (or restart Cursor) to load MCPProxy")
        XCTAssertEqual(result.effectiveDisplayPath, "~/.cursor/mcp.json")
    }

    /// A core older than Spec 109-b sends neither field; the result must
    /// still decode, and effectiveDisplayPath falls back to the full path.
    func testConnectResultToleratesACoreWithoutTheNewFields() throws {
        let result = try decodeResult("""
        {"success":true,"client":"cursor","config_path":"/Users/x/.cursor/mcp.json",
         "server_name":"mcpproxy","action":"created","message":"Connected."}
        """)

        XCTAssertNil(result.displayPath)
        XCTAssertNil(result.reloadHint)
        XCTAssertEqual(result.effectiveDisplayPath, "/Users/x/.cursor/mcp.json")
    }

    func testConnectResultCarriesTheFieldsOnAFailureBranchToo() throws {
        // FR-037: "populated for every result whose ConfigPath is known" —
        // not gated on success, e.g. an already_exists conflict.
        let result = try decodeResult("""
        {"success":false,"client":"cursor","config_path":"/Users/x/.cursor/mcp.json",
         "display_path":"~/.cursor/mcp.json","action":"already_exists",
         "message":"mcpproxy already registered in cursor (use --force to overwrite)",
         "reload_hint":"Reload the Cursor window (or restart Cursor) to load MCPProxy"}
        """)

        XCTAssertFalse(result.success)
        XCTAssertEqual(result.displayPath, "~/.cursor/mcp.json")
        XCTAssertEqual(result.reloadHint, "Reload the Cursor window (or restart Cursor) to load MCPProxy")
    }

    // MARK: - ClientStatus

    func testClientStatusDecodesDisplayPathAndReloadHint() throws {
        let status = try decodeStatus("""
        {"id":"claude-code","name":"Claude Code","config_path":"/Users/x/.claude.json",
         "display_path":"~/.claude.json","exists":true,"connected":true,"supported":true,
         "icon":"claude-code","server_name":"mcpproxy","access_state":"accessible",
         "reload_hint":"Run /mcp in Claude Code (or restart it) to load MCPProxy"}
        """)

        XCTAssertEqual(status.displayPath, "~/.claude.json")
        XCTAssertEqual(status.reloadHint, "Run /mcp in Claude Code (or restart it) to load MCPProxy")
        XCTAssertEqual(status.effectiveDisplayPath, "~/.claude.json")
    }

    func testClientStatusEffectiveDisplayPathFallsBackWithoutTheField() throws {
        let status = try decodeStatus("""
        {"id":"gemini","name":"Gemini CLI","config_path":"/Users/x/.gemini/settings.json",
         "exists":false,"connected":false,"supported":true}
        """)

        XCTAssertNil(status.displayPath)
        XCTAssertNil(status.reloadHint)
        XCTAssertEqual(status.effectiveDisplayPath, "/Users/x/.gemini/settings.json")
    }

    // MARK: - ConnectPreviewModel

    /// The preview pane must render the same shortened path as the status
    /// list and the post-connect result (FR-037) — not the raw, unshortened
    /// `config_path` — so `effectiveDisplayPath` needs to actually decode and
    /// resolve here too, not just on `ClientStatus`/`ConnectResult`.
    func testConnectPreviewDecodesDisplayPath() throws {
        let preview = try decodePreview("""
        {"client":"cursor","config_path":"/Users/x/.cursor/mcp.json",
         "display_path":"~/.cursor/mcp.json","server_name":"mcpproxy",
         "entry_text":"{}","entry_exists":false,"contains_api_key":false,
         "access_state":"accessible","precondition_token":"tok"}
        """)

        XCTAssertEqual(preview.displayPath, "~/.cursor/mcp.json")
        XCTAssertEqual(preview.effectiveDisplayPath, "~/.cursor/mcp.json")
    }

    /// A core older than Spec 109-b's preview change sends no `display_path`;
    /// the preview must still decode and fall back to the full path.
    func testConnectPreviewToleratesACoreWithoutDisplayPath() throws {
        let preview = try decodePreview("""
        {"client":"cursor","config_path":"/Users/x/.cursor/mcp.json",
         "server_name":"mcpproxy","entry_text":"{}","entry_exists":false,
         "contains_api_key":false,"access_state":"accessible","precondition_token":"tok"}
        """)

        XCTAssertNil(preview.displayPath)
        XCTAssertEqual(preview.effectiveDisplayPath, "/Users/x/.cursor/mcp.json")
    }
}
