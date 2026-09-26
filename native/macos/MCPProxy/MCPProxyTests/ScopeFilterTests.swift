import XCTest
@testable import MCPProxy

/// Spec 109-k T116: the macOS `ScopeFilter` maps to exactly the REST query the
/// URL filter contract (contracts/url-filter-contract.md) defines — both the
/// parameter table and the `view` → REST table — so a filter applied natively
/// asks the core the same question the Web UI and the CLI ask.
final class ScopeFilterTests: XCTestCase {

    private let now = ISO8601DateFormatter().date(from: "2026-09-26T12:00:00Z")!

    private func items(_ request: ScopeRequest?) -> [String: [String]] {
        var out: [String: [String]] = [:]
        for item in request?.query ?? [] {
            out[item.name, default: []].append(item.value ?? "")
        }
        return out
    }

    private func activity(_ f: ScopeFilter, scopeFilters: Bool = false) -> ScopeRequest? {
        f.restRequest(for: .activity, scopeFiltersAvailable: scopeFilters, now: now)
    }

    // MARK: - view → REST

    func testDefaultViewIsToolCalls() {
        let f = ScopeFilter()
        XCTAssertEqual(f.view, .calls)
        let r = activity(f)
        XCTAssertEqual(r?.path, "/api/v1/activity")
        XCTAssertEqual(items(r)["type"], ["tool_call,internal_tool_call"])
    }

    func testSystemViewRequestsEveryOtherType() {
        var f = ScopeFilter()
        f.view = .system
        let types = items(activity(f))["type"]?.first?.split(separator: ",").map(String.init) ?? []
        XCTAssertEqual(Set(types), Set([
            "policy_decision", "quarantine_change", "server_change", "system_start",
            "system_stop", "config_change", "tool_quarantine_change", "security_scan",
            "credential_broker", "preflight", "prompt_get",
        ]))
        XCTAssertFalse(types.contains("tool_call"))
        XCTAssertFalse(types.contains("internal_tool_call"))
    }

    func testAllViewSendsNoType() {
        var f = ScopeFilter()
        f.view = .all
        XCTAssertNil(items(activity(f))["type"])
    }

    func testExplicitTypeOverridesView() {
        var f = ScopeFilter()
        f.view = .system
        f.type = "config_change"
        XCTAssertEqual(items(activity(f))["type"], ["config_change"])
    }

    func testSegmentsAreToolCallsSessionsSystemAll() {
        XCTAssertEqual(ActivityViewMode.allCases.map(\.label),
                       ["Tool calls", "Sessions", "System events", "All"])
        XCTAssertEqual(ActivityViewMode.allCases.map(\.rawValue),
                       ["calls", "sessions", "system", "all"])
    }

    // MARK: - tool split and rule 8

    func testToolIsSplitIntoServerAndBareTool() {
        var f = ScopeFilter()
        f.tool = "github:create_issue"
        let q = items(activity(f))
        XCTAssertEqual(q["server"], ["github"])
        XCTAssertEqual(q["tool"], ["create_issue"])
    }

    func testAgreeingServerIsSentOnce() {
        var f = ScopeFilter()
        f.server = "github"
        f.tool = "github:create_issue"
        let q = items(activity(f))
        XCTAssertEqual(q["server"], ["github"])
        XCTAssertEqual(q["tool"], ["create_issue"])
        XCTAssertFalse(f.hasConflict)
    }

    func testDisagreeingServerIsAConflictWithNoRequest() {
        var f = ScopeFilter()
        f.server = "notion"
        f.tool = "github:create_issue"
        XCTAssertTrue(f.hasConflict)
        XCTAssertNil(activity(f), "rule 8: no request can express both filters")
        XCTAssertNil(f.restRequest(for: .usage, scopeFiltersAvailable: false, now: now))
        XCTAssertEqual(f.conflictMessage,
                       "No calls match: server notion and tool github:create_issue name different servers")
    }

    func testBareToolIsSentNextToServer() {
        var f = ScopeFilter()
        f.server = "notion"
        f.tool = "search"
        let q = items(activity(f))
        XCTAssertEqual(q["server"], ["notion"])
        XCTAssertEqual(q["tool"], ["search"])
    }

    // MARK: - session routing

    func testWorkSessionIdRoutesToWorkSessionParam() {
        var f = ScopeFilter()
        f.session = "ws-abc"
        let q = items(activity(f))
        XCTAssertEqual(q["work_session_id"], ["ws-abc"])
        XCTAssertNil(q["session_id"])
    }

    func testOtherSessionIdRoutesToSessionParam() {
        var f = ScopeFilter()
        f.session = "3f2a-legacy"
        let q = items(activity(f))
        XCTAssertEqual(q["session_id"], ["3f2a-legacy"])
        XCTAssertNil(q["work_session_id"])
    }

    // MARK: - status, time, caller

    func testStatusIsSentButOtherNeverReachesREST() {
        var f = ScopeFilter()
        f.status = "rejected"
        XCTAssertEqual(items(activity(f))["status"], ["rejected"])
        f.status = "other"
        XCTAssertNil(items(activity(f))["status"])
    }

    func testRelativeFromResolvesAgainstNow() {
        var f = ScopeFilter()
        f.from = "-24h"
        f.to = "2026-09-26T11:00:00Z"
        let q = items(activity(f))
        XCTAssertEqual(q["start_time"], ["2026-09-25T12:00:00Z"])
        XCTAssertEqual(q["end_time"], ["2026-09-26T11:00:00Z"])
        XCTAssertNil(q["from"])
    }

    func testResolveTimeUnits() {
        XCTAssertEqual(ScopeFilter.resolveTime("-30m", now: now), "2026-09-26T11:30:00Z")
        XCTAssertEqual(ScopeFilter.resolveTime("-7d", now: now), "2026-09-19T12:00:00Z")
        XCTAssertEqual(ScopeFilter.resolveTime("2026-01-01T00:00:00Z", now: now), "2026-01-01T00:00:00Z")
    }

    func testAuthTypeIsSent() {
        var f = ScopeFilter()
        f.authType = "agent"
        XCTAssertEqual(items(activity(f))["auth_type"], ["agent"])
    }

    // MARK: - Sessions view

    func testSessionsViewRequestsSessionsWithoutInapplicableParams() {
        var f = ScopeFilter()
        f.view = .sessions
        f.from = "-24h"; f.to = "2026-09-26T11:00:00Z"
        f.server = "github"; f.tool = "github:create_issue"
        f.status = "error"; f.type = "tool_call"; f.authType = "agent"
        f.session = "ws-abc"
        let r = activity(f)
        XCTAssertEqual(r?.path, "/api/v1/sessions")
        let q = items(r)
        for name in ["from", "to", "start_time", "end_time", "server", "tool", "status",
                     "type", "auth_type", "session_id", "work_session_id"] {
            XCTAssertNil(q[name], "\(name) must not reach GET /sessions")
        }
        XCTAssertEqual(Set(f.inapplicableParams(for: .activity)),
                       Set(["from", "to", "server", "tool", "status", "type", "auth_type"]))
    }

    func testSessionsViewCarriesScopeFiltersOnceAvailable() {
        var f = ScopeFilter()
        f.view = .sessions
        f.profile = "work"; f.client = "cursor"; f.token = "ci-bot"
        XCTAssertTrue(items(activity(f, scopeFilters: false)).isEmpty)
        let q = items(activity(f, scopeFilters: true))
        XCTAssertEqual(q["profile"], ["work"])
        XCTAssertEqual(q["client"], ["cursor"])
        XCTAssertEqual(q["token"], ["ci-bot"])
    }

    // MARK: - profile / client / token availability

    func testScopeFiltersHiddenUntilFeatureListed() {
        var f = ScopeFilter()
        f.profile = "work"; f.client = "cursor"; f.token = "ci-bot"
        let hidden = items(activity(f, scopeFilters: false))
        XCTAssertNil(hidden["profile"]); XCTAssertNil(hidden["client"]); XCTAssertNil(hidden["token"])
        XCTAssertTrue(f.visibleScopeParams(scopeFiltersAvailable: false).isEmpty)

        let shown = items(activity(f, scopeFilters: true))
        XCTAssertEqual(shown["profile"], ["work"])
        XCTAssertEqual(shown["client"], ["cursor"])
        XCTAssertEqual(shown["token"], ["ci-bot"])
        XCTAssertEqual(f.visibleScopeParams(scopeFiltersAvailable: true), ["profile", "client", "token"])
    }

    // MARK: - Tools and Servers

    func testToolsNeverSendsServerStatusOrSearch() {
        var f = ScopeFilter()
        f.server = "github"; f.status = "disabled"; f.q = "issue"
        f.tier = "write"; f.approval = "pending"
        f.profile = "work"; f.client = "cursor"; f.token = "ci-bot"
        let hidden = f.restRequest(for: .tools, scopeFiltersAvailable: false, now: now)
        XCTAssertEqual(hidden?.path, "/api/v1/tools")
        XCTAssertTrue(items(hidden).isEmpty)
        let q = items(f.restRequest(for: .tools, scopeFiltersAvailable: true, now: now))
        XCTAssertEqual(Set(q.keys), ["profile", "client"])
    }

    func testServersSendsOnlyProfile() {
        var f = ScopeFilter()
        f.status = "degraded"; f.q = "git"
        f.profile = "work"; f.client = "cursor"
        let r = f.restRequest(for: .servers, scopeFiltersAvailable: true, now: now)
        XCTAssertEqual(r?.path, "/api/v1/servers")
        XCTAssertEqual(Set(items(r).keys), ["profile"])
    }

    // MARK: - Usage window mapping

    func testUsageWindowPresets() {
        func window(_ from: String?, _ to: String? = nil) -> [String]? {
            var f = ScopeFilter()
            f.from = from; f.to = to
            return items(f.restRequest(for: .usage, scopeFiltersAvailable: false, now: now))["window"]
        }
        XCTAssertEqual(window("-24h"), ["24h"])
        XCTAssertEqual(window("-7d"), ["7d"])
        XCTAssertEqual(window(nil), ["all"])
        XCTAssertEqual(window("-3d"), ["all"], "an unmappable range is not applied, never silently 24h")
        XCTAssertEqual(window("-24h", "2026-09-26T11:00:00Z"), ["all"])
    }

    func testUsageUnmappableRangeIsAnInapplicableChip() {
        var f = ScopeFilter()
        f.from = "-3d"
        XCTAssertEqual(f.inapplicableParams(for: .usage), ["from"])
        f.from = "-7d"
        XCTAssertTrue(f.inapplicableParams(for: .usage).isEmpty)
    }

    func testUsageSplitsTool() {
        var f = ScopeFilter()
        f.tool = "github:create_issue"
        let r = f.restRequest(for: .usage, scopeFiltersAvailable: false, now: now)
        XCTAssertEqual(r?.path, "/api/v1/activity/usage")
        let q = items(r)
        XCTAssertEqual(q["server"], ["github"])
        XCTAssertEqual(q["tool"], ["create_issue"])
        XCTAssertNil(q["start_time"])
    }

    // MARK: - URL parsing (a Web URL's parameters translate to the same filter)

    func testInitFromURLQuery() {
        let f = ScopeFilter(query: [
            "view": "system", "server": "github", "tool": "github:search",
            "session": "ws-1", "status": "error", "from": "-24h", "auth_type": "admin",
            "risk": "write", "unknown": "kept-elsewhere",
        ])
        XCTAssertEqual(f.view, .system)
        XCTAssertEqual(f.server, "github")
        XCTAssertEqual(f.tool, "github:search")
        XCTAssertEqual(f.session, "ws-1")
        XCTAssertEqual(f.status, "error")
        XCTAssertEqual(f.from, "-24h")
        XCTAssertEqual(f.authType, "admin")
        XCTAssertEqual(f.tier, "write", "risk is an alias of tier")
    }

    func testUnknownViewFallsBackToCalls() {
        XCTAssertEqual(ScopeFilter(query: ["view": "bogus"]).view, .calls)
    }

    func testQueryStringIsEscaped() {
        var f = ScopeFilter()
        f.view = .all
        f.server = "my server&x"
        XCTAssertEqual(activity(f)?.queryString, "server=my%20server%26x")
    }

    // MARK: - Status feature decoding

    func testStatusDecodesScopeFiltersFeature() throws {
        let with = #"{"running":true,"features":{"scope_filters":["profile","client","token"]}}"#
        let s1 = try JSONDecoder().decode(StatusResponse.self, from: Data(with.utf8))
        XCTAssertTrue(s1.scopeFiltersAvailable)
        let without = #"{"running":true}"#
        let s2 = try JSONDecoder().decode(StatusResponse.self, from: Data(without.utf8))
        XCTAssertFalse(s2.scopeFiltersAvailable)
    }
}

/// The in-app link channel (T122): a tray glance client row, a Clients row or a
/// Token row hands its filter to AppState before switching the sidebar, and the
/// Activity view consumes it exactly once.
@MainActor
final class AppStateScopeFilterTests: XCTestCase {

    func testGlanceSessionHandOffSetsTheFilter() {
        let state = AppState()
        state.handOffScopeFilter(.forSession("ws-abc"))
        XCTAssertEqual(state.scopeFilter?.session, "ws-abc")
        XCTAssertEqual(state.scopeFilter?.view, .calls)
    }

    func testConsumeClearsThePendingFilter() {
        let state = AppState()
        var f = ScopeFilter()
        f.server = "github"
        state.handOffScopeFilter(f)
        XCTAssertEqual(state.consumeScopeFilter()?.server, "github")
        XCTAssertNil(state.scopeFilter)
        XCTAssertNil(state.consumeScopeFilter(), "a second view must not re-apply it")
    }

    func testOpenActivityHandsOffThenNotifies() {
        let state = AppState()
        var switched = false
        var delivered: ScopeFilter?
        let o1 = NotificationCenter.default.addObserver(forName: .switchToActivity, object: nil, queue: nil) { _ in
            switched = true
        }
        let o2 = NotificationCenter.default.addObserver(forName: .activityFilter, object: nil, queue: nil) { note in
            delivered = note.object as? ScopeFilter
        }
        defer {
            NotificationCenter.default.removeObserver(o1)
            NotificationCenter.default.removeObserver(o2)
        }
        state.openActivity(with: .forToken("ci-bot"))
        XCTAssertEqual(state.scopeFilter?.token, "ci-bot", "published before the switch for a new window")
        XCTAssertTrue(switched)
        XCTAssertEqual(delivered?.token, "ci-bot")
    }

    func testClientAndTokenLinksCarryTheirScope() {
        XCTAssertEqual(ScopeFilter.forClient("cursor").client, "cursor")
        XCTAssertEqual(ScopeFilter.forToken("ci-bot").token, "ci-bot")
    }
}

/// Link map: a Sessions-view row opens `view=calls&session=<work_session_id>`,
/// never its transport `id`; a legacy row without one links its `id`.
final class ScopeFilterSessionRowTests: XCTestCase {
    private func session(_ json: String) throws -> APIClient.MCPSession {
        try JSONDecoder().decode(APIClient.MCPSession.self, from: Data(json.utf8))
    }

    func testRowWithWorkSessionLinksTheWorkSession() throws {
        let row = try session(#"{"id":"S1","status":"active","work_session_id":"ws-W1"}"#)
        let f = ScopeFilter.forSessionRow(row)
        XCTAssertEqual(f.view, .calls)
        XCTAssertEqual(f.session, "ws-W1")
        let q = f.restRequest(for: .activity, scopeFiltersAvailable: false)?.query ?? []
        XCTAssertTrue(q.contains(URLQueryItem(name: "work_session_id", value: "ws-W1")))
    }

    func testLegacyRowLinksItsTransportId() throws {
        let row = try session(#"{"id":"S1","status":"closed"}"#)
        let f = ScopeFilter.forSessionRow(row)
        XCTAssertEqual(f.session, "S1")
        let q = f.restRequest(for: .activity, scopeFiltersAvailable: false)?.query ?? []
        XCTAssertTrue(q.contains(URLQueryItem(name: "session_id", value: "S1")))
    }

    func testSessionRowMatchesEitherId() throws {
        let row = try session(#"{"id":"S1","status":"active","work_session_id":"ws-W1"}"#)
        XCTAssertTrue(ScopeFilter.forSession("ws-W1").highlights(row))
        XCTAssertTrue(ScopeFilter.forSession("S1").highlights(row))
        XCTAssertFalse(ScopeFilter.forSession("S2").highlights(row))
    }
}
