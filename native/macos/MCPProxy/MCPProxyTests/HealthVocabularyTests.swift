import XCTest
@testable import MCPProxy

/// T044 (Spec 109 FR-010–012, FR-014 labels): decode the new
/// `status`/`usable`/`actions` fields, verify the row/tray label and color
/// for each status, and guard against `level` leaking as rendered text
/// (Spec 109 SC-003).
final class HealthVocabularyTests: XCTestCase {

    private func decode(_ jsonString: String) throws -> HealthStatus {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(HealthStatus.self, from: data)
    }

    // MARK: - Decoding

    func testDecodesStatusUsableActions() throws {
        let json = """
        {
            "level": "degraded",
            "admin_state": "enabled",
            "summary": "Sign-in required",
            "action": "login",
            "status": "sign_in_required",
            "usable": false,
            "actions": ["login"]
        }
        """
        let health = try decode(json)
        XCTAssertEqual(health.status, "sign_in_required")
        XCTAssertEqual(health.usable, false)
        XCTAssertEqual(health.actions, ["login"])
        XCTAssertEqual(health.action, health.actions?.first, "action must equal actions[0]")
    }

    func testToleratesMissingNewFields() throws {
        // An older core payload without status/usable/actions must still decode.
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "Connected"}
        """
        let health = try decode(json)
        XCTAssertNil(health.status)
        XCTAssertNil(health.usable)
        XCTAssertNil(health.actions)
        // Falls back to the pre-Spec-109 reading.
        XCTAssertTrue(health.isUsable)
    }

    func testOldCorePayloadConnectingIsNotUsable() throws {
        // A pre-Spec-109 core's "connecting"/"idle" branch
        // (internal/health/calculator.go) reported a mid-connect server as
        // level=healthy/admin_state=enabled with this exact summary — the same
        // shape a fully connected, usable server reports. A naive
        // level+adminState fallback would call it usable when it cannot yet
        // serve tool calls.
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "Connecting..."}
        """
        let health = try decode(json)
        XCTAssertNil(health.usable)
        XCTAssertFalse(health.isUsable, "a mid-connect server must not fall back to usable")
    }

    /// Review finding (Spec 109 round 2): ServerDetailView's "Suggested
    /// Action" row gated on `health.actions` alone, so an old-core payload
    /// with only the legacy singular `action` field (no `actions`) dropped
    /// the row entirely — a regression from before this PR, and inconsistent
    /// with `isUsable`'s own old-core tolerance just above.
    func testActionsOrLegacyFallbackUsesLegacyActionWhenActionsIsAbsent() throws {
        let json = """
        {"level": "unhealthy", "admin_state": "enabled", "summary": "Missing secret", "action": "set_secret"}
        """
        let health = try decode(json)
        XCTAssertNil(health.actions)
        XCTAssertEqual(health.actionsOrLegacyFallback, ["set_secret"])
    }

    func testActionsOrLegacyFallbackPrefersActionsWhenPresent() throws {
        let json = """
        {
            "level": "unhealthy", "admin_state": "enabled", "summary": "Missing secret",
            "action": "set_secret", "status": "needs_secret", "usable": false,
            "actions": ["set_secret"]
        }
        """
        let health = try decode(json)
        XCTAssertEqual(health.actionsOrLegacyFallback, ["set_secret"])
    }

    func testActionsOrLegacyFallbackIsEmptyWhenNeitherIsPresent() throws {
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "Connected"}
        """
        let health = try decode(json)
        XCTAssertEqual(health.actionsOrLegacyFallback, [])
    }

    // MARK: - Every derivation-table row (mirrors internal/health/status_test.go, T041)

    private struct Row {
        let name: String
        let json: String
        let wantStatus: String
        let wantUsable: Bool
        let wantLabel: String
    }

    private let rows: [Row] = [
        Row(name: "disabled",
            json: #"{"level":"healthy","admin_state":"disabled","summary":"Disabled","action":"enable","status":"disabled","usable":false,"actions":["enable"]}"#,
            wantStatus: "disabled", wantUsable: false, wantLabel: "Disabled"),
        Row(name: "quarantined + OAuth login required",
            json: #"{"level":"degraded","admin_state":"quarantined","summary":"Sign-in required","action":"login","status":"sign_in_required","usable":false,"actions":["login","approve"]}"#,
            wantStatus: "sign_in_required", wantUsable: false, wantLabel: "Sign-in required"),
        Row(name: "quarantined transport fault",
            json: #"{"level":"unhealthy","admin_state":"quarantined","summary":"Quarantined — Connection refused","action":"approve","status":"error","usable":false,"actions":["approve","view_logs"]}"#,
            wantStatus: "error", wantUsable: false, wantLabel: "Error"),
        Row(name: "quarantined otherwise",
            json: #"{"level":"healthy","admin_state":"quarantined","summary":"Quarantined for review","action":"approve","status":"needs_review","usable":false,"actions":["approve"]}"#,
            wantStatus: "needs_review", wantUsable: false, wantLabel: "Needs review"),
        Row(name: "missing secret",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"Missing secret","action":"set_secret","status":"needs_secret","usable":false,"actions":["set_secret"]}"#,
            wantStatus: "needs_secret", wantUsable: false, wantLabel: "Secret required"),
        Row(name: "config error",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"OAuth configuration error","action":"configure","status":"needs_config","usable":false,"actions":["configure"]}"#,
            wantStatus: "needs_config", wantUsable: false, wantLabel: "Needs configuration"),
        Row(name: "connecting",
            json: #"{"level":"healthy","admin_state":"enabled","summary":"Connecting...","status":"connecting","usable":false,"actions":[]}"#,
            wantStatus: "connecting", wantUsable: false, wantLabel: "Connecting"),
        Row(name: "connection error",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused","action":"restart","status":"error","usable":false,"actions":["restart","view_logs"]}"#,
            wantStatus: "error", wantUsable: false, wantLabel: "Error"),
        Row(name: "ready, token refresh retrying (still usable)",
            json: #"{"level":"degraded","admin_state":"enabled","summary":"Token refresh pending","action":"view_logs","status":"ready","usable":true,"actions":["view_logs"]}"#,
            wantStatus: "ready", wantUsable: true, wantLabel: "Online"),
        Row(name: "connected healthy",
            json: #"{"level":"healthy","admin_state":"enabled","summary":"Connected (5 tools)","status":"ready","usable":true,"actions":[]}"#,
            wantStatus: "ready", wantUsable: true, wantLabel: "Online"),
    ]

    func testEveryRow_DecodesAndLabels() throws {
        for row in rows {
            let health = try decode(row.json)
            XCTAssertEqual(health.status, row.wantStatus, row.name)
            XCTAssertEqual(health.usable, row.wantUsable, row.name)
            XCTAssertEqual(health.statusLabel, row.wantLabel, row.name)
            XCTAssertEqual(health.isUsable, row.wantUsable, row.name)

            // action == actions.first, or absent when actions is empty.
            let actions = health.actions ?? []
            if actions.isEmpty {
                XCTAssertTrue(health.action == nil || health.action == "", row.name)
            } else {
                XCTAssertEqual(health.action, actions.first, row.name)
            }
        }
    }

    // MARK: - Forbidden renderings (SC-003)

    /// For every row where `usable == false`, the status label must never say
    /// the server is healthy/online/connected.
    func testForbiddenWordsForUnusableRows() throws {
        let forbidden = ["healthy", "Healthy", "online", "Online", "connected", "Connected"]
        for row in rows where !row.wantUsable {
            let health = try decode(row.json)
            for word in forbidden {
                XCTAssertFalse(health.statusLabel.contains(word),
                                "\(row.name): status label '\(health.statusLabel)' must not contain '\(word)'")
            }
        }
    }

    // MARK: - Action labels

    func testActionLabelsCoverEveryAction() {
        let actions = ["login", "set_secret", "configure", "edit_url", "approve", "restart", "view_logs", "enable"]
        for action in actions {
            XCTAssertNotNil(HealthStatus.actionLabels[action], "no label for action \(action)")
            XCTAssertFalse(HealthStatus.actionLabels[action]!.isEmpty)
        }
    }

    func testStatusLabelsCoverEveryStatus() {
        let statuses = ["ready", "connecting", "sign_in_required", "needs_review", "needs_secret", "needs_config", "error", "disabled"]
        for status in statuses {
            XCTAssertNotNil(HealthStatus.statusLabels[status], "no label for status \(status)")
            XCTAssertFalse(HealthStatus.statusLabels[status]!.isEmpty)
        }
    }
}
