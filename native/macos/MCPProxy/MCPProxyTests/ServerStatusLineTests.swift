// ServerStatusLineTests.swift
// MCPProxyTests
//
// Spec 109 FR-011 / FR-014 (PR 109-leftovers, T049; audit S4/S5):
// `ServerStatusLinePresentation` is the ONE place the macOS Servers row, the
// tray server submenu's first line and the Server Detail header turn a
// `ServerStatus` into visible status text. It reads the shared status label
// table (`HealthStatus.statusLabels`), so a connected-transport server that
// still needs sign-in never reads "Connected".

import XCTest
@testable import MCPProxy

final class ServerStatusLineTests: XCTestCase {

    // MARK: - Every derivation-table row

    func testEveryRowUsesTheSharedStatusLabelAndTone() throws {
        let wantTone: [String: ServerStatusTone] = [
            "ready": .success,
            "connecting": .neutral,
            "disabled": .neutral,
            "sign_in_required": .warning,
            "needs_review": .warning,
            "needs_secret": .warning,
            "needs_config": .warning,
            "error": .error,
        ]
        for row in HealthFixtureRows.all {
            let server = try Self.server(connected: true, healthJSON: row.json)
            let line = ServerStatusLinePresentation.line(for: server)
            XCTAssertEqual(line.label, row.wantLabel, row.name)
            XCTAssertEqual(line.tone, wantTone[row.wantStatus], row.name)
        }
    }

    // MARK: - The S4/S5 regression

    func testConnectedTransportThatNeedsSignInNeverReadsConnected() throws {
        let cases: [(String, String, String)] = [
            (#"{"level":"degraded","admin_state":"enabled","summary":"Sign-in required","action":"login","status":"sign_in_required","usable":false,"actions":["login"]}"#, "Sign-in required", "sign_in_required"),
            (#"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused","action":"restart","status":"error","usable":false,"actions":["restart"]}"#, "Error", "error"),
            (#"{"level":"unhealthy","admin_state":"enabled","summary":"Missing secret","action":"set_secret","status":"needs_secret","usable":false,"actions":["set_secret"]}"#, "Secret required", "needs_secret"),
        ]
        for (json, label, status) in cases {
            let server = try Self.server(connected: true, enabled: true, quarantined: false, healthJSON: json)
            let text = ServerStatusLinePresentation.text(for: server)
            XCTAssertEqual(ServerStatusLinePresentation.line(for: server).label, label, status)
            XCTAssertFalse(text.contains("Connected"), "\(status): row text '\(text)' must not say Connected")
        }
    }

    // MARK: - SC-003 forbidden words

    func testUnusableRowsNeverRenderHealthyOnlineConnected() throws {
        let forbidden = try NSRegularExpression(pattern: #"\b(healthy|online|connected)\b"#, options: [.caseInsensitive])
        for row in HealthFixtureRows.all where !row.wantUsable {
            let server = try Self.server(connected: true, healthJSON: row.json)
            let text = ServerStatusLinePresentation.text(for: server)
            let range = NSRange(text.startIndex..., in: text)
            XCTAssertNil(forbidden.firstMatch(in: text, range: range),
                         "\(row.name): '\(text)' must not say healthy/online/connected")
        }
    }

    // MARK: - Detail rule

    func testQuarantinedRowDoesNotRepeatTheAdminStateAsDetail() throws {
        let json = #"{"level":"healthy","admin_state":"quarantined","summary":"Quarantined for review","action":"approve","status":"needs_review","usable":false,"actions":["approve"]}"#
        let line = ServerStatusLinePresentation.line(for: try Self.server(connected: false, quarantined: true, healthJSON: json))
        XCTAssertEqual(line.label, "Needs review")
        XCTAssertNil(line.detail)
    }

    func testEnabledErrorKeepsItsSummaryAsDetail() throws {
        let json = #"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused","action":"restart","status":"error","usable":false,"actions":["restart"]}"#
        let server = try Self.server(connected: false, healthJSON: json)
        let line = ServerStatusLinePresentation.line(for: server)
        XCTAssertEqual(line.detail, "Connection refused")
        XCTAssertEqual(ServerStatusLinePresentation.text(for: server), "Error · Connection refused")
        XCTAssertEqual(line.tooltip, "Connection refused")
    }

    func testReadyKeepsItsSummaryAsDetail() throws {
        let json = #"{"level":"healthy","admin_state":"enabled","summary":"Connected (5 tools)","status":"ready","usable":true,"actions":[]}"#
        let line = ServerStatusLinePresentation.line(for: try Self.server(connected: true, healthJSON: json))
        XCTAssertEqual(line.label, "Online")
        XCTAssertEqual(line.detail, "Connected (5 tools)")
    }

    func testSummaryThatOnlyRestatesTheLabelIsNotRepeated() throws {
        let connecting = #"{"level":"healthy","admin_state":"enabled","summary":"Connecting...","status":"connecting","usable":false,"actions":[]}"#
        XCTAssertNil(ServerStatusLinePresentation.line(for: try Self.server(connected: false, healthJSON: connecting)).detail)
        let signIn = #"{"level":"degraded","admin_state":"enabled","summary":"Sign-in required","action":"login","status":"sign_in_required","usable":false,"actions":["login"]}"#
        XCTAssertNil(ServerStatusLinePresentation.line(for: try Self.server(connected: true, healthJSON: signIn)).detail)
    }

    func testTooltipPrefersDetailOverSummary() throws {
        let json = #"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused","detail":"dial tcp 127.0.0.1:1: connection refused","action":"restart","status":"error","usable":false,"actions":["restart"]}"#
        let line = ServerStatusLinePresentation.line(for: try Self.server(connected: false, healthJSON: json))
        XCTAssertEqual(line.tooltip, "dial tcp 127.0.0.1:1: connection refused")
    }

    func testUnknownStatusFallsBackToTheRawValueWithNeutralTone() throws {
        let json = #"{"level":"healthy","admin_state":"enabled","summary":"","status":"brand_new","usable":false,"actions":[]}"#
        let line = ServerStatusLinePresentation.line(for: try Self.server(connected: true, healthJSON: json))
        XCTAssertEqual(line.label, "brand_new")
        XCTAssertEqual(line.tone, .neutral)
    }

    // MARK: - Old core (no health vocabulary)

    func testOldCoreFallbacksUseLegacyWordsOnlyWithoutHealth() throws {
        let quarantined = try Self.server(connected: false, quarantined: true, healthJSON: nil)
        XCTAssertEqual(ServerStatusLinePresentation.line(for: quarantined).label, "Needs review")
        XCTAssertEqual(ServerStatusLinePresentation.line(for: quarantined).tone, .warning)

        let disabled = try Self.server(connected: false, enabled: false, healthJSON: nil)
        XCTAssertEqual(ServerStatusLinePresentation.line(for: disabled).label, "Disabled")
        XCTAssertEqual(ServerStatusLinePresentation.line(for: disabled).tone, .neutral)

        let connecting = try Self.server(connected: false, connecting: true, healthJSON: nil)
        XCTAssertEqual(ServerStatusLinePresentation.line(for: connecting).label, "Connecting")

        let connected = try Self.server(connected: true, healthJSON: nil)
        XCTAssertEqual(ServerStatusLinePresentation.line(for: connected).label, "Connected")
        XCTAssertEqual(ServerStatusLinePresentation.line(for: connected).tone, .success)

        let disconnected = try Self.server(connected: false, healthJSON: nil)
        XCTAssertEqual(ServerStatusLinePresentation.line(for: disconnected).label, "Disconnected")
    }

    func testOldCoreHealthWithoutStatusUsesItsSummary() throws {
        let json = #"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused"}"#
        let line = ServerStatusLinePresentation.line(for: try Self.server(connected: false, healthJSON: json))
        XCTAssertEqual(line.label, "Connection refused")
        XCTAssertNil(line.detail)
        XCTAssertEqual(line.tone, .error)
    }

    // MARK: - Row / tray / detail parity

    /// `text(for:)` is what the Servers row, the tray submenu and the detail
    /// header all render; it is exactly `line.label` plus the optional detail.
    func testTextIsLabelPlusDetail() throws {
        for row in HealthFixtureRows.all {
            let server = try Self.server(connected: true, healthJSON: row.json)
            let line = ServerStatusLinePresentation.line(for: server)
            let expected = line.detail.map { "\(line.label) · \($0)" } ?? line.label
            XCTAssertEqual(ServerStatusLinePresentation.text(for: server), expected, row.name)
        }
    }

    // MARK: - Source guard: no surface re-derives its own words

    func testSurfacesDoNotBypassTheStatusLine() throws {
        let servers = try source("MCPProxy/Views/ServersView.swift")
        let app = try source("MCPProxy/MCPProxyApp.swift")
        let detail = try source("MCPProxy/Views/ServerDetailView.swift")

        XCTAssertFalse(servers.contains("statusText = \"Connected\""))
        XCTAssertFalse(servers.contains("statusText = \"Quarantined\""))
        XCTAssertFalse(app.contains("health?.summary ?? (server.connected"))
        XCTAssertFalse(detail.contains("health?.summary ?? statusText"))
        XCTAssertTrue(servers.contains("ServerStatusLinePresentation"))
        XCTAssertTrue(app.contains("ServerStatusLinePresentation.text(for: server)"))
        XCTAssertTrue(detail.contains("ServerStatusLinePresentation.text(for: server)"))
    }

    // MARK: - Helpers

    private func source(_ relative: String) throws -> String {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()   // MCPProxyTests
            .deletingLastPathComponent()   // package root
        return try String(contentsOf: packageRoot.appendingPathComponent(relative), encoding: .utf8)
    }

    private static func server(connected: Bool,
                               enabled: Bool = true,
                               quarantined: Bool = false,
                               connecting: Bool = false,
                               healthJSON: String?) throws -> ServerStatus {
        let healthField = healthJSON.map { ", \"health\": \($0)" } ?? ""
        let json = """
        {
            "id": "srv", "name": "srv", "protocol": "http", "enabled": \(enabled),
            "connected": \(connected), "connecting": \(connecting), "quarantined": \(quarantined), "tool_count": 1
            \(healthField)
        }
        """.data(using: .utf8)!
        return try JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
