import XCTest
import AppKit
import Combine
@testable import MCPProxy

/// The delivery layer underneath the glance's open-menu update path.
///
/// `GlanceSection.updateInPlace` and `MenuRebuildGuard`'s update-in-place /
/// defer-until-close branches are covered elsewhere, but every one of those
/// tests calls the section directly. In production nothing calls it while the
/// menu is on screen except the debounced `objectWillChange` sink — so if that
/// sink is not serviced during menu tracking, all of that machinery is dead code
/// and the glance is a snapshot frozen at menu-open time. That is not
/// hypothetical: it shipped that way, and live QA held the menu open for 45s and
/// saw a 45-second-old call still captioned "3s", counts that never moved, two
/// screenshots 2s apart pixel-identical, and a call made while the menu was open
/// never appear.
///
/// While an `NSMenu` tracks, the main run loop runs in
/// `NSEventTrackingRunLoopMode`. These tests therefore pump the main run loop in
/// **that mode only** — never `.default` — which is what lets them tell a
/// scheduler that survives tracking from one that does not.
@MainActor
final class MenuRefreshSchedulerTests: XCTestCase {

    /// Mirrors the production debounce interval at the subscription site.
    private static let debounceMs = 500

    /// `.eventTracking` only reaches the main dispatch queue once it is one of
    /// the run loop's *common* modes, and it is AppKit that puts it there when
    /// `NSApplication` is initialized. The tray is a real `NSApplication`, so
    /// that holds in production; an xctest bundle has to ask for it, and without
    /// this every test below would "pass" by measuring a process where no
    /// scheduler at all is serviced during tracking.
    private func requireEventTrackingIsACommonMode() {
        _ = NSApplication.shared
    }

    // MARK: - The regression

    /// The end-to-end pin: a state change while the menu is "tracking" must
    /// reach the rows, not be dropped.
    ///
    /// The sink body mirrors `AppController.rebuildMenu`'s guard call rather
    /// than invoking it (that method is private and needs a live
    /// `NSStatusItem`). What is *not* mirrored is the scheduler: this binds to
    /// `AppController.menuRefreshScheduler`, the same value the subscription
    /// uses, so reverting that to `RunLoop.main` fails this test.
    func testAStateChangeWhileTheMenuIsTrackingRewritesTheRowsInPlace() {
        requireEventTrackingIsACommonMode()

        let state = GlanceFixtures.connectedState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let header = items[0]
        XCTAssertEqual(header.title, "12 calls in the last 24h · 1 active")

        var rebuildGuard = MenuRebuildGuard()
        rebuildGuard.menuWillOpen()

        var decisions: [MenuRebuildDecision] = []
        let token = state.objectWillChange
            .debounce(for: .milliseconds(Self.debounceMs),
                      scheduler: AppController.menuRefreshScheduler)
            .sink { _ in
                MainActor.assumeIsolated {
                    decisions.append(rebuildGuard.decide(refreshing: section,
                                                         from: state,
                                                         now: Self.now))
                }
            }
        defer { token.cancel() }

        // A call lands while the user is reading the menu.
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 13, errors: 0, totalRespBytes: 0)]

        Self.pump(.eventTracking, until: { !decisions.isEmpty })

        XCTAssertEqual(decisions, [.updateInPlace],
                       "a refresh during menu tracking must reach the section and take the in-place branch")
        XCTAssertEqual(header.title, "13 calls in the last 24h · 1 active",
                       "the row on screen must show the new count, not the one captured at menu-open time")
    }

    /// Why the scheduler had to change — and the proof that the pump above is
    /// selective rather than draining everything.
    ///
    /// Combine's `RunLoop` scheduler installs its timers in `.default` mode
    /// only, so nothing it schedules is serviced while a menu tracks. Without
    /// this control a reader could not tell whether the test above passes
    /// because the fix works or because the pump services every mode.
    func testTheRunLoopSchedulerIsNeverServicedWhileTheMenuIsTracking() {
        requireEventTrackingIsACommonMode()

        let subject = PassthroughSubject<Void, Never>()
        var fired = 0
        let token = subject
            .debounce(for: .milliseconds(Self.debounceMs), scheduler: RunLoop.main)
            .sink { fired += 1 }
        defer { token.cancel() }

        subject.send(())
        Self.pump(.eventTracking, until: { fired > 0 }, timeout: 2.0)

        XCTAssertEqual(fired, 0,
                       "RunLoop.main schedules in .default mode only; if this ever fires under an event-tracking-only pump, this suite has lost its ability to detect the freeze")

        // And it was only the mode holding it back: one turn of the default mode
        // releases that very same subscription.
        Self.pump(.default, until: { fired > 0 })
        XCTAssertEqual(fired, 1, "the delivery was deferred by the run-loop mode, not lost")
    }

    // MARK: - No regression when no menu is open

    /// The debounce still coalesces a burst into a single delivery, on the main
    /// thread. Changing the scheduler must not turn the 500 ms coalescing window
    /// into a repaint per event.
    func testTheDebounceStillCoalescesABurstIntoOneMainThreadDelivery() {
        requireEventTrackingIsACommonMode()

        let subject = PassthroughSubject<Void, Never>()
        var deliveries = 0
        var everOffMain = false
        let token = subject
            .debounce(for: .milliseconds(Self.debounceMs),
                      scheduler: AppController.menuRefreshScheduler)
            .sink { _ in
                if !Thread.isMainThread { everOffMain = true }
                deliveries += 1
            }
        defer { token.cancel() }

        for _ in 0..<20 { subject.send(()) }

        // Well past the window, so a second delivery would have shown up.
        let settled = Date().addingTimeInterval(1.5)
        while Date() < settled {
            RunLoop.main.run(mode: .default, before: Date().addingTimeInterval(0.05))
        }

        XCTAssertEqual(deliveries, 1, "20 events inside the window must repaint the menu once")
        XCTAssertFalse(everOffMain, "menu rebuilds must arrive on the main thread")
    }

    /// Two events separated by a quiet gap are two repaints, not one — i.e. the
    /// window really is the debounce and not something the queue swallowed.
    func testEventsSeparatedByTheWindowAreDeliveredSeparately() {
        requireEventTrackingIsACommonMode()

        let subject = PassthroughSubject<Void, Never>()
        var deliveries = 0
        let token = subject
            .debounce(for: .milliseconds(Self.debounceMs),
                      scheduler: AppController.menuRefreshScheduler)
            .sink { _ in deliveries += 1 }
        defer { token.cancel() }

        subject.send(())
        Self.pump(.default, until: { deliveries == 1 })
        subject.send(())
        Self.pump(.default, until: { deliveries == 2 })

        XCTAssertEqual(deliveries, 2)
    }

    // MARK: - Helpers

    /// Pump the main run loop in one mode only, until `done` or the deadline.
    private static func pump(_ mode: RunLoop.Mode,
                             until done: () -> Bool,
                             timeout: TimeInterval = 3.0) {
        let deadline = Date().addingTimeInterval(timeout)
        while !done() && Date() < deadline {
            RunLoop.main.run(mode: mode, before: Date().addingTimeInterval(0.02))
        }
    }

    private static let now = GlanceFixtures.now

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    private static func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
    }
}
