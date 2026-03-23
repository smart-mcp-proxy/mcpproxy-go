// MCPProxyApp.swift
// MCPProxy
//
// The @main entry point for the MCPProxy macOS tray application.
// Sets up MenuBarExtra, initializes services, and manages app lifecycle.

import SwiftUI

@main
struct MCPProxyApp: App {
    @State private var appState = AppState()
    @State private var coreManager: CoreProcessManager?
    @State private var notificationService = NotificationService()
    @State private var updateService = UpdateService()
    @State private var hasInitialized = false

    var body: some Scene {
        MenuBarExtra {
            TrayMenu(
                appState: appState,
                updateService: updateService,
                onRestart: { Task { await coreManager?.retry() } },
                onQuit: { Task { await shutdown() } }
            )
            .task {
                guard !hasInitialized else { return }
                hasInitialized = true
                await setupOnFirstAppear()
            }
        } label: {
            TrayIcon(appState: appState)
        }
        .menuBarExtraStyle(.menu)
    }

    // MARK: - Lifecycle

    /// Called once when the menu first appears. Initializes core process management,
    /// notification categories, and auto-start state.
    private func setupOnFirstAppear() async {
        // Register notification categories and request permission
        await notificationService.setup()

        // Read auto-start state from the system
        appState.autoStartEnabled = AutoStartService.isEnabled

        // Offer symlink creation if not already set up
        if SymlinkService.needsSetup() {
            // Attempt non-interactively; if the bundled binary exists inside .app we link it.
            // Errors are logged but not user-blocking.
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

    /// Gracefully shuts down the core and exits the application.
    private func shutdown() async {
        await coreManager?.shutdown()
        // Give a brief moment for cleanup before terminating
        try? await Task.sleep(nanoseconds: 200_000_000) // 200ms
        NSApplication.shared.terminate(nil)
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
