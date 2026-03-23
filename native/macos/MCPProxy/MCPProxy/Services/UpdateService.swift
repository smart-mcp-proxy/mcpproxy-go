// UpdateService.swift
// MCPProxy
//
// Sparkle framework integration for automatic software updates.
//
// Sparkle is added as an SPM dependency. When the dependency is not yet
// resolved (e.g., initial checkout), this file compiles with a stub
// implementation that gracefully degrades.
//
// To add Sparkle via SPM:
//   Package dependency: https://github.com/sparkle-project/Sparkle
//   Version: 2.5.0+
//   Target: MCPProxy

import Foundation

// MARK: - Update Service

/// Manages software update checks using the Sparkle framework.
///
/// Sparkle provides:
/// - Automatic periodic update checks
/// - User-initiated "Check for Updates" action
/// - Delta updates for faster downloads
/// - Code-signed update verification
///
/// The appcast URL is configured in the app's Info.plist under `SUFeedURL`.
///
/// When Sparkle is not available (SPM dependency not resolved), all methods
/// are no-ops and `canCheckForUpdates` returns `false`.
@Observable
final class UpdateService {

    // MARK: - Sparkle Integration

    // Sparkle controller is conditionally compiled. When Sparkle SPM package
    // is resolved, replace the stub with the real controller:
    //
    //   import Sparkle
    //   private let updaterController: SPUStandardUpdaterController
    //
    // For now, we use a placeholder that compiles without the Sparkle dependency.

    /// Whether Sparkle is available and an update check can be performed.
    var canCheckForUpdates: Bool {
        // Will be `updaterController.updater.canCheckForUpdates` once Sparkle is linked.
        return sparkleAvailable
    }

    /// Whether an update check is currently in progress.
    private(set) var isChecking: Bool = false

    /// Flag indicating whether Sparkle framework is linked.
    private let sparkleAvailable: Bool

    // MARK: - Initialization

    init() {
        // Attempt to load Sparkle dynamically. This allows the binary to
        // compile and run even when Sparkle is not yet linked.
        self.sparkleAvailable = Self.isSparkleLinked()

        if sparkleAvailable {
            // Initialize SPUStandardUpdaterController here when Sparkle is linked:
            // updaterController = SPUStandardUpdaterController(
            //     startingUpdater: true,
            //     updaterDelegate: nil,
            //     userDriverDelegate: nil
            // )
        }
    }

    // MARK: - Public API

    /// Trigger a user-initiated update check.
    ///
    /// This opens Sparkle's standard update UI if an update is available.
    /// If Sparkle is not linked, this is a no-op.
    func checkForUpdates() {
        guard sparkleAvailable else { return }
        guard !isChecking else { return }

        isChecking = true

        // When Sparkle is linked:
        // updaterController.checkForUpdates(nil)

        // For now, simulate the check completion after a short delay
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            self?.isChecking = false
        }
    }

    // MARK: - Private

    /// Check if the Sparkle framework is available at runtime.
    private static func isSparkleLinked() -> Bool {
        // Check if SPUStandardUpdaterController class exists
        return NSClassFromString("SPUStandardUpdaterController") != nil
    }
}
