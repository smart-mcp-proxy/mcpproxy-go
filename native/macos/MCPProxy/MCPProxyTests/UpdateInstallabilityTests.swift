// UpdateInstallabilityTests.swift
// MCPProxyTests
//
// Spec 092 FR-016 — the three ways an in-place update is impossible, and the
// requirement that each one produces an explanation plus a fallback rather
// than a silent no-op.

import XCTest
@testable import MCPProxy

final class UpdateInstallabilityTests: XCTestCase {

    private func evaluate(
        _ path: String,
        readOnly: Bool = false,
        writable: Bool = true
    ) -> UpdateBlockedReason? {
        UpdateInstallability.evaluate(
            bundleURL: URL(fileURLWithPath: path),
            isReadOnlyVolume: { _ in readOnly },
            isWritable: { _ in writable }
        )
    }

    func testANormalApplicationsInstallIsNotBlocked() {
        XCTAssertNil(evaluate("/Applications/MCPProxy.app"))
    }

    func testTranslocatedAppIsBlockedWithAMoveFallback() throws {
        let reason = try XCTUnwrap(evaluate(
            "/private/var/folders/ab/xyz/X/AppTranslocation/1234-ABCD/d/MCPProxy.app"
        ))
        XCTAssertTrue(reason.menuTitle.contains("Applications"),
                      "the menu title must carry the action, not just the diagnosis")
        XCTAssertFalse(reason.explanation.isEmpty)
        XCTAssertTrue(reason.fallback.contains("Applications"))
        XCTAssertTrue(reason.message.contains(reason.fallback))
    }

    func testReadOnlyVolumeIsBlocked() throws {
        let reason = try XCTUnwrap(evaluate("/Volumes/MCPProxy/MCPProxy.app", readOnly: true))
        XCTAssertTrue(reason.explanation.contains("/Volumes/MCPProxy/MCPProxy.app"),
                      "name the path the user has to act on")
        XCTAssertFalse(reason.fallback.isEmpty)
    }

    func testUnwritableParentIsBlockedAndNamesTheDirectory() throws {
        let reason = try XCTUnwrap(evaluate("/Applications/MCPProxy.app", writable: false))
        XCTAssertTrue(reason.explanation.contains("/Applications"))
        XCTAssertTrue(reason.fallback.contains("~/Applications"),
                      "FR-016 asks for a fallback path, not just a refusal")
    }

    func testTranslocationOutranksTheOtherChecks() throws {
        // A translocated app is also on a read-only volume; the translocation
        // message is the one that tells the user what actually happened.
        let reason = try XCTUnwrap(evaluate(
            "/private/var/folders/ab/AppTranslocation/1/d/MCPProxy.app",
            readOnly: true, writable: false
        ))
        XCTAssertTrue(reason.explanation.contains("temporary"))
    }

    func testAFailingVolumeProbeDoesNotManufactureABlock() {
        // The real probe answers "not read-only" when it cannot read the
        // resource values — a failed probe must not hide a working updater.
        let url = URL(fileURLWithPath: "/nonexistent-\(UUID().uuidString)/MCPProxy.app")
        XCTAssertFalse(UpdateInstallability.volumeIsReadOnly(url))
    }
}
