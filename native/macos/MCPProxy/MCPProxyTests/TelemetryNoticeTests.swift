// TelemetryNoticeTests.swift
// MCPProxyTests
//
// Spec 109 FR-044a (codex first-run user test F-03): the macOS leg of the
// effective-telemetry-state chain. The shared fixture
// internal/telemetry/testdata/effective_state_cases.json also drives the Go
// resolver test and the vitest helper test, so the Swift env port and the copy
// cannot drift from them.

import AppKit
import SwiftUI
import XCTest
@testable import MCPProxy

final class TelemetryNoticeTests: XCTestCase {

    private struct Case: Decodable {
        struct Want: Decodable {
            let enabled: Bool
            let source: String
            let disabledBy: String?
            enum CodingKeys: String, CodingKey {
                case enabled, source
                case disabledBy = "disabled_by"
            }
        }
        let name: String
        let env: [String: String]
        let want: Want
        let notice: String
        let offLine: String?
        let settingLock: String?
        enum CodingKeys: String, CodingKey {
            case name, env, want, notice
            case offLine = "off_line"
            case settingLock = "setting_lock"
        }
    }

    private func cases() throws -> [Case] {
        // native/macos/MCPProxy/MCPProxyTests/<this file> -> repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        url.appendPathComponent("internal/telemetry/testdata/effective_state_cases.json")
        struct Doc: Decodable { let cases: [Case] }
        let doc = try JSONDecoder().decode(Doc.self, from: Data(contentsOf: url))
        XCTAssertGreaterThan(doc.cases.count, 5)
        return doc.cases
    }

    private func dto(_ want: Case.Want) -> TelemetryStateDTO {
        TelemetryStateDTO(enabled: want.enabled, source: want.source, disabledBy: want.disabledBy)
    }

    func testEnvResolutionMatchesGoFixture() throws {
        for c in try cases() {
            let got = TelemetryNotice.envDisabledReason(environment: c.env)
            let expected = c.want.source == "env" ? c.want.disabledBy : nil
            XCTAssertEqual(got, expected, c.name)
        }
    }

    func testNoticeModeAndCopyMatchFixture() throws {
        for c in try cases() {
            let state = dto(c.want)
            XCTAssertEqual(TelemetryNotice.mode(state: state).rawValue, c.notice, c.name)
            if let line = c.offLine {
                XCTAssertEqual(TelemetryNotice.offLine(state: state), line, c.name)
            }
            XCTAssertEqual(TelemetryNotice.settingLock(state: state), c.settingLock, c.name)
        }
    }

    func testUnknownStateKeepsTheStandardNotice() {
        XCTAssertEqual(TelemetryNotice.mode(state: nil), .notice)
        XCTAssertNil(TelemetryNotice.settingLock(state: nil))
    }

    func testFirstRunUsesTheAppEnvironment() {
        XCTAssertEqual(
            FirstRunTelemetryNotice.resolve(environment: ["MCPPROXY_TELEMETRY": "false"]),
            .offEnv("MCPPROXY_TELEMETRY=false"))
        XCTAssertEqual(FirstRunTelemetryNotice.resolve(environment: [:]), .notice)
    }

    @MainActor
    func testFirstRunDialogTextFollowsTheNotice() {
        let off = FirstRunDialog(
            launchAtLogin: .constant(true),
            telemetryNotice: .offEnv("MCPPROXY_TELEMETRY=false"),
            onContinue: {})
        XCTAssertEqual(
            off.telemetryText,
            "Anonymous usage telemetry is off — disabled by MCPPROXY_TELEMETRY=false in the environment. Nothing is sent.")
        XCTAssertFalse(off.telemetryText.contains("sends anonymous usage statistics"))

        let standard = FirstRunDialog(launchAtLogin: .constant(true), onContinue: {})
        XCTAssertTrue(standard.telemetryText.contains("sends anonymous usage statistics"))
    }

    /// The dialog must still fit its computed window with the off line.
    @MainActor
    func testTheOffLineDialogFitsItsComputedWindowSize() {
        let host = NSHostingController(
            rootView: FirstRunDialog(
                launchAtLogin: .constant(true), telemetryNotice: .offEnv("MCPPROXY_TELEMETRY=false"),
                onContinue: {}))
        host.view.layoutSubtreeIfNeeded()
        let fitting = host.view.fittingSize.height
        XCTAssertGreaterThan(fitting, 0)
        XCTAssertGreaterThanOrEqual(firstRunDialogContentSize(fittingHeight: fitting).height, fitting)
    }

    func testStatusDecodesTelemetry() throws {
        let withBlock = Data("""
        {"running":true,"telemetry":{"enabled":false,"source":"env","disabled_by":"DO_NOT_TRACK"}}
        """.utf8)
        let status = try JSONDecoder().decode(StatusResponse.self, from: withBlock)
        XCTAssertEqual(status.telemetry,
                       TelemetryStateDTO(enabled: false, source: "env", disabledBy: "DO_NOT_TRACK"))

        let without = Data(#"{"running":true}"#.utf8)
        XCTAssertNil(try JSONDecoder().decode(StatusResponse.self, from: without).telemetry)

        let config = Data(#"{"running":true,"telemetry":{"enabled":true,"source":"default"}}"#.utf8)
        let decoded = try JSONDecoder().decode(StatusResponse.self, from: config)
        XCTAssertEqual(decoded.telemetry?.enabled, true)
        XCTAssertNil(decoded.telemetry?.disabledBy)
    }
}
