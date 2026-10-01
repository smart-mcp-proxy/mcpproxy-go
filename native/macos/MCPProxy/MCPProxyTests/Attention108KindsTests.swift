// Attention108KindsTests.swift
// MCPProxyTests
//
// Spec 109-l (FR-093, P9): the Spec 108 warnings arrive as needs-attention
// items (a `setting` subject for the binding guard, `client` subjects for the
// rest) and their fix buttons are navigation only: they open Settings, the
// Clients hub, a sheet or the Agent Tokens tab, and never mutate config or a
// credential.

import XCTest
@testable import MCPProxy

@MainActor
final class Attention108KindsTests: XCTestCase {

    override func setUp() {
        super.setUp()
        HomeReviewActionStubURLProtocol.reset()
    }

    // The exact wire shapes of the core's P1 table (internal/runtime/attention_108.go).
    private let fixtureJSON = """
    {"count":6,"generated_at":"2026-10-01T09:00:00Z","items":[
     {"id":"anonymous_denied_by_binding_guard:setting:require_mcp_auth","kind":"anonymous_denied_by_binding_guard","rank":4,
      "subject":{"type":"setting","id":"require_mcp_auth","name":"Anonymous callers"},
      "summary":"Anonymous callers are denied: client bindings could be bypassed without authentication",
      "detail":"Bound: Codex. Turn on authentication, or set anonymous callers to a profile no wider than the bindings.",
      "fix":{"verb":"change_setting","label":"Require authentication…","target":"/settings?tab=security&focus=require_mcp_auth"},
      "since":"2026-10-01T08:59:00Z"},
     {"id":"client_holds_admin_key:client:cursor","kind":"client_holds_admin_key","rank":5,
      "subject":{"type":"client","id":"cursor","name":"Cursor"},"summary":"Cursor: holds the admin API key",
      "detail":"Upgrade it to a client credential, then rotate the admin API key.",
      "fix":{"verb":"upgrade_admin_key_holders","label":"Upgrade…","target":"/clients?focus=cursor"},"since":"2026-10-01T08:59:00Z"},
     {"id":"client_token_name_conflict:client:codex","kind":"client_token_name_conflict","rank":6,
      "subject":{"type":"client","id":"codex","name":"Codex"},"summary":"Codex: token name client-codex is held by an agent token",
      "fix":{"verb":"edit_token","label":"Open token","target":"/clients?tab=tokens&token=client-codex"},"since":"2026-10-01T08:59:00Z"},
     {"id":"profile_missing:client:codex","kind":"profile_missing","rank":7,
      "subject":{"type":"client","id":"codex","name":"Codex"},"summary":"Codex: bound to missing profile gone — denied everything",
      "fix":{"verb":"move_client","label":"Move client…","target":"/clients?focus=codex"},"since":"2026-10-01T08:59:00Z"},
     {"id":"client_rotation_pending:client:codex","kind":"client_rotation_pending","rank":8,
      "subject":{"type":"client","id":"codex","name":"Codex"},"summary":"Codex: credential rotation not finished",
      "fix":{"verb":"reconnect_client","label":"Reconnect…","target":"/clients?focus=codex"},"since":"2026-10-01T08:59:00Z"},
     {"id":"client_credential_expiring:client:codex","kind":"client_credential_expiring","rank":9,
      "subject":{"type":"client","id":"codex","name":"Codex"},"summary":"Codex: client credential expires 2026-10-08",
      "fix":{"verb":"reconnect_client","label":"Reconnect…","target":"/clients?focus=codex"},"since":"2026-09-24T12:00:00Z"}
    ]}
    """

    private func decodeItems() throws -> [AttentionItem] {
        let data = Data(fixtureJSON.utf8)
        struct Response: Decodable { let items: [AttentionItem] }
        return try JSONDecoder().decode(Response.self, from: data).items
    }

    private func item(_ kind: String) throws -> AttentionItem {
        try XCTUnwrap(try decodeItems().first { $0.kind == kind }, kind)
    }

    func testDecodesAllSixKindsAndTheSettingSubject() throws {
        let items = try decodeItems()
        XCTAssertEqual(items.map(\.rank), [4, 5, 6, 7, 8, 9])
        XCTAssertEqual(items[0].subject.type, "setting")
        XCTAssertEqual(items[0].subject.id, "require_mcp_auth")
        XCTAssertEqual(items.dropFirst().map(\.subject.type), Array(repeating: "client", count: 5))
        XCTAssertEqual(items[1].fix.verb, "upgrade_admin_key_holders")
    }

    func testMapperReturnsTheWarningActionPerRow() throws {
        let expected: [(String, FixAction, String?)] = [
            ("anonymous_denied_by_binding_guard", .changeSetting, "require_mcp_auth"),
            ("client_holds_admin_key", .upgradeAdminKeyHolders, nil),
            ("client_token_name_conflict", .editToken, "client-codex"),
            ("profile_missing", .moveClient, "codex"),
            ("client_rotation_pending", .reconnectClient, "codex"),
            ("client_credential_expiring", .reconnectClient, "codex"),
        ]
        for (kind, fixKind, target) in expected {
            let action = try XCTUnwrap(AttentionWarningAction.from(try item(kind)), kind)
            XCTAssertEqual(action.kind, fixKind, kind)
            XCTAssertEqual(action.target, target, kind)
        }
    }

    func testMapperIsNilForServerVerbs() {
        for verb in ["login", "restart", "enable", "set_secret", "review", "reload_hint", "view_logs"] {
            let server = AttentionItem(
                id: "x:server:github", kind: "server_error", rank: 40,
                subject: AttentionSubject(type: "server", id: "github", name: "github"),
                summary: "github: connection error",
                fix: AttentionFix(verb: verb, label: "Fix", target: "/servers/github"),
                since: Date())
            XCTAssertNil(AttentionWarningAction.from(server), verb)
        }
    }

    func testRoutesAreNavigationToTheOwningScreen() throws {
        XCTAssertEqual(AttentionWarningAction.route(for: try item("anonymous_denied_by_binding_guard")), .settings(.requireMCPAuth))
        XCTAssertEqual(AttentionWarningAction.route(for: try item("client_holds_admin_key")), .upgradeAdminKeys)
        XCTAssertEqual(AttentionWarningAction.route(for: try item("client_token_name_conflict")),
                       .clients(tab: .tokens, filter: .forToken("client-codex")))
        XCTAssertEqual(AttentionWarningAction.route(for: try item("profile_missing")), .clientDetail(id: "codex"))
        XCTAssertEqual(AttentionWarningAction.route(for: try item("client_rotation_pending")), .connectSheet(clientId: "codex"))
        XCTAssertEqual(AttentionWarningAction.route(for: try item("client_credential_expiring")), .connectSheet(clientId: "codex"))
    }

    func testPerformFixNavigatesAndNeverMutates() async throws {
        for kind in ["anonymous_denied_by_binding_guard", "client_holds_admin_key", "client_token_name_conflict",
                     "profile_missing", "client_rotation_pending", "client_credential_expiring"] {
            HomeReviewActionStubURLProtocol.reset()
            let appState = AppState()
            appState.apiClient = HomeReviewActionStubURLProtocol.makeClient()
            let attention = try item(kind)

            await HomeAttentionAction.performFix(attention, appState: appState)

            XCTAssertEqual(appState.pendingRoute, AttentionWarningAction.route(for: attention), kind)
            XCTAssertNotNil(appState.pendingRoute, kind)
            for request in HomeReviewActionStubURLProtocol.requests {
                XCTAssertFalse(["POST", "PUT", "PATCH", "DELETE"].contains(request.method),
                               "\(kind) must not mutate (\(request.method) \(request.url))")
            }
        }
    }

    func testTrayRowForAClientOrSettingItemIsActionable() throws {
        for kind in ["anonymous_denied_by_binding_guard", "client_holds_admin_key", "profile_missing"] {
            XCTAssertTrue(AttentionWarningAction.isActionable(try item(kind)), kind)
        }
        let server = AttentionItem(
            id: "x:server:github", kind: "server_error", rank: 40,
            subject: AttentionSubject(type: "server", id: "github", name: "github"), summary: "s",
            fix: AttentionFix(verb: "restart", label: "Restart", target: "/servers/github"), since: Date())
        XCTAssertFalse(AttentionWarningAction.isActionable(server))
        // client_never_seen keeps its own reload_hint behaviour.
        let neverSeen = AttentionItem(
            id: "client_never_seen:client:cursor", kind: "client_never_seen", rank: 70,
            subject: AttentionSubject(type: "client", id: "cursor", name: "Cursor"), summary: "s",
            fix: AttentionFix(verb: "reload_hint", label: "How to restart", target: "/clients?focus=cursor"), since: Date())
        XCTAssertFalse(AttentionWarningAction.isActionable(neverSeen))
    }
}
