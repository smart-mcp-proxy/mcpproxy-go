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
}
