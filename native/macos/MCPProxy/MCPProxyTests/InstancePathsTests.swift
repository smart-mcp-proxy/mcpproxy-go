import XCTest
@testable import MCPProxy

/// Where a tray instance keeps its state, and how a dev/QA instance is pointed
/// somewhere else (GH #936).
///
/// The tray resolves everything under `homeDirectoryForCurrentUser`, which
/// IGNORES `$HOME` — so a build run out of a scratch bundle still read and wrote
/// the real `~/.mcpproxy`, and the autostart sidecar silently overwrote the
/// user's login-item state with the scratch bundle's. There is no way to test
/// "does not touch real user state" by running the app, so the resolution rules
/// are pinned here instead: every one of them takes its environment and home as
/// parameters precisely so a test can supply them.
final class InstancePathsTests: XCTestCase {

    private let home = URL(fileURLWithPath: "/Users/tester", isDirectory: true)

    // MARK: - Default (no override): behaviour is exactly what it always was

    func testWithoutTheOverrideEverythingResolvesUnderTheRealHome() {
        let env: [String: String] = [:]
        XCTAssertEqual(InstancePaths.root(environment: env, home: home).path,
                       "/Users/tester/.mcpproxy")
        XCTAssertEqual(InstancePaths.socketPath(environment: env, home: home),
                       "/Users/tester/.mcpproxy/mcpproxy.sock")
        XCTAssertEqual(InstancePaths.autostartSidecarURL(environment: env, home: home).path,
                       "/Users/tester/.mcpproxy/tray-autostart.json")
        XCTAssertEqual(InstancePaths.configFileURL(environment: env, home: home).path,
                       "/Users/tester/.mcpproxy/mcp_config.json")
        XCTAssertEqual(InstancePaths.dataLockURL(environment: env, home: home).path,
                       "/Users/tester/.mcpproxy/config.db")
        XCTAssertFalse(InstancePaths.isOverridden(environment: env))
    }

    /// An empty or whitespace-only value is not an override. A launcher that
    /// exports the variable unset must not silently relocate the instance to
    /// the filesystem root.
    func testABlankOverrideIsNoOverride() {
        for blank in ["", "   ", "\n"] {
            let env = [InstancePaths.rootEnvVar: blank]
            XCTAssertEqual(InstancePaths.root(environment: env, home: home).path,
                           "/Users/tester/.mcpproxy",
                           "\(blank.debugDescription) must not be treated as a root")
            XCTAssertFalse(InstancePaths.isOverridden(environment: env))
        }
    }

    // MARK: - The override moves everything at once

    func testTheRootOverrideRelocatesEveryInstanceFile() {
        let env = [InstancePaths.rootEnvVar: "/tmp/qa936"]
        XCTAssertEqual(InstancePaths.root(environment: env, home: home).path, "/tmp/qa936")
        XCTAssertEqual(InstancePaths.socketPath(environment: env, home: home),
                       "/tmp/qa936/mcpproxy.sock")
        XCTAssertEqual(InstancePaths.autostartSidecarURL(environment: env, home: home).path,
                       "/tmp/qa936/tray-autostart.json",
                       "the sidecar is the file that silently overwrote real user state")
        XCTAssertEqual(InstancePaths.configFileURL(environment: env, home: home).path,
                       "/tmp/qa936/mcp_config.json")
        XCTAssertEqual(InstancePaths.dataLockURL(environment: env, home: home).path,
                       "/tmp/qa936/config.db")
        XCTAssertTrue(InstancePaths.isOverridden(environment: env))
    }

    /// A relative or `~`-prefixed value is resolved, not passed through: a
    /// socket path the tray and the core disagree about is worse than none.
    func testTheOverrideIsExpandedAndStandardised() {
        let env = [InstancePaths.rootEnvVar: "~/scratch/qa"]
        XCTAssertEqual(InstancePaths.root(environment: env, home: home).path,
                       "\(NSHomeDirectory())/scratch/qa",
                       "tilde expansion goes through the same rule everything else does")

        let trailing = [InstancePaths.rootEnvVar: "/tmp/qa936/"]
        XCTAssertEqual(InstancePaths.root(environment: trailing, home: home).path, "/tmp/qa936")
    }

    // MARK: - The socket override still wins

    /// `MCPPROXY_SOCKET_PATH` shipped first and points at ONE core, which is
    /// not always the core that owns the root (attaching to a core somebody
    /// else started is the whole point of it). It therefore outranks the root.
    func testAnExplicitSocketPathOutranksTheRoot() {
        let env = [
            InstancePaths.rootEnvVar: "/tmp/qa936",
            InstancePaths.socketPathEnvVar: "/tmp/other/mcpproxy.sock"
        ]
        XCTAssertEqual(InstancePaths.socketPath(environment: env, home: home),
                       "/tmp/other/mcpproxy.sock")
        XCTAssertEqual(InstancePaths.autostartSidecarURL(environment: env, home: home).path,
                       "/tmp/qa936/tray-autostart.json",
                       "…and moves nothing else")
    }

    // MARK: - The one failure mode that is silent

    /// `sockaddr_un.sun_path` is 104 bytes. Over that the bind simply fails and
    /// a QA run spends an hour wondering why the tray never sees its core, so
    /// the tray says so instead.
    func testAnOverlongSocketPathIsReported() {
        let deep = "/tmp/" + String(repeating: "a", count: 120)
        let env = [InstancePaths.rootEnvVar: deep]
        let path = InstancePaths.socketPath(environment: env, home: home)
        guard let warning = InstancePaths.socketPathProblem(path) else {
            return XCTFail("a \(path.utf8.count)-byte socket path cannot be bound and must warn")
        }
        XCTAssertTrue(warning.contains("\(path.utf8.count)"), "the warning names the length: \(warning)")

        XCTAssertNil(InstancePaths.socketPathProblem("/tmp/qa936/mcpproxy.sock"),
                     "a path that fits is not a problem")
        XCTAssertNil(
            InstancePaths.socketPathProblem(
                InstancePaths.socketPath(environment: [:], home: home)
            ),
            "and the default never warns"
        )
    }

    /// The boundary itself: 103 bytes of `sun_path` plus the NUL fit, 104 do not.
    func testTheSocketPathLimitIsTheSunPathBoundary() {
        let fits = "/tmp/" + String(repeating: "a", count: 103 - 5)
        XCTAssertEqual(fits.utf8.count, 103)
        XCTAssertNil(InstancePaths.socketPathProblem(fits))
        XCTAssertNotNil(InstancePaths.socketPathProblem(fits + "a"))
    }

    // MARK: - What the spawned core is told

    /// A tray with an overridden root must hand the core the same root, or the
    /// tray watches `/tmp/qa936/mcpproxy.sock` while the core it just launched
    /// writes to the real `~/.mcpproxy` — the accidental mutation this issue
    /// was opened about.
    func testTheSpawnedCoreIsPointedAtTheSameRoot() {
        let env = [InstancePaths.rootEnvVar: "/tmp/qa936"]
        XCTAssertEqual(
            InstancePaths.coreArguments(environment: env, home: home),
            ["serve", "--data-dir", "/tmp/qa936", "--config", "/tmp/qa936/mcp_config.json"]
        )
    }

    /// …and an ordinary launch is byte-for-byte the command it always was.
    func testWithoutTheOverrideTheCoreIsLaunchedExactlyAsBefore() {
        XCTAssertEqual(InstancePaths.coreArguments(environment: [:], home: home), ["serve"])
    }
}
