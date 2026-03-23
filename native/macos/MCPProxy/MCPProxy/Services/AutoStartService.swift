// AutoStartService.swift
// MCPProxy
//
// Login item management using the ServiceManagement framework.
// Manages automatic launch at user login on macOS 13+.

import Foundation
import ServiceManagement

// MARK: - Auto Start Errors

/// Errors from login item management.
enum AutoStartError: Error, LocalizedError {
    case registrationFailed(Error)
    case unregistrationFailed(Error)

    var errorDescription: String? {
        switch self {
        case .registrationFailed(let error):
            return "Failed to register login item: \(error.localizedDescription)"
        case .unregistrationFailed(let error):
            return "Failed to unregister login item: \(error.localizedDescription)"
        }
    }
}

// MARK: - Auto Start Service

/// Manages the application's login item registration.
///
/// On macOS 13+, uses `SMAppService.mainApp` for login item management.
/// This requires no special entitlements and is the recommended approach
/// for sandboxed and non-sandboxed apps alike.
struct AutoStartService {

    /// Whether the app is currently registered as a login item.
    static var isEnabled: Bool {
        SMAppService.mainApp.status == .enabled
    }

    /// Register the app as a login item so it launches at user login.
    /// Throws `AutoStartError.registrationFailed` on failure.
    static func enable() throws {
        do {
            try SMAppService.mainApp.register()
        } catch {
            throw AutoStartError.registrationFailed(error)
        }
    }

    /// Unregister the app from launching at login.
    /// Throws `AutoStartError.unregistrationFailed` on failure.
    static func disable() throws {
        do {
            try SMAppService.mainApp.unregister()
        } catch {
            throw AutoStartError.unregistrationFailed(error)
        }
    }
}
