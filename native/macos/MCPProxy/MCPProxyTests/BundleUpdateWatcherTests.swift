// BundleUpdateWatcherTests.swift
// MCPProxyTests
//
// Spec 092 FR-003 — a drag-install replaced the app bundle underneath the
// running process. Fixture-driven: each test builds a throwaway `.app`
// skeleton on disk (Contents/Info.plist and nothing else, which is all the
// detector reads) and, where it matters, REWRITES it mid-test to reproduce the
// replacement itself.

import XCTest
@testable import MCPProxy

final class BundleUpdateWatcherTests: XCTestCase {

    /// A minimal `.app` on disk. Only `Contents/Info.plist` exists — the
    /// detector must not need anything else.
    private func makeBundle(version: String?, name: String = "Fixture") throws -> String {
        let root = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("mcpproxy-bundle-\(UUID().uuidString.prefix(8))")
            .appendingPathComponent("\(name).app")
        try FileManager.default.createDirectory(
            at: root.appendingPathComponent("Contents"), withIntermediateDirectories: true
        )
        addTeardownBlock {
            try? FileManager.default.removeItem(at: root.deletingLastPathComponent())
        }
        try writeInfoPlist(version: version, into: root.path)
        return root.path
    }

    private func writeInfoPlist(version: String?, into bundlePath: String) throws {
        var plist: [String: Any] = ["CFBundleIdentifier": "com.smartmcpproxy.mcpproxy"]
        if let version { plist["CFBundleShortVersionString"] = version }
        let data = try PropertyListSerialization.data(
            fromPropertyList: plist, format: .xml, options: 0
        )
        try data.write(to: URL(fileURLWithPath: bundlePath)
            .appendingPathComponent("Contents/Info.plist"))
    }

    // MARK: - Reading the plist off disk

    func testReadsVersionFromTheOnDiskPlist() throws {
        let path = try makeBundle(version: "0.54.0")
        XCTAssertEqual(BundleUpdateWatcher.onDiskVersion(bundlePath: path), "0.54.0")
    }

    /// The whole mechanism depends on this: the SAME path answering differently
    /// after the bundle is replaced. `Bundle(path:)` caches and would keep
    /// returning the first answer forever.
    func testARewrittenPlistIsSeenAtTheSamePath() throws {
        let path = try makeBundle(version: "0.53.0")
        XCTAssertEqual(BundleUpdateWatcher.onDiskVersion(bundlePath: path), "0.53.0")

        try writeInfoPlist(version: "0.54.0", into: path)
        XCTAssertEqual(BundleUpdateWatcher.onDiskVersion(bundlePath: path), "0.54.0",
                       "a replaced bundle must be re-read, not served from a cache")
    }

    func testMissingOrUnreadableBundlesYieldNoVersion() throws {
        XCTAssertNil(BundleUpdateWatcher.onDiskVersion(bundlePath: "/nonexistent/Nope.app"))
        XCTAssertNil(BundleUpdateWatcher.onDiskVersion(bundlePath: try makeBundle(version: nil)),
                     "a plist without CFBundleShortVersionString is not a version")

        let corrupt = try makeBundle(version: "0.54.0")
        try "not a plist".write(
            toFile: (corrupt as NSString).appendingPathComponent("Contents/Info.plist"),
            atomically: true, encoding: .utf8
        )
        XCTAssertNil(BundleUpdateWatcher.onDiskVersion(bundlePath: corrupt))
    }

    // MARK: - When to offer a relaunch

    func testOffersOnlyWhenTheDiskVersionIsStrictlyNewer() {
        XCTAssertEqual(
            BundleUpdateWatcher.newerVersionOnDisk(runningVersion: "0.53.0", onDiskVersion: "0.54.0"),
            "0.54.0"
        )
        XCTAssertNil(
            BundleUpdateWatcher.newerVersionOnDisk(runningVersion: "0.54.0", onDiskVersion: "0.54.0"),
            "FR-005: equal versions must produce no prompt"
        )
        XCTAssertNil(
            BundleUpdateWatcher.newerVersionOnDisk(runningVersion: "0.54.0", onDiskVersion: "0.53.0"),
            "an older bundle on disk is a downgrade — never proposed automatically"
        )
    }

    /// FR-006 again: `rc.10` on disk over `rc.2` running is an upgrade, and the
    /// reverse is not.
    func testPrereleaseOrderingIsNumeric() {
        XCTAssertEqual(
            BundleUpdateWatcher.newerVersionOnDisk(
                runningVersion: "0.54.0-rc.2", onDiskVersion: "0.54.0-rc.10"),
            "0.54.0-rc.10"
        )
        XCTAssertNil(
            BundleUpdateWatcher.newerVersionOnDisk(
                runningVersion: "0.54.0-rc.10", onDiskVersion: "0.54.0-rc.2")
        )
        XCTAssertEqual(
            BundleUpdateWatcher.newerVersionOnDisk(
                runningVersion: "0.54.0-rc.10", onDiskVersion: "0.54.0"),
            "0.54.0", "the release supersedes its own release candidate"
        )
    }

    func testUnparseableVersionsNeverOfferARelaunch() {
        XCTAssertNil(BundleUpdateWatcher.newerVersionOnDisk(
            runningVersion: nil, onDiskVersion: "0.54.0"))
        XCTAssertNil(BundleUpdateWatcher.newerVersionOnDisk(
            runningVersion: "0.53.0", onDiskVersion: nil))
        XCTAssertNil(BundleUpdateWatcher.newerVersionOnDisk(
            runningVersion: "dev", onDiskVersion: "0.54.0"),
            "a dev build must not nag on every activation")
        XCTAssertNil(BundleUpdateWatcher.newerVersionOnDisk(
            runningVersion: "0.53.0", onDiskVersion: "SNAPSHOT"))
    }

    // MARK: - Production entry point

    /// The test runner is not an app bundle, so there is nothing to relaunch
    /// into — and the honest answer is "no offer", not a crash or a false
    /// positive on every timer tick.
    func testNoOfferWhenNotRunningFromAnAppBundle() {
        XCTAssertNil(BundleUpdateWatcher.replacementVersion(
            bundle: Bundle(for: BundleUpdateWatcherTests.self)))
    }
}
