// NotificationService.swift
// MCPProxy
//
// macOS notification delivery with rate limiting and category registration.
// Uses UNUserNotificationCenter for native notification support.

import Foundation
import UserNotifications

// MARK: - Notification Categories

/// Identifiers for notification categories, matching the action buttons shown to the user.
enum NotificationCategory: String {
    case sensitiveData = "SENSITIVE_DATA"
    case quarantine = "QUARANTINE"
    case oauthExpiry = "OAUTH_EXPIRY"
    case coreError = "CORE_ERROR"
    case updateAvailable = "UPDATE_AVAILABLE"
}

/// Identifiers for notification actions (buttons).
enum NotificationAction: String {
    case viewDetails = "VIEW_DETAILS"
    case approve = "APPROVE"
    case login = "LOG_IN"
    case restart = "RESTART"
    case dismiss = "DISMISS"
    case update = "UPDATE"
}

// MARK: - Notification Service

/// Actor that manages macOS notification delivery with rate limiting.
///
/// Rate limiting prevents notification storms when many events arrive in quick
/// succession (e.g., a server reconnecting rapidly). Each notification type
/// is independently throttled to at most one delivery per `rateLimitInterval`.
actor NotificationService {

    /// Tracks the last delivery time for each (category + key) pair.
    private var lastNotifications: [String: Date] = [:]

    /// Minimum interval between repeated notifications of the same kind.
    private let rateLimitInterval: TimeInterval = 300 // 5 minutes

    /// The shared notification center.
    private let center = UNUserNotificationCenter.current()

    // MARK: - Setup

    /// Request notification permission and register action categories.
    /// Call this once during app launch.
    func setup() async {
        // Request authorization
        do {
            let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
            if !granted {
                // User declined; notifications will silently fail. Not blocking.
                return
            }
        } catch {
            // Authorization request failed; continue without notifications.
            return
        }

        // Register notification categories with action buttons
        let categories = buildCategories()
        center.setNotificationCategories(categories)
    }

    // MARK: - Notification Senders

    /// Notify about sensitive data detected in a tool call.
    func sendSensitiveDataAlert(server: String, tool: String, category: String) async {
        let key = "sensitive:\(server):\(tool)"
        guard shouldDeliver(key: key) else { return }

        let content = UNMutableNotificationContent()
        content.title = "Sensitive Data Detected"
        content.subtitle = "\(server):\(tool)"
        content.body = "A \(category) detection was found in tool call arguments or response. Review in the activity log."
        content.sound = .default
        content.categoryIdentifier = NotificationCategory.sensitiveData.rawValue
        content.userInfo = [
            "server": server,
            "tool": tool,
            "category": category,
        ]

        await deliver(content: content, identifier: "sensitive-\(server)-\(tool)-\(Date().timeIntervalSince1970)")
        markDelivered(key: key)
    }

    /// Notify about a server entering quarantine (new or changed tools detected).
    func sendQuarantineAlert(server: String, toolCount: Int) async {
        let key = "quarantine:\(server)"
        guard shouldDeliver(key: key) else { return }

        let content = UNMutableNotificationContent()
        content.title = "Server Quarantined"
        content.subtitle = server
        content.body = "\(toolCount) tool\(toolCount == 1 ? "" : "s") need\(toolCount == 1 ? "s" : "") approval before use."
        content.sound = .default
        content.categoryIdentifier = NotificationCategory.quarantine.rawValue
        content.userInfo = [
            "server": server,
            "toolCount": toolCount,
        ]

        await deliver(content: content, identifier: "quarantine-\(server)-\(Date().timeIntervalSince1970)")
        markDelivered(key: key)
    }

    /// Notify about an OAuth token nearing expiry.
    func sendOAuthExpiryAlert(server: String, expiresIn: TimeInterval) async {
        let key = "oauth:\(server)"
        guard shouldDeliver(key: key) else { return }

        let hours = Int(expiresIn / 3600)
        let timeDescription: String
        if hours > 0 {
            timeDescription = "\(hours) hour\(hours == 1 ? "" : "s")"
        } else {
            let minutes = max(1, Int(expiresIn / 60))
            timeDescription = "\(minutes) minute\(minutes == 1 ? "" : "s")"
        }

        let content = UNMutableNotificationContent()
        content.title = "OAuth Token Expiring"
        content.subtitle = server
        content.body = "Token expires in \(timeDescription). Log in again to refresh."
        content.sound = .default
        content.categoryIdentifier = NotificationCategory.oauthExpiry.rawValue
        content.userInfo = [
            "server": server,
            "expiresIn": expiresIn,
        ]

        await deliver(content: content, identifier: "oauth-\(server)-\(Date().timeIntervalSince1970)")
        markDelivered(key: key)
    }

    /// Notify about a core process error.
    func sendCoreError(error: CoreError) async {
        let key = "core-error"
        guard shouldDeliver(key: key) else { return }

        let content = UNMutableNotificationContent()
        content.title = "MCPProxy Error"
        content.body = error.userMessage
        content.sound = .defaultCritical
        content.categoryIdentifier = NotificationCategory.coreError.rawValue
        content.userInfo = [
            "errorMessage": error.userMessage,
            "isRetryable": error.isRetryable,
        ]

        await deliver(content: content, identifier: "core-error-\(Date().timeIntervalSince1970)")
        markDelivered(key: key)
    }

    /// Notify about an available software update.
    func sendUpdateAvailable(version: String) async {
        let key = "update:\(version)"
        guard shouldDeliver(key: key) else { return }

        let content = UNMutableNotificationContent()
        content.title = "MCPProxy Update Available"
        content.body = "Version \(version) is ready to install."
        content.sound = .default
        content.categoryIdentifier = NotificationCategory.updateAvailable.rawValue
        content.userInfo = [
            "version": version,
        ]

        await deliver(content: content, identifier: "update-\(version)")
        markDelivered(key: key)
    }

    // MARK: - Rate Limiting

    /// Check whether a notification with the given key should be delivered,
    /// based on the rate limit interval.
    private func shouldDeliver(key: String) -> Bool {
        if let lastTime = lastNotifications[key] {
            return Date().timeIntervalSince(lastTime) >= rateLimitInterval
        }
        return true
    }

    /// Record that a notification was delivered for the given key.
    private func markDelivered(key: String) {
        lastNotifications[key] = Date()

        // Prune old entries to prevent unbounded growth
        let cutoff = Date().addingTimeInterval(-rateLimitInterval * 2)
        lastNotifications = lastNotifications.filter { $0.value > cutoff }
    }

    // MARK: - Delivery

    /// Schedule a notification for immediate delivery.
    private func deliver(content: UNMutableNotificationContent, identifier: String) async {
        let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 0.1, repeats: false)
        let request = UNNotificationRequest(
            identifier: identifier,
            content: content,
            trigger: trigger
        )
        do {
            try await center.add(request)
        } catch {
            // Notification delivery failed; non-fatal.
        }
    }

    // MARK: - Category Builder

    /// Build the set of notification categories with their action buttons.
    private func buildCategories() -> Set<UNNotificationCategory> {
        // Sensitive Data: View Details, Dismiss
        let sensitiveDataCategory = UNNotificationCategory(
            identifier: NotificationCategory.sensitiveData.rawValue,
            actions: [
                UNNotificationAction(
                    identifier: NotificationAction.viewDetails.rawValue,
                    title: "View Details",
                    options: [.foreground]
                ),
                UNNotificationAction(
                    identifier: NotificationAction.dismiss.rawValue,
                    title: "Dismiss",
                    options: [.destructive]
                ),
            ],
            intentIdentifiers: [],
            hiddenPreviewsBodyPlaceholder: "Sensitive data was detected"
        )

        // Quarantine: Approve, View Details
        let quarantineCategory = UNNotificationCategory(
            identifier: NotificationCategory.quarantine.rawValue,
            actions: [
                UNNotificationAction(
                    identifier: NotificationAction.approve.rawValue,
                    title: "Approve",
                    options: [.foreground]
                ),
                UNNotificationAction(
                    identifier: NotificationAction.viewDetails.rawValue,
                    title: "View Details",
                    options: [.foreground]
                ),
            ],
            intentIdentifiers: [],
            hiddenPreviewsBodyPlaceholder: "A server was quarantined"
        )

        // OAuth Expiry: Log In, Dismiss
        let oauthExpiryCategory = UNNotificationCategory(
            identifier: NotificationCategory.oauthExpiry.rawValue,
            actions: [
                UNNotificationAction(
                    identifier: NotificationAction.login.rawValue,
                    title: "Log In",
                    options: [.foreground]
                ),
                UNNotificationAction(
                    identifier: NotificationAction.dismiss.rawValue,
                    title: "Dismiss",
                    options: []
                ),
            ],
            intentIdentifiers: [],
            hiddenPreviewsBodyPlaceholder: "OAuth token expiring"
        )

        // Core Error: Restart, View Details
        let coreErrorCategory = UNNotificationCategory(
            identifier: NotificationCategory.coreError.rawValue,
            actions: [
                UNNotificationAction(
                    identifier: NotificationAction.restart.rawValue,
                    title: "Restart",
                    options: [.foreground]
                ),
                UNNotificationAction(
                    identifier: NotificationAction.viewDetails.rawValue,
                    title: "View Logs",
                    options: [.foreground]
                ),
            ],
            intentIdentifiers: [],
            hiddenPreviewsBodyPlaceholder: "MCPProxy encountered an error"
        )

        // Update Available: Update, Dismiss
        let updateCategory = UNNotificationCategory(
            identifier: NotificationCategory.updateAvailable.rawValue,
            actions: [
                UNNotificationAction(
                    identifier: NotificationAction.update.rawValue,
                    title: "Update Now",
                    options: [.foreground]
                ),
                UNNotificationAction(
                    identifier: NotificationAction.dismiss.rawValue,
                    title: "Later",
                    options: []
                ),
            ],
            intentIdentifiers: [],
            hiddenPreviewsBodyPlaceholder: "An update is available"
        )

        return [
            sensitiveDataCategory,
            quarantineCategory,
            oauthExpiryCategory,
            coreErrorCategory,
            updateCategory,
        ]
    }
}
