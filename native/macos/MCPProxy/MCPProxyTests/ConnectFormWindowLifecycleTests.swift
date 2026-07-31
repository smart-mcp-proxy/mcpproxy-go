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
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        try XCTSkipIf(sheet.sheetParent == nil, "AppKit did not attach the sheet in this environment")

        lifecycle.adopt(sheet, model: ConnectClientModel(source: FakeConnectSource(), sleeper: { _ in }))

        XCTAssertTrue(lifecycle.windowWillClose(host))
        XCTAssertNil(lifecycle.model)

        host.endSheet(sheet)
        host.close()
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
