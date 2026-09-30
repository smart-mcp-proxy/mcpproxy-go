import XCTest
@testable import MCPProxy

/// Spec 108-k (T112, FR-048, K14): the tray "Clients" submenu is a pure
/// function of the clients and profiles lists. It replaces the v2 "Profile:"
/// switcher (the inverted assertions live in `TrayAuditMenuTests`).
final class ClientsTrayMenuTests: XCTestCase {

    // MARK: Fixtures

    private func client(
        id: String, name: String, credential: String? = "client", profile: String = "",
        title: String? = nil, mode: String = "switchable", connected: Bool = true, missing: Bool = false
    ) -> ClientPresenceRecord {
        var json: [String: Any] = [
            "id": id, "display_name": name, "kind": "supported", "state": "connected_seen",
            "installed": true, "connected": connected, "active_sessions": 0, "calls_24h": 0,
            "profile": profile, "profile_mode": mode, "profile_missing": missing,
        ]
        if let credential { json["credential_state"] = credential }
        if let title { json["profile_title"] = title }
        return try! JSONDecoder().decode(ClientPresenceRecord.self, from: JSONSerialization.data(withJSONObject: json))
    }

    private func profile(_ name: String, title: String? = nil, servers: [String] = ["github"]) -> ProfileView {
        ProfileView(name: name, title: title, servers: servers)
    }

    private var profiles: [ProfileView] {
        [profile("work-ro", title: "Work Read-only"), profile("work-full", title: "Work Full")]
    }

    // MARK: Row set

    func testOnlyCredentialedOrConnectedClientsAppear() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "cursor", name: "Cursor"),
            client(id: "codex", name: "Codex", credential: "admin_key", connected: true),
            client(id: "zed", name: "Zed", credential: "none", connected: false),
            client(id: "old", name: "Old", credential: "revoked", connected: false),
        ], profiles: profiles)
        XCTAssertEqual(rows.map(\.clientId), ["codex", "cursor"], "sorted by display name; idle clients omitted")
    }

    func testRowsSortByDisplayNameIgnoringCase() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "b", name: "beta"), client(id: "a", name: "Alpha"), client(id: "c", name: "Charlie"),
        ], profiles: profiles)
        XCTAssertEqual(rows.map(\.clientId), ["a", "b", "c"])
    }

    func testNoClientsMeansNoRows() {
        XCTAssertTrue(TrayClientsMenu.build(clients: [], profiles: profiles).isEmpty)
    }

    // MARK: Titles

    func testTitleShowsTheProfileAndALockForALockedClient() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "cursor", name: "Cursor", profile: "work-ro", title: "Work Read-only", mode: "locked"),
        ], profiles: profiles)
        XCTAssertEqual(rows[0].title, "Cursor — Work Read-only 🔒")
    }

    func testSwitchableClientHasNoLock() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "cursor", name: "Cursor", profile: "work-ro", title: "Work Read-only", mode: "switchable"),
        ], profiles: profiles)
        XCTAssertEqual(rows[0].title, "Cursor — Work Read-only")
    }

    func testAllServersIsNamedWhenThereIsNoProfile() {
        let rows = TrayClientsMenu.build(clients: [client(id: "c", name: "Cursor")], profiles: profiles)
        XCTAssertEqual(rows[0].title, "Cursor — All servers")
    }

    func testAMissingProfileIsFlagged() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "c", name: "Cursor", profile: "gone", mode: "locked", missing: true),
        ], profiles: profiles)
        XCTAssertEqual(rows[0].title, "Cursor — gone (missing) 🔒")
    }

    func testTheProfileTitleFallsBackToTheListedTitleThenTheSlug() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "a", name: "A", profile: "work-full"),
            client(id: "b", name: "B", profile: "unlisted"),
        ], profiles: profiles)
        XCTAssertEqual(rows[0].title, "A — Work Full")
        XCTAssertEqual(rows[1].title, "B — unlisted")
    }

    // MARK: Items

    func testProfileItemsCarryACheckmarkOnTheCurrentOne() {
        let row = TrayClientsMenu.build(clients: [
            client(id: "cursor", name: "Cursor", profile: "work-ro", mode: "switchable"),
        ], profiles: profiles)[0]
        let choices = row.items.compactMap { item -> (String, String, Bool)? in
            if case .profile(let title, let profile, let current, _) = item { return (title, profile, current) }
            return nil
        }
        XCTAssertEqual(choices.map(\.0), ["All servers", "Work Read-only", "Work Full"])
        XCTAssertEqual(choices.map(\.2), [false, true, false])
        XCTAssertEqual(choices.map(\.1), ["", "work-ro", "work-full"], "the slug is what is sent")
    }

    func testAllServersIsCheckedWhenThereIsNoProfile() {
        let row = TrayClientsMenu.build(clients: [client(id: "c", name: "Cursor")], profiles: profiles)[0]
        guard case .profile(_, let slug, let current, _) = row.items[0] else { return XCTFail("first item") }
        XCTAssertEqual(slug, "")
        XCTAssertTrue(current)
    }

    func testAMissingProfileChecksNothing() {
        let row = TrayClientsMenu.build(clients: [
            client(id: "c", name: "Cursor", profile: "gone", missing: true),
        ], profiles: profiles)[0]
        let checked = row.items.filter { if case .profile(_, _, true, _) = $0 { return true } else { return false } }
        XCTAssertTrue(checked.isEmpty)
    }

    func testLockTogglesToUnlockForALockedClient() {
        let locked = TrayClientsMenu.build(clients: [
            client(id: "c", name: "C", profile: "work-ro", mode: "locked"),
        ], profiles: profiles)[0]
        XCTAssertEqual(locked.items.last, .lock(title: "Unlock", mode: .switchable, isEnabled: true))

        let free = TrayClientsMenu.build(clients: [
            client(id: "c", name: "C", profile: "work-ro", mode: "switchable"),
        ], profiles: profiles)[0]
        XCTAssertEqual(free.items.last, .lock(title: "Lock", mode: .locked, isEnabled: true))
    }

    /// Only `switchable` is valid on All servers, so Lock is disabled there.
    func testLockIsDisabledOnAllServers() {
        let row = TrayClientsMenu.build(clients: [client(id: "c", name: "C")], profiles: profiles)[0]
        XCTAssertEqual(row.items.last, .lock(title: "Lock", mode: .locked, isEnabled: false))
    }

    // MARK: No credential

    /// A client without an active client credential cannot be bound: its whole
    /// submenu is the one way to get a credential.
    func testAClientWithoutACredentialOffersOnlyTheUpgrade() {
        let rows = TrayClientsMenu.build(clients: [
            client(id: "codex", name: "Codex", credential: "admin_key"),
            client(id: "gone", name: "Gone", credential: nil, connected: true),
        ], profiles: profiles)
        for row in rows {
            XCTAssertEqual(row.items, [.upgrade(title: "Upgrade to client credential…")], row.clientId)
        }
    }

    func testARevokedConnectedClientOffersTheUpgradeToo() {
        let row = TrayClientsMenu.build(clients: [
            client(id: "c", name: "C", credential: "revoked", connected: true),
        ], profiles: profiles)[0]
        XCTAssertEqual(row.items, [.upgrade(title: "Upgrade to client credential…")])
    }

    // MARK: Tooltip (F11 kept)

    func testAProfileWhoseServersAreNotConfiguredIsFlagged() {
        let research = profile("research", servers: ["github", "gitlab"])
        let live = profile("live", servers: ["everything"])
        let row = TrayClientsMenu.build(
            clients: [client(id: "c", name: "C")], profiles: [research, live],
            knownServers: ["everything"])[0]
        let tooltips = row.items.compactMap { item -> String? in
            if case .profile(_, _, _, let tooltip) = item { return tooltip }
            return nil
        }
        XCTAssertEqual(tooltips.count, 1)
        XCTAssertTrue(tooltips[0].contains("github, gitlab"))
    }

    // MARK: Glance link

    func testAGlanceRowOpensTheClientsActivityWhenAttributionIsAvailable() throws {
        let session = try JSONDecoder().decode(APIClient.MCPSession.self, from: Data(
            #"{"id":"s1","status":"active","client_id":"cursor","client_name":"Cursor"}"#.utf8))
        XCTAssertEqual(
            GlanceClientLink.filter(forSession: "s1", sessions: [session], scopeFiltersAvailable: true),
            .forClient("cursor"))
        XCTAssertEqual(
            GlanceClientLink.filter(forSession: "s1", sessions: [session], scopeFiltersAvailable: false),
            .forSession("s1"), "without scope_filters it stays the session link")
        XCTAssertEqual(
            GlanceClientLink.filter(forSession: "other", sessions: [session], scopeFiltersAvailable: true),
            .forSession("other"))
    }
}
