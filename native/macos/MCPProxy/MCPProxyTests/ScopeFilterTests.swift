import XCTest
@testable import MCPProxy

// Spec 109-k (activity-scope-filters), contracts/url-filter-contract.md
// "macOS" section: "`ScopeFilterTests` asserts the REST query mapping equals
// the tables above — `tool=github:create_issue` -> `server=github&tool=
// create_issue` (also with `server=github`), `server=notion&tool=github:
// create_issue` -> `nil` and no request (rule 8), `session=ws-...` ->
// `work_session_id`, any other `session` -> `session_id`, `server` never sent
// for Tools ... including `view=sessions` -> a `GET /sessions` request that
// carries no `from`/`to`, `server`, `tool`, `status`, `type` or `auth_type`."
final class ScopeFilterTests: XCTestCase {
    func testBareToolSplitsFromServerColonTool() {
        let filter = ScopeFilter(tool: "github:create_issue")
        XCTAssertEqual(filter.restQuery(), ["server": "github", "tool": "create_issue"])
    }

    func testServerColonToolAgreesWithExplicitServer() {
        let filter = ScopeFilter(server: "github", tool: "github:create_issue")
        XCTAssertEqual(filter.restQuery(), ["server": "github", "tool": "create_issue"])
    }

    func testConflictingServerAndToolPrefixReturnsNil() {
        // rule 8: their intersection is empty, so no request can express both.
        let filter = ScopeFilter(server: "notion", tool: "github:create_issue")
        XCTAssertNil(filter.restQuery())
    }

    func testBareToolWithNoServerPrefixKeepsAnyExplicitServer() {
        let filter = ScopeFilter(server: "filesystem", tool: "read")
        XCTAssertEqual(filter.restQuery(), ["server": "filesystem", "tool": "read"])
    }

    func testBareToolWithNoExplicitServer() {
        let filter = ScopeFilter(tool: "read")
        XCTAssertEqual(filter.restQuery(), ["tool": "read"])
    }

    func testWorkSessionPrefixRoutesToWorkSessionID() {
        let filter = ScopeFilter(session: "ws-abc123")
        XCTAssertEqual(filter.restQuery(), ["work_session_id": "ws-abc123"])
    }

    func testOtherSessionRoutesToSessionID() {
        let filter = ScopeFilter(session: "transport-xyz")
        XCTAssertEqual(filter.restQuery(), ["session_id": "transport-xyz"])
    }

    func testServerAloneIsSent() {
        let filter = ScopeFilter(server: "filesystem")
        XCTAssertEqual(filter.restQuery(), ["server": "filesystem"])
    }

    func testViewCallsMapsToTheTwoCallTypes() {
        let filter = ScopeFilter(view: "calls")
        XCTAssertEqual(filter.restQuery(), ["type": "tool_call,internal_tool_call"])
    }

    func testViewSystemMapsToEveryOtherType() {
        let filter = ScopeFilter(view: "system")
        let query = filter.restQuery()
        XCTAssertEqual(query?["type"]?.contains("tool_call") ?? true, false)
        XCTAssertTrue(query?["type"]?.contains("quarantine_change") ?? false)
    }

    func testViewAllSendsNoTypeFilter() {
        let filter = ScopeFilter(view: "all", server: "filesystem")
        XCTAssertEqual(filter.restQuery(), ["server": "filesystem"])
    }

    func testViewSessionsCarriesNoActivityParameters() {
        // Every parameter that would apply to /activity is set here, but
        // view=sessions targets GET /sessions instead, which (per the
        // contract's "view -> REST" table) takes none of them.
        let filter = ScopeFilter(
            view: "sessions", server: "filesystem", tool: "read",
            session: "ws-abc", status: "success", authType: "admin"
        )
        XCTAssertEqual(filter.restQuery(), [:])
    }

    func testEmptyFilterIsEmpty() {
        XCTAssertTrue(ScopeFilter().isEmpty)
        XCTAssertFalse(ScopeFilter(session: "ws-abc").isEmpty)
    }

    func testEmptyFilterRestQueryIsEmptyDictionary() {
        XCTAssertEqual(ScopeFilter().restQuery(), [:])
    }
}
