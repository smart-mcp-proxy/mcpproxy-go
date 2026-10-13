import XCTest
@testable import MCPProxy

/// The one liveness signal that does not go through the socket (GH #933).
///
/// A core with a saturated listen backlog refuses every probe, exactly like a
/// socket file a dead core left behind — sample by sample the two are
/// indistinguishable, which is what let the tray launch over a healthy core.
/// The core takes bbolt's exclusive `flock` on its data directory BEFORE it
/// creates its listener, and the kernel releases that lock when the process
/// dies. So "is somebody holding the lock" answers the question the socket
/// cannot, and answers it without a false positive: no dead process can hold it.
final class DataDirectoryLockTests: XCTestCase {

    private var directory: URL!
    private var database: URL!
    private var heldDescriptor: Int32 = -1

    override func setUpWithError() throws {
        directory = URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
            .appendingPathComponent("datalock-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        database = directory.appendingPathComponent("config.db")
    }

    override func tearDownWithError() throws {
        if heldDescriptor >= 0 {
            flock(heldDescriptor, LOCK_UN)
            close(heldDescriptor)
            heldDescriptor = -1
        }
        try? FileManager.default.removeItem(at: directory)
    }

    /// No database, nothing to conclude. A tray pointed at a socket outside any
    /// data directory must get "I cannot tell", never "nothing is running".
    func testAMissingDatabaseIsUnknownNotFree() {
        guard case .unknown = DataDirectoryLock.probe(path: database.path) else {
            return XCTFail("a missing file cannot prove anything, got "
                           + "\(DataDirectoryLock.probe(path: database.path))")
        }
    }

    /// The stale-socket case: the file is there and nobody holds it, because
    /// the process that did is gone. This is the reading that permits a launch.
    func testAnUnlockedDatabaseReadsAsFree() throws {
        FileManager.default.createFile(atPath: database.path, contents: Data())
        XCTAssertEqual(DataDirectoryLock.probe(path: database.path), .free)
    }

    /// The case this exists for: something alive holds the lock. `flock` locks
    /// belong to the open file DESCRIPTION, so a second `open` of the same file
    /// conflicts even from inside this process — which is what makes the live
    /// case testable at all.
    func testADatabaseHeldByALiveProcessIsDetected() throws {
        FileManager.default.createFile(atPath: database.path, contents: Data())
        heldDescriptor = open(database.path, O_RDONLY)
        XCTAssertGreaterThanOrEqual(heldDescriptor, 0)
        XCTAssertEqual(flock(heldDescriptor, LOCK_EX | LOCK_NB), 0,
                       "precondition: this test now holds the lock a core would hold")

        XCTAssertEqual(DataDirectoryLock.probe(path: database.path), .heldByALiveProcess)
    }

    /// And the probe itself must not become the thing that blocks a core: it
    /// takes a SHARED lock, non-blocking, and gives it straight back. Two
    /// probes at once therefore both read "free" rather than each reporting the
    /// other as a live core.
    func testTheProbeDoesNotLockOutAnotherProbe() throws {
        FileManager.default.createFile(atPath: database.path, contents: Data())
        XCTAssertEqual(DataDirectoryLock.probe(path: database.path), .free)
        XCTAssertEqual(DataDirectoryLock.probe(path: database.path), .free)

        // The lock really was released: an exclusive taker still gets it.
        let descriptor = open(database.path, O_RDONLY)
        defer { close(descriptor) }
        XCTAssertEqual(flock(descriptor, LOCK_EX | LOCK_NB), 0,
                       "the probe must leave no lock behind")
        flock(descriptor, LOCK_UN)
    }

    /// Where the tray looks: beside the socket it is probing. The core's socket
    /// and its database live in the same directory, so the lock that answers
    /// for THIS socket is the one next to it — not the one under the default
    /// instance root, which belongs to a different core entirely.
    func testTheLockIsLookedForBesideTheSocketBeingProbed() {
        XCTAssertEqual(DataDirectoryLock.path(forSocket: "/tmp/qa933/mcpproxy.sock"),
                       "/tmp/qa933/config.db")
        XCTAssertEqual(
            DataDirectoryLock.path(forSocket: "/Users/x/.mcpproxy/mcpproxy.sock"),
            "/Users/x/.mcpproxy/config.db"
        )
    }
}
