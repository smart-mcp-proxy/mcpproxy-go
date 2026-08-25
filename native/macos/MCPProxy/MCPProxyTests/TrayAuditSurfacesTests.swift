// TrayAuditSurfacesTests.swift
// MCPProxyTests
//
// The window- and settings-side fixes from the 2026-08 macOS tray UX audit.
// These surfaces cannot be walked by `mcpproxy-ui-test` (it reads the status
// bar menu, and window-level accessibility is unavailable while the screen is
// locked), so their contracts are pinned here instead:
//
//   F5  Agent Tokens is reachable
//   F6  the three Web-UI-only settings are in the native catalogue
//   F13 placeholders and steps match the Web UI
//   F14 the About panel carries a way to report something
//   F16 the Tools view assembles server:tool from the bare REST name

import XCTest
@testable import MCPProxy

final class TrayAuditSurfacesTests: XCTestCase {

    private var allFields: [ConfigField] {
        SettingsCatalog.security
            + SettingsCatalog.general
            + SettingsCatalog.advanced.flatMap(\.fields)
    }

    private func field(_ key: String) throws -> ConfigField {
        try XCTUnwrap(allFields.first { $0.key == key },
                      "\(key) is not in SettingsCatalog — it is unreachable from the tray, "
                      + "whose Raw tab is read-only")
    }

    // MARK: - F6 · The settings that were web-only

    func testDeepScanIsEditableFromTheTray() throws {
        let deepScan = try field("security.deep_scan.enabled")
        XCTAssertEqual(deepScan.control, .toggle)
        XCTAssertNotNil(deepScan.docs, "the Docker-scanner opt-in needs its doc link")
    }

    func testServerInstructionsAreEditableFromTheTray() throws {
        let instructions = try field("instructions")
        XCTAssertEqual(instructions.control, .textarea, "a paragraph is not a 240pt trailing field")
        XCTAssertTrue(instructions.optional, "blank means: use the built-in default")
        XCTAssertNotNil(instructions.placeholder)
    }

    func testCodeExecutionParallelismIsEditableFromTheTray() throws {
        let parallel = try field("code_execution_max_parallel")
        XCTAssertEqual(parallel.control, .number)
        XCTAssertEqual(parallel.min, 1)
        XCTAssertEqual(parallel.max, 32)
    }

    /// The catalogue's own header claims it mirrors fields.ts 1:1 — this is the
    /// in-suite half of that claim (scripts/check-settings-parity.py is the
    /// cross-file gate).
    func testNoFieldIsDeclaredTwice() {
        let keys = allFields.map(\.key)
        XCTAssertEqual(Set(keys).count, keys.count,
                       "duplicate key(s): \(keys.filter { k in keys.filter { $0 == k }.count > 1 })")
    }

    // MARK: - F13 · Copy and control drift

    func testDockerFieldsCarryTheirExampleValues() throws {
        XCTAssertEqual(try field("docker_isolation.memory_limit").placeholder, "512m")
        XCTAssertEqual(try field("docker_isolation.cpu_limit").placeholder, "1.0")
        XCTAssertEqual(try field("docker_isolation.registry").placeholder, "docker.io")
    }

    /// Both are fractional in practice (entropy 4.5, "warn 1.5h before"), and
    /// without a step they could only be typed, never nudged.
    func testFractionalNumbersHaveAStep() throws {
        XCTAssertEqual(try field("sensitive_data_detection.entropy_threshold").step, 0.1)
        XCTAssertEqual(try field("oauth_expiry_warning_hours").step, 0.5)
    }

    func testTelemetryCopyMatchesTheWebUIRevision() throws {
        let telemetry = try field("telemetry.enabled")
        XCTAssertTrue(telemetry.help?.contains("single anonymous opt-out signal") == true,
                      "the tray's wording was a revision behind: \(telemetry.help ?? "nil")")
        XCTAssertTrue(telemetry.dangerMessage?.contains("opt-out signal") == true)
    }

    // MARK: - F14 · A way to report something

    func testTheAboutPanelOffersTheIssueTracker() {
        let labels = AboutPanelLinks.all.map(\.label)
        XCTAssertTrue(labels.contains("Report an Issue"), "\(labels)")
        XCTAssertTrue(labels.contains("Discussions"))
        XCTAssertEqual(AboutPanelLinks.all.first { $0.label == "Report an Issue" }?.url,
                       ProjectLinks.issues)
    }

    func testEveryAboutLinkIsDistinct() {
        let urls = AboutPanelLinks.all.map(\.url)
        XCTAssertEqual(Set(urls).count, urls.count)
    }

    // MARK: - F16 · Tool identity in the Tools view

    /// REST `/index/search` returns the BARE name plus `server_name` (#871).
    /// The view must assemble the canonical id rather than trust `name`.
    func testToolRowAssemblesTheCanonicalIdentity() {
        let row = ToolsView.ToolRow(server: "github", name: "create_issue",
                                    description: "Open an issue", score: 4.2)
        XCTAssertEqual(row.qualified, "github:create_issue")
        XCTAssertEqual(row.id, "github:create_issue")
    }

    func testAToolWithNoServerStillHasAUsableIdentity() {
        let row = ToolsView.ToolRow(server: "", name: "retrieve_tools",
                                    description: "", score: nil)
        XCTAssertEqual(row.qualified, "retrieve_tools",
                       "a leading colon is not an identity")
    }

    // MARK: - Query encoding (cross-model review, round 1)

    /// `.urlQueryAllowed` — the obvious choice, and the one this code used —
    /// deliberately permits `&`, `=`, `+` and `?`, so a search for
    /// "a&limit=1" would have become a second parameter.
    func testQueryValuesEscapeParameterDelimiters() {
        XCTAssertEqual(APIClient.escapeQueryValue("a&limit=1"), "a%26limit%3D1")
        XCTAssertEqual(APIClient.escapeQueryValue("git hub"), "git%20hub")
        XCTAssertEqual(APIClient.escapeQueryValue("a+b"), "a%2Bb", "a raw + decodes as a space")
        XCTAssertEqual(APIClient.escapeQueryValue("sess-1_a.b~c"), "sess-1_a.b~c",
                       "unreserved characters must survive untouched")
    }

    // MARK: - F10 · The glance hand-off has its own channel

    func testTheActivityFilterNotificationIsItsOwnChannel() {
        XCTAssertNotEqual(Notification.Name.activityFilter, .switchToActivity)
        XCTAssertNotEqual(Notification.Name.activityFilter, .switchToSidebarTab)
    }
}
