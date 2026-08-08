// StagedCoreBinaryTests.swift
// MCPProxyTests
//
// Spec 092 FR-030 — the legacy staged core copy at
// `~/Library/Application Support/mcpproxy/bin/mcpproxy`.
//
// The requirement is explicitly conservative ("never deleting a binary the
// user may manage themselves"), so the tests are mostly about what must NOT
// happen: no creation, no deletion, no touching a symlink, no action on a
// binary whose version cannot be established.

import XCTest
@testable import MCPProxy

final class StagedCoreBinaryDecisionTests: XCTestCase {

    private func decide(
        exists: Bool = true,
        isSymlink: Bool = false,
        staged: String? = "0.40.0",
        bundledPath: String? = "/Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy",
        bundled: String? = "0.54.0"
    ) -> StagedCoreBinary.Action {
        StagedCoreBinary.decide(
            stagedExists: exists, stagedIsSymlink: isSymlink, stagedVersion: staged,
            bundledPath: bundledPath, bundledVersion: bundled
        )
    }

    private func assertNoAction(
        _ action: StagedCoreBinary.Action, _ message: String,
        file: StaticString = #filePath, line: UInt = #line
    ) {
        guard case .none(let reason) = action else {
            return XCTFail("\(message) — got \(action)", file: file, line: line)
        }
        XCTAssertFalse(reason.isEmpty, "every no-action outcome must say why",
                       file: file, line: line)
    }

    func testAnOlderStagedCopyIsRefreshed() {
        guard case .refresh(_, let stale, let fresh) = decide() else {
            return XCTFail("a provably older staged copy is the one case that acts")
        }
        XCTAssertEqual(stale, "0.40.0")
        XCTAssertEqual(fresh, "0.54.0")
    }

    func testAbsentStagedCopyIsNotCreated() {
        assertNoAction(decide(exists: false),
                       "this tray must not become a writer of a legacy artifact")
    }

    func testSymlinkIsLeftAlone() {
        assertNoAction(decide(isSymlink: true),
                       "a symlink is deliberate wiring, not a stale copy")
    }

    func testUnknownVersionsMeanNoAction() {
        assertNoAction(decide(staged: nil), "a binary that will not answer is not provably stale")
        assertNoAction(decide(staged: ""), "an empty version is not a version")
        assertNoAction(decide(staged: "development"), "an unparseable version is not comparable")
        assertNoAction(decide(bundled: "dev"), "an unparseable bundled version is not comparable")
    }

    func testEqualOrNewerStagedCopyIsLeftAlone() {
        assertNoAction(decide(staged: "0.54.0"), "same version — nothing to refresh")
        assertNoAction(decide(staged: "0.55.0"), "a NEWER staged copy must never be overwritten")
        assertNoAction(decide(staged: "0.54.0", bundled: "0.54.0-rc.9"),
                       "a release staged copy outranks a bundled release candidate")
    }

    func testPrereleaseOrderingIsNumericHereToo() {
        guard case .refresh = decide(staged: "0.54.0-rc.2", bundled: "0.54.0-rc.10") else {
            return XCTFail("rc.2 is older than rc.10 and must be refreshed")
        }
        assertNoAction(decide(staged: "0.54.0-rc.10", bundled: "0.54.0-rc.2"),
                       "rc.10 is NEWER than rc.2 — a string comparison would overwrite it")
    }

    func testNoBundledCoreMeansNoAction() {
        assertNoAction(decide(bundledPath: nil), "nothing to refresh from")
        assertNoAction(decide(bundled: nil), "nothing to refresh from")
    }

    func testDefaultPathMatchesTheLegacyTrayLayout() {
        XCTAssertEqual(
            StagedCoreBinary.defaultPath(home: "/Users/someone"),
            "/Users/someone/Library/Application Support/mcpproxy/bin/mcpproxy"
        )
    }
}

/// The file-system half: the refresh must swap the file, keep its mode, and
/// leave no temp file behind.
final class StagedCoreBinaryRefreshTests: XCTestCase {

    private var directory: URL!

    override func setUpWithError() throws {
        directory = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("mcpproxy-staged-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
    }

    /// A script that reports `version` and can be identified by a marker line.
    @discardableResult
    private func makeCore(named name: String, version: String, mode: Int16 = 0o755) throws -> String {
        let path = directory.appendingPathComponent(name).path
        try """
        #!/bin/sh
        # marker:\(name)
        [ "$1" = version ] || exit 2
        echo '{"version":"\(version)"}'
        """.write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes(
            [.posixPermissions: NSNumber(value: mode)], ofItemAtPath: path
        )
        return path
    }

    private func mode(of path: String) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: path)
        return (attributes[.posixPermissions] as! NSNumber).intValue
    }

    func testStaleStagedCopyIsReplacedByTheBundledOne() throws {
        let bundled = try makeCore(named: "bundled", version: "0.54.0")
        let staged = try makeCore(named: "staged", version: "0.40.0", mode: 0o700)

        let action = StagedCoreBinary.refreshIfStale(bundledPath: bundled, stagedPath: staged)
        guard case .refresh = action else { return XCTFail("expected a refresh, got \(action)") }

        XCTAssertEqual(CoreBinaryVersion.read(at: staged), "0.54.0",
                       "the staged path must now run the bundled core")
        XCTAssertEqual(try mode(of: staged), 0o700,
                       "the existing mode is preserved, not reset to the source's")

        let leftovers = try FileManager.default.contentsOfDirectory(atPath: directory.path)
            .filter { $0.contains(".new-") }
        XCTAssertTrue(leftovers.isEmpty, "the staging file must not survive: \(leftovers)")
    }

    func testCurrentStagedCopyIsUntouched() throws {
        let bundled = try makeCore(named: "bundled", version: "0.54.0")
        let staged = try makeCore(named: "staged", version: "0.54.0")
        let before = try Data(contentsOf: URL(fileURLWithPath: staged))

        let action = StagedCoreBinary.refreshIfStale(bundledPath: bundled, stagedPath: staged)
        guard case .none = action else { return XCTFail("expected no action, got \(action)") }
        XCTAssertEqual(try Data(contentsOf: URL(fileURLWithPath: staged)), before)
    }

    /// The requirement's hard line: nothing is ever removed.
    func testAnUnidentifiableStagedFileIsNeitherRefreshedNorRemoved() throws {
        let bundled = try makeCore(named: "bundled", version: "0.54.0")
        let staged = directory.appendingPathComponent("staged").path
        try "not a core at all".write(toFile: staged, atomically: true, encoding: .utf8)

        let action = StagedCoreBinary.refreshIfStale(bundledPath: bundled, stagedPath: staged)
        guard case .none = action else { return XCTFail("expected no action, got \(action)") }
        XCTAssertTrue(FileManager.default.fileExists(atPath: staged),
                      "FR-030: never delete a binary whose provenance is unproven")
        XCTAssertEqual(try String(contentsOfFile: staged, encoding: .utf8), "not a core at all")
    }

    func testMissingStagedCopyIsNotCreated() throws {
        let bundled = try makeCore(named: "bundled", version: "0.54.0")
        let staged = directory.appendingPathComponent("absent").path

        let action = StagedCoreBinary.refreshIfStale(bundledPath: bundled, stagedPath: staged)
        guard case .none = action else { return XCTFail("expected no action, got \(action)") }
        XCTAssertFalse(FileManager.default.fileExists(atPath: staged))
    }

    func testSymlinkedStagedPathIsLeftPointingWhereItPointed() throws {
        let bundled = try makeCore(named: "bundled", version: "0.54.0")
        let real = try makeCore(named: "user-managed", version: "0.10.0")
        let staged = directory.appendingPathComponent("staged").path
        try FileManager.default.createSymbolicLink(atPath: staged, withDestinationPath: real)

        let action = StagedCoreBinary.refreshIfStale(bundledPath: bundled, stagedPath: staged)
        guard case .none = action else { return XCTFail("expected no action, got \(action)") }
        XCTAssertEqual(try FileManager.default.destinationOfSymbolicLink(atPath: staged), real)
        XCTAssertEqual(CoreBinaryVersion.read(at: real), "0.10.0", "the target is untouched")
    }
}
