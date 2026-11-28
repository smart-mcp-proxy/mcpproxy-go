import XCTest
@testable import MCPProxy

/// A second tray for the same instance root must see the first one's lock, and
/// the lock must go away with its holder.
final class TrayInstanceLockTests: XCTestCase {

    private var lockURL: URL!

    override func setUpWithError() throws {
        lockURL = URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
            .appendingPathComponent("traylock-\(UUID().uuidString)", isDirectory: true)
            .appendingPathComponent(InstancePaths.trayLockFileName)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: lockURL.deletingLastPathComponent())
    }

    func testASecondTrayIsRefusedUntilTheFirstLetsGo() {
        // flock belongs to the open file description, so two opens in one
        // process exclude each other exactly like two processes do.
        guard case .acquired(let first) = TrayInstanceLock.acquire(at: lockURL) else {
            return XCTFail("the first tray must get the lock (creating its directory)")
        }
        XCTAssertEqual(TrayInstanceLock.acquire(at: lockURL), .heldByAnotherTray)

        close(first) // what the kernel does when the first tray exits
        guard case .acquired(let again) = TrayInstanceLock.acquire(at: lockURL) else {
            return XCTFail("a tray launched after the first one exited must start")
        }
        close(again)
    }

    /// "Relaunch into the replaced bundle" starts the new tray while this one is
    /// still running. The new one must find the lock free, or it exits and the
    /// user is left with no tray at all.
    func testHandOverFreesTheLockBeforeTheSuccessorStarts() {
        guard case .acquired(let held) = TrayInstanceLock.acquire(at: lockURL) else {
            return XCTFail("the first tray must get the lock")
        }
        var successor: TrayInstanceLock?
        let outcome = TrayInstanceLock.handOver(held, at: lockURL) {
            successor = TrayInstanceLock.acquire(at: lockURL)
        }
        guard case .launched = outcome else { return XCTFail("the launch succeeded") }
        guard case .acquired(let next)? = successor else {
            return XCTFail("the successor tray must get the lock, got \(String(describing: successor))")
        }
        close(next)
    }

    /// A launch that fails leaves this tray running, so it must hold the lock again.
    func testHandOverTakesTheLockBackWhenTheLaunchFails() {
        guard case .acquired(let held) = TrayInstanceLock.acquire(at: lockURL) else {
            return XCTFail("the first tray must get the lock")
        }
        struct LaunchFailed: Error {}
        let outcome = TrayInstanceLock.handOver(held, at: lockURL) { throw LaunchFailed() }
        guard case .failed(_, let relock) = outcome, case .acquired(let again) = relock else {
            return XCTFail("a failed launch must take the lock back, got \(outcome)")
        }
        XCTAssertEqual(TrayInstanceLock.acquire(at: lockURL), .heldByAnotherTray)
        close(again)
    }

    /// A tray that started in the gap owns the lock now; the failed launch must
    /// report that, so the old tray quits instead of running beside it unlocked.
    func testHandOverReportsATrayThatStartedInTheGap() {
        guard case .acquired(let held) = TrayInstanceLock.acquire(at: lockURL) else {
            return XCTFail("the first tray must get the lock")
        }
        struct LaunchFailed: Error {}
        var gapTray: TrayInstanceLock?
        let outcome = TrayInstanceLock.handOver(held, at: lockURL) {
            gapTray = TrayInstanceLock.acquire(at: lockURL)
            throw LaunchFailed()
        }
        guard case .failed(_, let relock) = outcome else { return XCTFail("the launch failed") }
        XCTAssertEqual(relock, .heldByAnotherTray)
        if case .acquired(let fd)? = gapTray { close(fd) }
    }

    /// `Process.run()` only proves `open` started. A launcher that exits
    /// non-zero (bundle moved, LaunchServices refused it) must count as a failed
    /// launch, or the old tray quits and leaves none.
    func testLauncherThatExitsNonZeroIsAFailedLaunch() {
        let failing = Process()
        failing.executableURL = URL(fileURLWithPath: "/usr/bin/false")
        XCTAssertThrowsError(try TrayInstanceLock.runSuccessorLauncher(failing))

        let succeeding = Process()
        succeeding.executableURL = URL(fileURLWithPath: "/usr/bin/true")
        XCTAssertNoThrow(try TrayInstanceLock.runSuccessorLauncher(succeeding))
    }
}
