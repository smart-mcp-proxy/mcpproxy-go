// AttentionParityTests.swift
// MCPProxyTests
//
// Spec 109-m SC-002 (T145, M6): the macOS leg of the attention parity chain.
// The REST golden `internal/runtime/testdata/attention_parity_rest.json`
// (written by the Go httpapi test from the real Compute + GET /attention
// handler) is decoded into AppState; Home's section model, the tray
// "Needs Attention (N)" group and the sidebar badge source must all show the
// ids Go computed, in the same order, with the same count.

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class AttentionParityTests: XCTestCase {

    private var testdataURL: URL {
        // native/macos/MCPProxy/MCPProxyTests/<this file> -> repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("internal/runtime/testdata")
    }

    private func golden() throws -> AttentionResponse {
        let data = try Data(contentsOf: testdataURL.appendingPathComponent("attention_parity_rest.json"))
        // The golden is the REST `data` minus generated_at (not deterministic).
        guard var object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            XCTFail("golden is not an object")
            throw CocoaError(.fileReadCorruptFile)
        }
        object["generated_at"] = "2026-10-02T12:00:00Z"
        return try JSONDecoder().decode(
            AttentionResponse.self, from: try JSONSerialization.data(withJSONObject: object))
    }

    private func expectedIDs() throws -> [String] {
        let data = try Data(contentsOf: testdataURL.appendingPathComponent("attention_parity_fixture.json"))
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        return try XCTUnwrap(object["expected_ids"] as? [String])
    }

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    func testDecodedGoldenHasTheGoComputedIDsAndCount() throws {
        let response = try golden()
        XCTAssertEqual(response.items.map(\.id), try expectedIDs())
        XCTAssertEqual(response.count, response.items.count)
    }

    func testHomeSectionAndSidebarBadgeReadTheSameOrderedList() throws {
        let appState = AppState()
        appState.coreState = .connected
        appState.updateAttention(try golden().items)

        XCTAssertEqual(appState.attention.map(\.id), try expectedIDs(), "Home section order")
        // MainWindow.badgeCount(for: .home) is appState.attention.count.
        XCTAssertEqual(appState.attention.count, try expectedIDs().count, "sidebar and Dock badge count")
    }

    func testTrayGroupListsTheSameItemsInTheSameOrder() throws {
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: CountingGlanceDataSource(), menuHost: host)
        controller.appState.coreState = .connected
        let response = try golden()
        controller.appState.updateAttention(response.items)
        controller.rebuildMenu()

        let parent = try XCTUnwrap((host.menu?.items ?? []).first { $0.title.hasPrefix("Needs Attention") })
        XCTAssertEqual(parent.title, "Needs Attention (\(response.items.count))")

        let submenu = try XCTUnwrap(parent.submenu)
        // Each row speaks the full summary (the visible title may be truncated).
        let spoken = submenu.items.compactMap { $0.accessibilityLabel() }
        XCTAssertEqual(spoken, response.items.map(\.summary), "tray row order")
    }
}
