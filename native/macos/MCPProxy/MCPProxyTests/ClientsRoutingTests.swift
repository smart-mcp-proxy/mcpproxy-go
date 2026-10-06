// ClientsRoutingTests.swift
// MCPProxyTests
//
// A `.clientDetail` route clears the active clients filter only when that
// filter would hide the target client (wave-2 item 1451-4).

import XCTest
@testable import MCPProxy

final class ClientsRoutingTests: XCTestCase {
    private func record(_ id: String, profile: String?) -> ClientPresenceRecord {
        var r = ClientPresenceRecord(
            id: id, displayName: id, kind: "supported", icon: nil, state: "connected_seen",
            installed: true, connected: true, connectionUnverified: nil, configPath: nil,
            displayPath: nil, lastSeen: nil, activeSessions: 0, calls24h: 0, reloadHint: nil)
        r.profile = profile
        return r
    }

    private var roster: [ClientPresenceRecord] {
        [record("cursor", profile: "ro"), record("zed", profile: "rw")]
    }

    private func hides(_ filter: ScopeFilter, _ id: String, filtered: [String]? = nil) -> Bool {
        ClientsView.filterHidesTarget(filter: filter, targetID: id, filteredIDs: filtered, roster: roster)
    }

    func testFilterOnAnotherClientHidesTheTarget() {
        var f = ScopeFilter(); f.client = "zed"
        XCTAssertTrue(hides(f, "cursor"))
    }

    func testFilterOnTheTargetItselfDoesNotHideIt() {
        var f = ScopeFilter(); f.client = "cursor"
        XCTAssertFalse(hides(f, "cursor"))
    }

    func testProfileFilterThatIncludesTheTargetIsKept() {
        var f = ScopeFilter(); f.profile = "ro"
        XCTAssertFalse(hides(f, "cursor"))
        XCTAssertFalse(hides(f, "cursor", filtered: ["cursor"]))
    }

    func testProfileFilterThatExcludesTheTargetIsCleared() {
        var f = ScopeFilter(); f.profile = "ro"
        XCTAssertTrue(hides(f, "zed"))
        XCTAssertTrue(hides(f, "zed", filtered: ["cursor"]))
    }

    func testUnknownTargetIsNotHidden() {
        var f = ScopeFilter(); f.profile = "ro"
        XCTAssertFalse(hides(f, "ghost", filtered: []))
    }

    func testNoFilterNeverHides() {
        XCTAssertFalse(hides(ScopeFilter(), "cursor"))
        XCTAssertFalse(hides(ScopeFilter(), "zed", filtered: []))
    }
}
