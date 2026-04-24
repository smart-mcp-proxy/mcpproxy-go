// FirstRunDialog.swift
// MCPProxy
//
// Spec 044 (T054): on first launch the tray presents a small dialog asking
// the user to confirm the login-item default. The "Launch at login" checkbox
// starts ON (default-on per spec §4), with a clear opt-out that is one click
// away. Not a dark pattern: the dialog is modal, the copy explains why, and
// the user can turn it off here or from the tray menu anytime.
//
// A UserDefaults boolean `MCPProxy.firstRunCompleted` marks completion so the
// dialog only appears once.

import AppKit
import SwiftUI

// MARK: - UserDefaults Keys

/// UserDefaults key tracking whether the first-run dialog has been shown.
/// Absent or `false` → dialog will be presented at next launch.
let firstRunCompletedKey = "MCPProxy.firstRunCompleted"

/// Returns `true` if the first-run dialog has already been shown.
func isFirstRunCompleted() -> Bool {
    UserDefaults.standard.bool(forKey: firstRunCompletedKey)
}

/// Marks the first-run dialog as shown so it will not appear again.
func markFirstRunCompleted() {
    UserDefaults.standard.set(true, forKey: firstRunCompletedKey)
}

// MARK: - First-Run Dialog View

/// A modal dialog shown exactly once at app first launch. The user confirms
/// (or opts out of) the login-item default.
///
/// Design (spec 044 §7.3):
///   • Checkbox defaults to ON.
///   • "Launch MCPProxy at login so your AI tools always have access"
///   • Secondary opt-out text: "You can change this anytime in the tray menu."
///   • Continue button persists the choice and dismisses.
struct FirstRunDialog: View {
    @Binding var launchAtLogin: Bool
    let onContinue: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Welcome to MCPProxy")
                .font(.title2)
                .fontWeight(.semibold)
                .accessibilityIdentifier("firstrun-title")

            Text("MCPProxy runs quietly in your menu bar and gives AI tools (Claude, Cursor, Continue, …) a single, fast way to talk to all your configured MCP servers.")
                .font(.body)
                .fixedSize(horizontal: false, vertical: true)

            Divider()

            Toggle(isOn: $launchAtLogin) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Launch MCPProxy at login")
                        .font(.headline)
                    Text("Recommended — your AI tools can reach your servers immediately after you log in. You can turn this off anytime from the tray menu.")
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .toggleStyle(.checkbox)
            .accessibilityIdentifier("firstrun-launch-at-login")

            HStack {
                Spacer()
                Button("Continue") {
                    onContinue()
                }
                .keyboardShortcut(.defaultAction)
                .accessibilityIdentifier("firstrun-continue")
            }
            .padding(.top, 8)
        }
        .padding(20)
        .frame(width: 460)
    }
}

// MARK: - Presentation Helper

/// Presents the first-run dialog if it has not yet been shown. Called from
/// `applicationDidFinishLaunching` on the tray app's launch path.
///
/// On dismiss:
///   • If the checkbox is ON, `SMAppService.mainApp.register()` is invoked.
///     Failure is logged but not surfaced — the user can retry from the tray.
///   • `AutostartSidecarService.refresh()` is called so the core sees the
///     new state within its 1h TTL.
///   • `markFirstRunCompleted()` persists the one-shot flag.
///
/// MUST be called on the main thread (SwiftUI + NSWindow).
@MainActor
func presentFirstRunDialogIfNeeded() {
    if isFirstRunCompleted() {
        return
    }

    // Reference-type box so the SwiftUI @State binding can mutate a value
    // that the NSWindow callback can still read after the SwiftUI hierarchy
    // tears down.
    let choice = FirstRunDialogChoice()
    choice.launchAtLogin = true  // default ON per spec §4

    // Build the dialog inside an NSHostingController so we can drive the
    // window imperatively (modal NSApp.runModal → block-free dismissal).
    let host = NSHostingController(
        rootView: FirstRunDialogBinding(choice: choice)
    )
    host.view.frame = NSRect(x: 0, y: 0, width: 460, height: 260)

    let window = NSWindow(contentViewController: host)
    window.title = "Welcome to MCPProxy"
    window.styleMask = [.titled, .closable]
    window.isReleasedWhenClosed = false
    window.center()

    NSApp.activate(ignoringOtherApps: true)
    NSApp.runModal(for: window)
    window.orderOut(nil)

    // Apply the choice. Order matters: register first, then publish the
    // sidecar, so the sidecar reflects the actually-applied state.
    if choice.launchAtLogin {
        do {
            try AutoStartService.enable()
            NSLog("[MCPProxy] FirstRun: login item registered")
        } catch {
            NSLog("[MCPProxy] FirstRun: login item registration FAILED: %@", error.localizedDescription)
        }
    } else {
        // User opted out — make sure we're not already registered from a
        // previous installation.
        try? AutoStartService.disable()
    }
    AutostartSidecarService.refresh()
    markFirstRunCompleted()
}

/// Thin wrapper that bridges the SwiftUI @State binding to a reference-type
/// Choice, and wires the Continue button to `NSApp.stopModal`.
private struct FirstRunDialogBinding: View {
    let choice: FirstRunDialogChoice
    @State private var launchAtLogin: Bool = true

    var body: some View {
        FirstRunDialog(
            launchAtLogin: $launchAtLogin,
            onContinue: {
                choice.launchAtLogin = launchAtLogin
                NSApp.stopModal()
            }
        )
    }
}

/// Heap-allocated box so the modal callback can mutate it.
final class FirstRunDialogChoice {
    var launchAtLogin: Bool = true
}

// MARK: - Bridge alias

/// Keeps the private Choice name above distinct from the public-ish name used
/// by external callers (AppController). Importing `FirstRunDialogChoice`
/// directly stays clean.
typealias FirstRunChoice = FirstRunDialogChoice
