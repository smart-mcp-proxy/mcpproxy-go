import XCTest
import AppKit
import SwiftUI
@testable import MCPProxy

/// The Connect form's window can be dismissed two ways — its own Close button
/// and the titlebar's red button — and only the first used to run through the
/// app's dismissal path. With `isReleasedWhenClosed = false` a red-button close
/// merely orders the window out: the hosting view, the model and the SwiftUI
/// `.task` behind them stay alive, so the form's 2 s core-reachability poll kept
/// running from a window nobody could see. Both routes must end the same way.
@MainActor
final class ConnectFormWindowLifecycleTests: XCTestCase {

    private func makeWindow() -> NSWindow {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 400, height: 300),
            styleMask: [.titled, .closable, .resizable],
            backing: .buffered,
            defer: false)
        window.isReleasedWhenClosed = false
        return window
    }

    func testTheTitlebarCloseEndsTheSessionScopedUndo() async {
        let source = FakeConnectSource()
        let model = ConnectClientModel(source: source, sleeper: { _ in })
        await model.select("claude-code")
        await model.connect()
        XCTAssertTrue(model.undoControlExists, "precondition: this form performed a connect")

        let lifecycle = ConnectFormWindowLifecycle()
        let window = makeWindow()
        lifecycle.adopt(window, model: model)

        XCTAssertTrue(lifecycle.windowWillClose(window),
                      "the notification belongs to the form's window")

        XCTAssertFalse(model.undoControlExists, "the undo's scope ends with the form (FR-006)")
        XCTAssertNil(lifecycle.window, "a closed window must not stay adopted")
        XCTAssertNil(lifecycle.model)
    }

    /// The teardown must actually RELEASE the view hierarchy — that is what
    /// cancels the SwiftUI task driving the poll. Merely forgetting the window
    /// reference would leave the retained, invisible window polling.
    func testTheTitlebarCloseReleasesTheFormAndItsPollLoop() {
        let lifecycle = ConnectFormWindowLifecycle()
        let window = makeWindow()
        weak var hostingView: NSView?
        weak var weakModel: ConnectClientModel?

        autoreleasepool {
            let model = ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in })
            let hosting = NSHostingView(rootView: ConnectClientView(model: model))
            window.contentView = hosting
            lifecycle.adopt(window, model: model)
            hostingView = hosting
            weakModel = model
        }
        XCTAssertNotNil(hostingView, "precondition: the form is presented")

        autoreleasepool {
            lifecycle.windowWillClose(window)
        }

        XCTAssertNil(hostingView, "the hosting view must be released, cancelling its .task")
        XCTAssertNil(weakModel, "so the 2 s reachability poll cannot keep running unseen")
    }

    /// Presented as a SHEET, the form's own window never gets a close
    /// notification of its own: closing the host takes the sheet with it.
    ///
    /// Probed on this macOS: AppKit lets a sheet-bearing parent close, orders
    /// the sheet out with it, posts `willClose` for the HOST ONLY, and never
    /// runs `beginSheet`'s completion handler — so that completion cannot be
    /// the teardown hook, and the host's close has to be.
    func testClosingTheHostOfAnAttachedSheetTearsTheFormDown() {
        let lifecycle = ConnectFormWindowLifecycle()
        let host = makeWindow()
        let sheet = makeWindow()
        let model = ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in })
        lifecycle.adopt(sheet, model: model, host: host)

        XCTAssertTrue(lifecycle.windowWillClose(host),
                      "the host's close is the only notification this form will see")

        XCTAssertNil(lifecycle.window)
        XCTAssertNil(lifecycle.model)
    }

    /// Belt and braces: even a form adopted without its host is recognised
    /// through AppKit's own sheet relationship.
    func testClosingTheSheetParentTearsTheFormDown() throws {
        let lifecycle = ConnectFormWindowLifecycle()
        let host = makeWindow()
        let sheet = makeWindow()
        host.makeKeyAndOrderFront(nil)
        host.beginSheet(sheet) { _ in }
        // Attachment is asynchronous, so wait for it rather than for a fixed
        // slice of wall clock: a loaded runner that needed 0.3 s used to turn
        // this test into a silent skip.
        let attached = Date().addingTimeInterval(2)
        while sheet.sheetParent == nil, Date() < attached {
            RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
        }
        try XCTSkipIf(sheet.sheetParent == nil, "AppKit did not attach the sheet in this environment")

        lifecycle.adopt(sheet, model: ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in }))

        XCTAssertTrue(lifecycle.windowWillClose(host))
        XCTAssertNil(lifecycle.model)

        host.endSheet(sheet)
        host.close()
    }

    /// Counts what the poll asked the clock for, and records the moment the
    /// loop returned, so "it stopped" is a measured fact rather than an
    /// inference.
    @MainActor
    final class PollCounter {
        private(set) var ticks = 0
        private(set) var loopReturned = false
        func tick() { ticks += 1 }
        func markReturned() { loopReturned = true }
    }

    /// The leak that mattered is a poll that keeps running, so this drives the
    /// poll for real: against an unreachable core it iterates, and once the task
    /// carrying it is cancelled the counter FREEZES.
    ///
    /// Cancellation is what the teardown ultimately triggers — SwiftUI cancels
    /// the `.task` when the hosting view goes away, which the release test above
    /// pins by proving that view and model are deallocated. That SwiftUI link
    /// cannot be exercised here: probed on this machine, a `.task` never starts
    /// under `swift test` even with a key, activated window and a hosting
    /// controller (zero iterations, the view stays in its initial state), so
    /// this covers the half that is ours — that `loadList` honours the
    /// cancellation it is sent.
    ///
    /// Note the sleeper mirrors the production one, which returns IMMEDIATELY
    /// once cancelled (`try? await Task.sleep`): a loop that did not check
    /// `Task.isCancelled` would not slow down here, it would spin.
    ///
    /// What "stopped" is measured as, and why it is not a wall-clock guess: the
    /// cancellation can land while an iteration is already in flight — suspended
    /// in the source call, past that iteration's cancellation check — and that
    /// iteration still unwinds through the sleeper once. Counting from the
    /// pre-cancellation total therefore proves nothing on a loaded runner. So
    /// this waits for the loop to RETURN, allows that single settling tick as a
    /// hard ceiling, and then samples the counter twice to show it is frozen.
    func testTheReachabilityPollRunsAndThenProvablyStops() async {
        let source = FakeConnectSource()
        source.clientsResults = [.failure(APIClientError.notReady)]
        let counter = PollCounter()
        let model = ConnectClientModel(source: source, sleeper: { _ in
            counter.tick()
            try? await Task.sleep(nanoseconds: 5_000_000)
        })

        let poll = Task {
            await model.loadList()
            counter.markReturned()
        }
        defer { poll.cancel() }

        // It is polling: wait for real iterations rather than assuming any.
        var waits = 0
        while counter.ticks < 3, waits < 200 {
            try? await Task.sleep(nanoseconds: 5_000_000)
            waits += 1
        }
        XCTAssertGreaterThanOrEqual(counter.ticks, 3,
                                    "precondition: the form is polling an unreachable core")
        XCTAssertEqual(model.list, .coreUnreachable(APIClientError.notReady.errorDescription ?? ""))

        poll.cancel()
        let atCancellation = counter.ticks

        // A cancelled loop returns; a runaway one never does, so this waits on
        // the event rather than on the clock and a slow runner only makes the
        // test slower, never red.
        waits = 0
        while !counter.loopReturned, waits < 400 {
            try? await Task.sleep(nanoseconds: 5_000_000)
            waits += 1
        }
        XCTAssertTrue(counter.loopReturned,
                      "cancellation must END loadList, not merely slow it down")
        XCTAssertLessThanOrEqual(counter.ticks, atCancellation + 1,
                                 "only the one already-in-flight iteration may settle; "
                                 + "a second means the loop went round again after cancellation")

        // And it stays stopped. 200 ms is 40 of this sleeper's own delays, and
        // an uncancellable loop does not add a tick per delay here — it spins,
        // because the production sleeper returns immediately once cancelled.
        let settled = counter.ticks
        try? await Task.sleep(nanoseconds: 200_000_000)

        XCTAssertEqual(counter.ticks, settled,
                       "the poll must STOP, not merely slow down, once its task is cancelled")
    }

    func testAnUnrelatedWindowClosingIsIgnored() {
        let lifecycle = ConnectFormWindowLifecycle()
        let window = makeWindow()
        let model = ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in })
        lifecycle.adopt(window, model: model)

        XCTAssertFalse(lifecycle.windowWillClose(makeWindow()),
                       "another window's close is none of the form's business")
        XCTAssertNotNil(lifecycle.window)
        XCTAssertNotNil(lifecycle.model)
    }

    /// The form's own Close button goes through the same teardown, so the two
    /// routes cannot drift apart again.
    func testDismissClosesTheWindowAndTearsDown() {
        let lifecycle = ConnectFormWindowLifecycle()
        let window = makeWindow()
        let model = ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in })
        lifecycle.adopt(window, model: model)

        lifecycle.dismiss()

        XCTAssertFalse(window.isVisible)
        XCTAssertNil(lifecycle.window)
        XCTAssertNil(lifecycle.model)
    }

    func testAdoptingReplacesAPreviousForm() {
        let lifecycle = ConnectFormWindowLifecycle()
        let first = makeWindow()
        lifecycle.adopt(first, model: ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in }))
        let second = makeWindow()
        lifecycle.adopt(second, model: ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in }))

        XCTAssertTrue(lifecycle.window === second)
        XCTAssertFalse(lifecycle.windowWillClose(first),
                       "the replaced window no longer drives the lifecycle")
    }
}
