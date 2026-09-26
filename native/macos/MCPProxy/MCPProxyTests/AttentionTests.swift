// AttentionTests.swift
// MCPProxyTests
//
// Spec 109 FR-001/FR-005/T058: decoding the wire shape, the tray's
// "Needs Attention" menu model built from it (review rows open the review
// screen, never approve), and the Home section model (AppState.attention).

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class AttentionTests: XCTestCase {

    // MARK: - Decoding (contracts/rest-api.md#attention)

    /// The exact example payload from contracts/rest-api.md#attention:
    /// sign-in-required + server-review for the same server (a quarantined
    /// OAuth server) plus a client-never-seen item.
    private static let fixtureJSON = """
    {
      "count": 3,
      "generated_at": "2026-09-25T06:12:03Z",
      "items": [
        {
          "id": "sign_in_required:server:github",
          "kind": "sign_in_required",
          "rank": 10,
          "subject": {"type": "server", "id": "github", "name": "github"},
          "summary": "github: sign in required",
          "detail": "OAuth · api.githubcopilot.com",
          "fix": {"verb": "login", "label": "Sign in", "target": "/servers/github"},
          "since": "2026-09-25T06:02:11Z"
        },
        {
          "id": "server_review:server:github",
          "kind": "server_review",
          "rank": 50,
          "subject": {"type": "server", "id": "github", "name": "github"},
          "summary": "github: waiting for review",
          "detail": "Tools can be reviewed once sign-in finishes",
          "fix": {"verb": "review", "label": "Review", "target": "/review/github"},
          "since": "2026-09-25T06:01:40Z"
        },
        {
          "id": "client_never_seen:client:codex",
          "kind": "client_never_seen",
          "rank": 70,
          "subject": {"type": "client", "id": "codex", "name": "Codex CLI"},
          "summary": "Codex CLI: connected, never seen",
          "detail": "Restart Codex to load MCPProxy",
          "fix": {"verb": "reload_hint", "label": "How to restart", "target": "/clients?focus=codex"},
          "since": "2026-09-25T05:50:00Z"
        }
      ]
    }
    """

    private func decodeFixture() throws -> AttentionResponse {
        try JSONDecoder().decode(AttentionResponse.self, from: Data(Self.fixtureJSON.utf8))
    }

    func testDecodesTheDocumentedRESTShape() throws {
        let response = try decodeFixture()

        XCTAssertEqual(response.count, 3)
        XCTAssertEqual(response.items.count, 3)
        XCTAssertEqual(response.items.map(\.id), [
            "sign_in_required:server:github",
            "server_review:server:github",
            "client_never_seen:client:codex",
        ])
        XCTAssertEqual(response.items.map(\.rank), [10, 50, 70])
    }

    func testDecodesTheRFC3339SinceAndGeneratedAtTimestamps() throws {
        let response = try decodeFixture()
        // Both fields are Go's RFC 3339 rendering; the shared decoder's
        // .deferredToDate default cannot parse them, so this pins the
        // model-local parsing (UsageBucket.parseRFC3339) actually runs.
        XCTAssertEqual(
            Int(response.generatedAt.timeIntervalSince1970),
            Int(UsageBucket.parseRFC3339("2026-09-25T06:12:03Z")!.timeIntervalSince1970)
        )
        XCTAssertEqual(
            Int(response.items[0].since.timeIntervalSince1970),
            Int(UsageBucket.parseRFC3339("2026-09-25T06:02:11Z")!.timeIntervalSince1970)
        )
    }

    func testDecodesClientSubjectsAndOptionalDetail() throws {
        let response = try decodeFixture()
        let clientItem = try XCTUnwrap(response.items.first { $0.subject.type == "client" })
        XCTAssertEqual(clientItem.subject.id, "codex")
        XCTAssertEqual(clientItem.subject.name, "Codex CLI")
        XCTAssertEqual(clientItem.fix.verb, "reload_hint")
        XCTAssertEqual(clientItem.detail, "Restart Codex to load MCPProxy")
    }

    func testDetailIsOptionalAndDecodesToNilWhenAbsent() throws {
        let json = """
        {"count": 1, "generated_at": "2026-09-25T06:12:03Z", "items": [
          {"id": "missing_secret:server:weather", "kind": "missing_secret", "rank": 20,
           "subject": {"type": "server", "id": "weather", "name": "weather"},
           "summary": "weather: missing secret",
           "fix": {"verb": "set_secret", "label": "Add secret", "target": "/servers/weather?tab=config&focus=env"},
           "since": "2026-09-25T06:00:00Z"}
        ]}
        """
        let response = try JSONDecoder().decode(AttentionResponse.self, from: Data(json.utf8))
        XCTAssertNil(response.items[0].detail)
    }

    // MARK: - Tray "Needs Attention" menu model (FR-001/FR-005)

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    /// Drives the real `rebuildMenu()` off `appState.attention` (not
    /// `servers`) and pins F4/FR-005: a `review` row never carries a
    /// one-click execute action — it is disclosure-only, straight to the
    /// server's detail view.
    func testNeedsAttentionMenuRowForReviewOpensDetailNeverApprove() throws {
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: CountingGlanceDataSource(), menuHost: host)
        controller.appState.coreState = .connected
        controller.appState.updateAttention(try decodeFixture().items.filter { $0.subject.type == "server" })
        controller.rebuildMenu()

        let parent = try XCTUnwrap((host.menu?.items ?? []).first { $0.title.hasPrefix("Needs Attention") })
        XCTAssertEqual(parent.title, "Needs Attention (2)",
                       "the count is the FR-001 item count, github's two items included")

        let submenu = try XCTUnwrap(parent.submenu)
        let reviewRow = try XCTUnwrap(submenu.items.first { $0.title.contains("waiting for review") })
        XCTAssertNil(reviewRow.submenu, "no verb the tray can run — straight disclosure")
        XCTAssertEqual(reviewRow.representedObject as? String, "github",
                       "navigates by server name, never a one-click approve/unquarantine")
        XCTAssertNotEqual(reviewRow.action, NSSelectorFromString("performAttentionAction:"),
                          "a review row must never be wired to the execute-in-place action")
    }

    /// The `login` item DOES carry an in-place execute action (Sign in),
    /// mirroring the tray's own three self-executed verbs.
    func testNeedsAttentionMenuRowForLoginCarriesTheExecuteSubmenu() throws {
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: CountingGlanceDataSource(), menuHost: host)
        controller.appState.coreState = .connected
        controller.appState.updateAttention(try decodeFixture().items.filter { $0.subject.type == "server" })
        controller.rebuildMenu()

        let parent = try XCTUnwrap((host.menu?.items ?? []).first { $0.title.hasPrefix("Needs Attention") })
        let submenu = try XCTUnwrap(parent.submenu)
        let loginRow = try XCTUnwrap(submenu.items.first { $0.title.contains("sign in required") })
        let rowMenu = try XCTUnwrap(loginRow.submenu)
        XCTAssertEqual(rowMenu.items.first?.title, "Sign in")
        XCTAssertTrue(rowMenu.items.map(\.title).contains("Open Server Details"))
    }

    // MARK: - Home section model (AppState.attention)

    func testHomeSectionModelIsTheSameListTheTrayReads() throws {
        let appState = AppState()
        let items = try decodeFixture().items
        appState.updateAttention(items)

        XCTAssertEqual(appState.attention.map(\.id), items.map(\.id),
                       "Home's section and the tray's group must read the exact same list — one function, one list (FR-001)")
    }

    func testUpdateAttentionIsIdempotentOnAnUnchangedList() throws {
        let appState = AppState()
        let items = try decodeFixture().items
        appState.updateAttention(items)
        let first = appState.attention
        appState.updateAttention(items)
        XCTAssertEqual(appState.attention.map(\.id), first.map(\.id))
    }
}
