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

    /// A form is on screen right now.
    var isPresenting: Bool { window?.isVisible == true }

    /// Take ownership of a freshly built form.
    func adopt(_ window: NSWindow, model: ConnectClientModel) {
        self.window = window
        self.model = model
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
    /// was this form's — i.e. the notification was consumed.
    @discardableResult
    func windowWillClose(_ closing: NSWindow?) -> Bool {
        guard let window, closing === window else { return false }
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
    }
}
