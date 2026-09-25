// HomeReviewActionTests.swift
// MCPProxyTests
//
// Spec 109 FR-005 / T058a: Home's review action — for a quarantined server
// (`server_review`) and for a server with pending/changed tools
// (`tool_review`) — opens the review location (the server's existing
// per-tool review, the detail view before 109-g ships a dedicated screen)
// and issues NO request to POST /servers/{id}/tools/approve,
// /security/approve or /unquarantine. Pins the removal of the one-click
// `approveTools` call (`DashboardView.swift:1066` in the pre-rename file)
// across the DashboardView.swift → HomeView.swift rename: this PR (109-d)
// removes it itself, so this test passes here whatever 109-d's merge order
// is with 109-f (whose own removal, if it lands first, leaves HomeView.swift
// already clean).

import XCTest
@testable import MCPProxy

@MainActor
final class HomeReviewActionTests: XCTestCase {

    override func setUp() {
        super.setUp()
        HomeReviewActionStubURLProtocol.reset()
    }

    private func reviewItem(kind: String, id: String = "everything") -> AttentionItem {
        AttentionItem(
            id: "\(kind):server:\(id)",
            kind: kind,
            rank: kind == "server_review" ? 50 : 60,
            subject: AttentionSubject(type: "server", id: id, name: id),
            summary: "\(id): waiting for review",
            fix: AttentionFix(verb: "review", label: "Review", target: "/review/\(id)"),
            since: Date()
        )
    }

    /// A quarantined server's review action must open the detail view and
    /// never call the core's approve/unquarantine doors.
    func testQuarantinedServerReviewActionOpensDetailViewAndNeverApproves() async throws {
        let appState = AppState()
        appState.apiClient = HomeReviewActionStubURLProtocol.makeClient()
        let item = reviewItem(kind: "server_review", id: "everything")

        let opened = expectation(forNotification: .showServerDetail, object: nil) { note in
            guard let target = note.object as? ServerDetailTarget else { return false }
            return target.serverName == "everything" && target.tab == .tools
        }

        await HomeAttentionAction.performFix(item, appState: appState)
        await fulfillment(of: [opened], timeout: 2)

        assertNoApprovalRequestWasIssued()
    }

    /// A trusted server with pending/changed tools — `tool_review` — must
    /// route through the exact same review location, never a direct approve.
    func testToolReviewActionOpensDetailViewAndNeverApproves() async throws {
        let appState = AppState()
        appState.apiClient = HomeReviewActionStubURLProtocol.makeClient()
        let item = reviewItem(kind: "tool_review", id: "github")

        let opened = expectation(forNotification: .showServerDetail, object: nil) { note in
            guard let target = note.object as? ServerDetailTarget else { return false }
            return target.serverName == "github" && target.tab == .tools
        }

        await HomeAttentionAction.performFix(item, appState: appState)
        await fulfillment(of: [opened], timeout: 2)

        assertNoApprovalRequestWasIssued()
    }

    /// The stronger claim the two tests above rest on: the review action
    /// issues NO request to the core at all — it is pure navigation. Checked
    /// as its own assertion so a future change that adds an unrelated GET
    /// (e.g. a prefetch) is still caught if it slips into a POST.
    private func assertNoApprovalRequestWasIssued() {
        let requests = HomeReviewActionStubURLProtocol.requests
        XCTAssertTrue(requests.isEmpty,
                      "review must issue no request at all — it navigates, it never approves; got \(requests)")
        XCTAssertFalse(requests.contains { $0.url.contains("/tools/approve") },
                       "review must never POST /servers/{id}/tools/approve")
        XCTAssertFalse(requests.contains { $0.url.contains("/security/approve") },
                       "review must never POST /servers/{id}/security/approve")
        XCTAssertFalse(requests.contains { $0.url.contains("/unquarantine") },
                       "review must never POST /servers/{id}/unquarantine")
        XCTAssertFalse(requests.contains { $0.method == "POST" },
                       "review must never issue any POST")
    }
}
