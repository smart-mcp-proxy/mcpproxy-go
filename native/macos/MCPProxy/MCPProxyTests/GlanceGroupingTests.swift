import XCTest
@testable import MCPProxy

/// Spec 090 US1 — consecutive same-(server, tool, outcome class) records collapse
/// into one `GlanceRun` (FR-001…FR-004, FR-024).
final class GlanceGroupingTests: XCTestCase {

    // MARK: - Maximal consecutive runs

    func testConsecutiveCallsToOneToolCollapseIntoASingleRun() {
        var page: [ActivityEntry] = []
        for i in 0..<19 {
            page.append(Self.call(id: "jira-\(i)", server: "jira-gcore", tool: "jira_get_issue"))
        }
        page.append(Self.call(id: "gh-0", server: "github", tool: "create_issue"))

        let runs = GlanceSelection.activityRows(from: page)

        XCTAssertEqual(runs.count, 2, "a 19-call burst must not eat all five rows")
        XCTAssertEqual(runs[0].count, 19)
        XCTAssertEqual(runs[0].key.server, "jira-gcore")
        XCTAssertEqual(runs[0].key.tool, "jira_get_issue")
        XCTAssertEqual(runs[0].key.outcomeClass, .call)
        XCTAssertEqual(runs[0].key.statusClass, .success)
        XCTAssertEqual(runs[1].count, 1)
        XCTAssertEqual(runs[1].newest.id, "gh-0")
    }

    func testRecordsInsideARunStayNewestFirst() {
        let page = [
            Self.call(id: "new", server: "s", tool: "t"),
            Self.call(id: "mid", server: "s", tool: "t"),
            Self.call(id: "old", server: "s", tool: "t")
        ]
        let run = GlanceSelection.activityRows(from: page)[0]
        XCTAssertEqual(run.records.map(\.id), ["new", "mid", "old"])
        XCTAssertEqual(run.newest.id, "new")
        XCTAssertEqual(run.oldest.id, "old")
    }

    /// Grouping is consecutive-only: it compresses repetition without
    /// reordering the timeline (US1 scenario 3).
    func testInterleavedToolsKeepThreeRuns() {
        let page = [
            Self.call(id: "a2", server: "s", tool: "alpha"),
            Self.call(id: "b1", server: "s", tool: "beta"),
            Self.call(id: "a1", server: "s", tool: "alpha")
        ]
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.map(\.count), [1, 1, 1])
        XCTAssertEqual(runs.map(\.newest.id), ["a2", "b1", "a1"])
    }

    func testTheSameToolNameOnTwoServersDoesNotGroup() {
        let page = [
            Self.call(id: "1", server: "jira-a", tool: "get_issue"),
            Self.call(id: "2", server: "jira-b", tool: "get_issue")
        ]
        XCTAssertEqual(GlanceSelection.activityRows(from: page).map(\.count), [1, 1])
    }

    // MARK: - Exclusion happens BEFORE grouping (US1 scenario 6)

    func testManagementBuiltInsBetweenTwoCallsDoNotSplitTheRun() {
        let page = [
            Self.call(id: "newer", server: "s", tool: "t"),
            Self.internalCall(id: "mgmt-1", tool: "upstream_servers"),
            Self.internalCall(id: "mgmt-2", tool: "quarantine_security"),
            Self.call(id: "older", server: "s", tool: "t")
        ]
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.count, 1, "records that never render must not split a run")
        XCTAssertEqual(runs[0].count, 2)
        XCTAssertEqual(runs[0].records.map(\.id), ["newer", "older"])
    }

    /// Both calls fail here on purpose: the wrapper only qualifies when it
    /// failed, and the surviving upstream record has to share the run's status
    /// class with `newer` or the split would be the status change, not the
    /// wrapper — which would prove nothing about rule 4.
    func testACollapsedWrapperDoesNotSplitTheRun() {
        let page = [
            Self.call(id: "newer", server: "s", tool: "t",
                      status: "error", error: "newer boom", requestId: "req-1"),
            Self.internalCall(id: "wrapper", tool: "call_tool_read",
                              status: "error", requestId: "req-2"),
            Self.call(id: "older", server: "s", tool: "t",
                      status: "error", error: "boom", requestId: "req-2")
        ]
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.count, 1, "rule 4 collapses the pair before grouping sees it")
        XCTAssertEqual(runs[0].records.map(\.id), ["newer", "older"])
    }

    // MARK: - Status class is part of the key (FR-004)

    /// A stretch of calls to one tool that changed outcome mid-way is NOT one
    /// run: a row marked failed with a ×12 that counts ten successes claims
    /// twelve failures. The stretch splits where the outcome changed, so each
    /// row's count is a count of its own records.
    func testAMixedOutcomeStretchSplitsIntoOneRunPerStatus() {
        var page: [ActivityEntry] = []
        for i in 0..<4 {
            page.append(Self.call(id: "ok-\(i)", server: "s", tool: "t"))
        }
        page.append(Self.call(id: "bad-new", server: "s", tool: "t",
                              status: "error", error: "auth failed: token expired"))
        page.append(Self.call(id: "bad-old", server: "s", tool: "t",
                              status: "error", error: "older failure"))
        for i in 0..<6 {
            page.append(Self.call(id: "ok-late-\(i)", server: "s", tool: "t"))
        }

        let runs = GlanceSelection.activityRows(from: page)

        XCTAssertEqual(runs.count, 3, "success → failure → success is three runs, not one")
        XCTAssertEqual(runs.map(\.count), [4, 2, 6],
                       "a failed run's ×N counts only its own records")
        XCTAssertEqual(runs.map(\.key.statusClass), [.success, .failure, .success])
        XCTAssertEqual(runs.map(\.status), ["success", "error", "success"])
        XCTAssertEqual(runs[1].newestErroring?.id, "bad-new")
        XCTAssertEqual(runs[1].errorMessage, "auth failed: token expired",
                       "the clause comes from the NEWEST record of the failing run")
        XCTAssertNil(runs[0].errorMessage)
        XCTAssertNil(runs[2].errorMessage)
    }

    func testAnAllSuccessRunKeepsTheSuccessStatus() {
        let page = [
            Self.call(id: "1", server: "s", tool: "t"),
            Self.call(id: "2", server: "s", tool: "t")
        ]
        let run = GlanceSelection.activityRows(from: page)[0]
        XCTAssertEqual(run.count, 2)
        XCTAssertEqual(run.status, "success")
        XCTAssertNil(run.newestErroring)
        XCTAssertNil(run.errorMessage)
    }

    /// A call still running is not a failure — and it is not a success either,
    /// so it does not join the run of finished calls before it.
    func testARunningRecordDoesNotJoinTheRunOfFinishedCalls() {
        let page = [
            Self.call(id: "running", server: "s", tool: "t", status: "running"),
            Self.call(id: "done", server: "s", tool: "t")
        ]
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.map(\.count), [1, 1])
        XCTAssertEqual(runs.map(\.status), ["running", "success"])
        XCTAssertEqual(runs.map(\.key.statusClass), [.pending, .success])
        XCTAssertNil(runs[0].newestErroring)
    }

    /// Consecutive failures at one tool are still one run — the split is by
    /// outcome, not per record.
    func testConsecutiveFailuresStayOneRun() {
        let page = [
            Self.call(id: "e1", server: "s", tool: "t", status: "error", error: "newest: boom"),
            Self.call(id: "e2", server: "s", tool: "t", status: "error", error: "older: boom")
        ]
        let run = GlanceSelection.activityRows(from: page)[0]
        XCTAssertEqual(run.count, 2)
        XCTAssertEqual(run.status, "error")
        XCTAssertEqual(run.errorMessage, "newest: boom")
    }

    // MARK: - Reason (FR-004)

    func testTheReasonComesFromTheNewestRecordThatHasOne() {
        let page = [
            Self.call(id: "newest", server: "s", tool: "t"),
            Self.call(id: "mid", server: "s", tool: "t", reason: "Check the transition"),
            Self.call(id: "old", server: "s", tool: "t", reason: "Open the ticket")
        ]
        let run = GlanceSelection.activityRows(from: page)[0]
        XCTAssertEqual(run.displayReason, "Check the transition")
    }

    func testARunWithNoReasonsHasNoDisplayReason() {
        let run = GlanceSelection.activityRows(from: [Self.call(id: "1", server: "s", tool: "t")])[0]
        XCTAssertNil(run.displayReason)
    }

    // MARK: - Count and identity (FR-003, FR-024)

    func testASingleRecordRunCountsOne() {
        let run = GlanceSelection.activityRows(from: [Self.call(id: "1", server: "s", tool: "t")])[0]
        XCTAssertEqual(run.count, 1, "a single call must not render a ×N suffix")
    }

    func testRunIdentityIsTheOldestRecordsKey() {
        let page = [
            Self.call(id: "new", server: "s", tool: "t", requestId: "req-new"),
            Self.call(id: "old", server: "s", tool: "t", requestId: "req-old")
        ]
        let run = GlanceSelection.activityRows(from: page)[0]
        XCTAssertEqual(run.identity, "req-old")
    }

    /// The identity must survive a run growing at the head — otherwise every
    /// new call in a burst looks like a brand-new row to `updateInPlace`.
    func testRunIdentityIsStableWhenTheRunExtendsAtTheHead() {
        let base = [
            Self.call(id: "new", server: "s", tool: "t", requestId: "req-new"),
            Self.call(id: "old", server: "s", tool: "t", requestId: "req-old")
        ]
        let before = GlanceSelection.activityRows(from: base)[0].identity
        let after = GlanceSelection.activityRows(
            from: [Self.call(id: "newer", server: "s", tool: "t", requestId: "req-newer")] + base
        )[0].identity
        XCTAssertEqual(before, after)
    }

    /// A record with no request id falls back to its storage id, like
    /// `recordKey` — legacy records are never collapsed, so that is safe.
    func testIdentityFallsBackToTheStorageIdWithoutARequestId() {
        let entry = Self.call(id: "only", server: "s", tool: "t", requestId: nil)
        XCTAssertEqual(GlanceSelection.activityRows(from: [entry])[0].identity, "only")
    }

    // MARK: - Window and row caps

    /// The count is only ever what the fetched page holds: a 150-call run seen
    /// through a 100-record page reads ×100, and claims nothing beyond it.
    func testTheCountReflectsOnlyTheFetchedWindow() {
        var log: [ActivityEntry] = []
        for i in 0..<150 {
            log.append(Self.call(id: "burst-\(i)", server: "s", tool: "t"))
        }
        let page = Array(log.prefix(AppState.glanceActivityPageSize))
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.count, 1)
        XCTAssertEqual(runs[0].count, AppState.glanceActivityPageSize)
    }

    func testOnlyTheFirstFiveRunsSurvive() {
        var page: [ActivityEntry] = []
        for i in 0..<8 {
            page.append(Self.call(id: "a-\(i)", server: "s", tool: "tool-\(i)"))
            page.append(Self.call(id: "b-\(i)", server: "s", tool: "tool-\(i)"))
        }
        let runs = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(runs.count, GlanceSelection.rowLimit)
        XCTAssertEqual(runs.map(\.count), [2, 2, 2, 2, 2])
        XCTAssertEqual(runs.map(\.newest.id), ["a-0", "a-1", "a-2", "a-3", "a-4"])
    }

    func testAnEmptyPageYieldsNoRuns() {
        XCTAssertTrue(GlanceSelection.activityRows(from: []).isEmpty)
    }

    // MARK: - Outcome class is part of the key (FR-002)

    /// Asserted on `groupConsecutive` directly: whether a blocked record
    /// *qualifies* is US3's business, but the key must already keep the two
    /// classes apart.
    func testBlockedRecordsNeverMergeWithCallsToTheSameTool() {
        let entries = [
            Self.call(id: "call-1", server: "s", tool: "t"),
            Self.blocked(id: "blk-1", server: "s", tool: "t"),
            Self.blocked(id: "blk-2", server: "s", tool: "t"),
            Self.call(id: "call-2", server: "s", tool: "t")
        ]
        let runs = GlanceSelection.groupConsecutive(entries)
        XCTAssertEqual(runs.map(\.count), [1, 2, 1])
        XCTAssertEqual(runs.map(\.key.outcomeClass), [.call, .blocked, .call])
    }

    func testConsecutiveBlocksCollapseIntoOneBlockedRun() {
        var entries: [ActivityEntry] = []
        for i in 0..<27 {
            entries.append(Self.blocked(id: "blk-\(i)", server: "s", tool: "t"))
        }
        let runs = GlanceSelection.groupConsecutive(entries)
        XCTAssertEqual(runs.count, 1)
        XCTAssertEqual(runs[0].count, 27)
        XCTAssertEqual(runs[0].key.outcomeClass, .blocked)
        XCTAssertEqual(runs[0].status, "blocked")
    }

    // MARK: - Helpers

    static func call(
        id: String,
        server: String,
        tool: String,
        status: String = "success",
        error: String? = nil,
        reason: String? = nil,
        requestId: String? = "auto"
    ) -> ActivityEntry {
        entry(id: id, type: "tool_call", server: server, tool: tool, status: status,
              error: error, metadata: reason.map { ["intent": ["reason": $0]] },
              requestId: requestId)
    }

    static func internalCall(
        id: String,
        tool: String,
        status: String = "success",
        requestId: String? = "auto"
    ) -> ActivityEntry {
        entry(id: id, type: "internal_tool_call", server: nil, tool: tool, status: status,
              error: nil, metadata: nil, requestId: requestId)
    }

    static func blocked(
        id: String,
        server: String,
        tool: String,
        reason: String = "Intent rejected"
    ) -> ActivityEntry {
        entry(id: id, type: "policy_decision", server: server, tool: tool, status: "blocked",
              error: nil, metadata: ["decision": "blocked", "reason": reason],
              requestId: "auto")
    }

    private static func entry(
        id: String,
        type: String,
        server: String?,
        tool: String?,
        status: String,
        error: String?,
        metadata: [String: Any]?,
        requestId: String?
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": "2027-01-15T08:00:00Z"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        if let error { json["error_message"] = error }
        if let metadata { json["metadata"] = metadata }
        // "auto" means "a unique id", so records never accidentally collapse;
        // pass nil to model a legacy record with no request id at all.
        if let requestId { json["request_id"] = requestId == "auto" ? "req-\(id)" : requestId }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }
}
