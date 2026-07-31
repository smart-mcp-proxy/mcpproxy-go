import AppKit

/// Owns the Connect Client form's window together with its model, so the form's
/// lifetime ends however it is dismissed.
///
/// The form has two exits: its own Close button and the titlebar's red button.
/// Only the first used to run through the app's dismissal path, and because the
/// window is created with `isReleasedWhenClosed = false`, a red-button close
/// merely orders it out — the NSHostingView, the model, and the SwiftUI `.task`
/// driving the 2 s core-reachability poll (FR-013) all stayed alive, polling
/// from a window nobody could see, until the next presentation replaced them.
///
/// Both exits now run the same teardown: the session-scoped undo ends (FR-006)
/// and the view hierarchy is released, which is what actually cancels the task.
@MainActor
final class ConnectFormWindowLifecycle {

    private(set) var window: NSWindow?
    private(set) var model: ConnectClientModel?

    /// The window the form is attached to as a sheet, when it is presented that
    /// way. Recorded at adoption rather than read back from `sheetParent`
    /// alone, so the relationship is known before AppKit has attached anything.
    private weak var host: NSWindow?

    /// A form is on screen right now.
    var isPresenting: Bool { window?.isVisible == true }

    /// Take ownership of a freshly built form. `host` is the window it is being
    /// presented on as a sheet, if any.
    func adopt(_ window: NSWindow, model: ConnectClientModel, host: NSWindow? = nil) {
        self.window = window
        self.model = model
        self.host = host
    }

    /// Bring an already-presented form forward. Returns false when there is
    /// none, so the caller knows it has to build one.
    @discardableResult
    func makeKeyIfPresenting() -> Bool {
        guard let window, window.isVisible else { return false }
        window.makeKeyAndOrderFront(nil)
        return true
    }

    /// Handle a window-close notification. Returns true when the closing window
    /// was this form's — or the window it is attached to as a sheet.
    ///
    /// The host matters because a sheet gets no close notification of its own:
    /// AppKit lets a sheet-bearing parent close, orders the sheet out with it,
    /// posts `willClose` for the HOST only, and never runs `beginSheet`'s
    /// completion handler. Without this, closing the main window while the
    /// Connect sheet was up left the model and its reachability poll alive.
    @discardableResult
    func windowWillClose(_ closing: NSWindow?) -> Bool {
        guard let window, let closing else { return false }
        guard closing === window || closing === host || closing === window.sheetParent
        else { return false }
        tearDown()
        return true
    }

    /// Dismiss from the form's own Close button, whichever way it was shown.
    func dismiss() {
        guard let window else { return }
        if let host = window.sheetParent {
            host.endSheet(window)
        } else {
            window.close()
        }
        tearDown()
    }

    private func tearDown() {
        // The undo affordance is scoped to this open form, and an unanswered
        // confirmation is abandoned rather than remembered (FR-006).
        model?.formWillClose()
        // Replacing the content view releases the hosting view and everything
        // SwiftUI built under it, so the reachability poll is cancelled even
        // though AppKit keeps the (closed, invisible) window itself around.
        window?.contentView = NSView()
        window = nil
        model = nil
        host = nil
    }
}
