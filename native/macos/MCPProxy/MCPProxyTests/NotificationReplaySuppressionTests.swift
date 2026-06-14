import XCTest
@testable import MCPProxy

/// Tests the connection-settle gate that suppresses SSE-replay-driven
/// notifications (quarantine, sensitive-data) during a backend re-init /
/// restart loop.
///
/// Background (MCP-2328): quarantine/sensitive notifications fire on a
/// "count went up vs the last-seen value" heuristic. When the core is stuck
/// in a ~10s re-init loop, each cycle disconnects then replays the full
/// server/activity state, so the count transiently drops and is then
/// re-established — making every cycle look like a brand-new event and
/// producing a notification storm.
///
/// `ConnectionSettleGate` requires the connection to have been free of
/// instability (reconnect / relaunch / config reload) for `settleInterval`
/// before such notifications are allowed again. An active loop keeps marking
/// the connection unsettled faster than it can settle, so nothing fires; a
/// genuinely stable connection settles and lets real events through.
final class NotificationReplaySuppressionTests: XCTestCase {

    // MARK: - Default settled state

    func testFreshGateIsSettled() {
        // With no instability ever recorded, replay-driven notifications are
        // allowed (e.g. a long-running, stable session).
        let gate = ConnectionSettleGate(settleInterval: 12)
        XCTAssertTrue(gate.isSettled(now: Date()))
    }

    // MARK: - Suppression while unsettled

    func testNotSettledImmediatelyAfterInstability() {
        var gate = ConnectionSettleGate(settleInterval: 12)
        let t0 = Date()
        gate.markUnsettled(now: t0)
        // Right after a reconnect/relaunch the connection is unsettled.
        XCTAssertFalse(gate.isSettled(now: t0))
        // 5s later — still inside the settle window.
        XCTAssertFalse(gate.isSettled(now: t0.addingTimeInterval(5)))
        // 11s later — still inside.
        XCTAssertFalse(gate.isSettled(now: t0.addingTimeInterval(11)))
    }

    func testSettledAtAndAfterInterval() {
        var gate = ConnectionSettleGate(settleInterval: 12)
        let t0 = Date()
        gate.markUnsettled(now: t0)
        // Exactly at the boundary — settled.
        XCTAssertTrue(gate.isSettled(now: t0.addingTimeInterval(12)))
        // Comfortably past — settled.
        XCTAssertTrue(gate.isSettled(now: t0.addingTimeInterval(30)))
    }

    // MARK: - The restart-loop invariant (AC-1)

    /// A ~10s re-init loop marks the connection unsettled every cycle. Because
    /// the cycle period (10s) is shorter than the settle interval (12s), the
    /// gate NEVER reports settled for the duration of the loop — so no
    /// replay-driven notification is ever allowed to fire.
    func testRestartLoopNeverSettles() {
        var gate = ConnectionSettleGate(settleInterval: 12)
        let start = Date()
        let cyclePeriod: TimeInterval = 10
        for cycle in 0..<30 { // ~5 minutes of looping
            let t = start.addingTimeInterval(cyclePeriod * Double(cycle))
            gate.markUnsettled(now: t)
            // At the moment of each re-init, and just before the next one,
            // the gate must report unsettled.
            XCTAssertFalse(gate.isSettled(now: t), "cycle \(cycle): should be unsettled at re-init")
            XCTAssertFalse(
                gate.isSettled(now: t.addingTimeInterval(cyclePeriod - 1)),
                "cycle \(cycle): should still be unsettled 9s after re-init (< 12s window)"
            )
        }
    }

    // MARK: - Recovery after the loop ends (AC-2)

    /// Once the loop ends and the connection stays stable past the settle
    /// interval, the gate reports settled again so legitimate notifications
    /// resume.
    func testSettlesOnceLoopEnds() {
        var gate = ConnectionSettleGate(settleInterval: 12)
        let start = Date()
        // Three unstable cycles...
        for cycle in 0..<3 {
            gate.markUnsettled(now: start.addingTimeInterval(10 * Double(cycle)))
        }
        let lastInstability = start.addingTimeInterval(20)
        // 11s after the last re-init — not yet settled.
        XCTAssertFalse(gate.isSettled(now: lastInstability.addingTimeInterval(11)))
        // 13s after the last re-init — settled, real events allowed again.
        XCTAssertTrue(gate.isSettled(now: lastInstability.addingTimeInterval(13)))
    }
}
