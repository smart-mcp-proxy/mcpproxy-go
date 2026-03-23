// MCPProxyApp.swift
// MCPProxy
//
// The @main entry point for the MCPProxy macOS tray application.
// Sets up MenuBarExtra, initializes services, and manages app lifecycle.
//
// IMPORTANT: Core process launch happens at app startup (via AppDelegate),
// NOT on first menu open. The .menu style MenuBarExtra only renders its
// content when the user clicks the icon, so .task{} would be too late.

import SwiftUI

// MARK: - App Delegate (startup logic)

/// Handles app lifecycle events. Core launch happens here, not in SwiftUI views.
final class AppController: NSObject, NSApplicationDelegate {
    let appState = AppState()
    let notificationService = NotificationService()
    let updateService = UpdateService()
    var coreManager: CoreProcessManager?

    func applicationDidFinishLaunching(_ notification: Notification) {
        Task {
            await startCore()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        // Synchronous shutdown — send SIGTERM and don't wait
        if let process = coreManager?.managedProcess {
            process.terminate()
        }
    }

    private func startCore() async {
        // Register notification categories and request permission
        await notificationService.setup()

        // Read auto-start state from the system
        await MainActor.run {
            appState.autoStartEnabled = AutoStartService.isEnabled
        }

        // Symlink setup
        if SymlinkService.needsSetup() {
            if let bundledBinary = resolveBundledCoreBinary() {
                await SymlinkService.updateSymlinkIfNeeded(bundledBinary: bundledBinary)
            }
        }

        // Initialize and start the core process manager
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: notificationService
        )
        coreManager = manager
        await manager.start()
    }

    /// Resolve the bundled core binary inside the .app bundle's Resources/bin/ directory.
    private func resolveBundledCoreBinary() -> String? {
        guard let execPath = Bundle.main.executablePath else { return nil }
        let execURL = URL(fileURLWithPath: execPath)
        let macOSDir = execURL.deletingLastPathComponent()
        let contentsDir = macOSDir.deletingLastPathComponent()
        guard contentsDir.lastPathComponent == "Contents" else { return nil }
        let candidate = contentsDir
            .appendingPathComponent("Resources")
            .appendingPathComponent("bin")
            .appendingPathComponent("mcpproxy")
        if FileManager.default.isExecutableFile(atPath: candidate.path) {
            return candidate.path
        }
        return nil
    }
}

// MARK: - App

@main
struct MCPProxyApp: App {
    @NSApplicationDelegateAdaptor(AppController.self) var controller

    var body: some Scene {
        MenuBarExtra {
            TrayMenu(
                appState: controller.appState,
                updateService: controller.updateService,
                onRestart: { [weak controller] in
                    Task { await controller?.coreManager?.retry() }
                },
                onQuit: { [weak controller] in
                    Task {
                        await controller?.coreManager?.shutdown()
                        try? await Task.sleep(nanoseconds: 200_000_000)
                        NSApplication.shared.terminate(nil)
                    }
                }
            )
        } label: {
            TrayIcon(appState: controller.appState)
        }
        .menuBarExtraStyle(.menu)
    }
}
