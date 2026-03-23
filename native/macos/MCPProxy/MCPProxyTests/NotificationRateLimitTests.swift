import XCTest
@testable import MCPProxy

/// Tests the notification rate limiting algorithm used by `NotificationService`.
///
/// Since the actual rate limiting is implemented as private methods on an actor,
/// we extract and test the same algorithm here using a minimal testable struct
/// that mirrors the `shouldDeliver` / `markDelivered` pattern exactly.
///
/// The production code in `NotificationService` uses the same logic:
/// - `lastNotifications: [String: Date]` dictionary
/// - `rateLimitInterval: TimeInterval = 300` (5 minutes)
/// - `shouldDeliver(key:)` checks if enough time has elapsed
/// - `markDelivered(key:)` records the delivery time and prunes old entries

/// Minimal mirror of NotificationService rate limiting for testability.
private struct RateLimiter {
    var lastNotifications: [String: Date] = [:]
    let rateLimitInterval: TimeInterval

    init(rateLimitInterval: TimeInterval = 300) {
        self.rateLimitInterval = rateLimitInterval
    }

    func shouldDeliver(key: String, now: Date = Date()) -> Bool {
        if let lastTime = lastNotifications[key] {
            return now.timeIntervalSince(lastTime) >= rateLimitInterval
        }
        return true
    }

    mutating func markDelivered(key: String, now: Date = Date()) {
        lastNotifications[key] = now

        // Prune old entries (same as production code)
        let cutoff = now.addingTimeInterval(-rateLimitInterval * 2)
        lastNotifications = lastNotifications.filter { $0.value > cutoff }
    }
}

final class NotificationRateLimitTests: XCTestCase {

    // MARK: - Basic Rate Limiting

    func testFirstNotificationForKeyIsSent() {
        let limiter = RateLimiter(rateLimitInterval: 300)
        XCTAssertTrue(limiter.shouldDeliver(key: "quarantine:github"))
    }

    func testSecondNotificationWithinIntervalIsSuppressed() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "quarantine:github", now: now)

        // 1 second later — should be suppressed
        let soon = now.addingTimeInterval(1)
        XCTAssertFalse(limiter.shouldDeliver(key: "quarantine:github", now: soon))
    }

    func testNotificationExactlyAtIntervalBoundaryIsSent() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "quarantine:github", now: now)

        // Exactly 300 seconds later (5 minutes)
        let later = now.addingTimeInterval(300)
        XCTAssertTrue(limiter.shouldDeliver(key: "quarantine:github", now: later))
    }

    func testNotificationAfterIntervalIsSent() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "quarantine:github", now: now)

        // 301 seconds later (just past 5 minutes)
        let later = now.addingTimeInterval(301)
        XCTAssertTrue(limiter.shouldDeliver(key: "quarantine:github", now: later))
    }

    func testNotificationAt4MinutesIsSuppressed() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "core-error", now: now)

        // 240 seconds later (4 minutes) — still within interval
        let fourMin = now.addingTimeInterval(240)
        XCTAssertFalse(limiter.shouldDeliver(key: "core-error", now: fourMin))
    }

    // MARK: - Key Independence

    func testDifferentKeysDoNotAffectEachOther() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "quarantine:github", now: now)

        // A different key should still be deliverable immediately
        XCTAssertTrue(limiter.shouldDeliver(key: "quarantine:gitlab", now: now))
        XCTAssertTrue(limiter.shouldDeliver(key: "sensitive:github:create_issue", now: now))
        XCTAssertTrue(limiter.shouldDeliver(key: "core-error", now: now))
    }

    func testMultipleKeysTrackedIndependently() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "key-a", now: now)
        limiter.markDelivered(key: "key-b", now: now.addingTimeInterval(60))

        // At t+120s: key-a suppressed, key-b suppressed
        let t120 = now.addingTimeInterval(120)
        XCTAssertFalse(limiter.shouldDeliver(key: "key-a", now: t120))
        XCTAssertFalse(limiter.shouldDeliver(key: "key-b", now: t120))

        // At t+300s: key-a allowed (300s elapsed), key-b still suppressed (only 240s)
        let t300 = now.addingTimeInterval(300)
        XCTAssertTrue(limiter.shouldDeliver(key: "key-a", now: t300))
        XCTAssertFalse(limiter.shouldDeliver(key: "key-b", now: t300))

        // At t+360s: both allowed
        let t360 = now.addingTimeInterval(360)
        XCTAssertTrue(limiter.shouldDeliver(key: "key-a", now: t360))
        XCTAssertTrue(limiter.shouldDeliver(key: "key-b", now: t360))
    }

    // MARK: - Re-delivery After Interval

    func testRedeliveryResetsTheClock() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        // First delivery
        limiter.markDelivered(key: "test", now: now)

        // Wait 5 minutes and deliver again
        let t300 = now.addingTimeInterval(300)
        XCTAssertTrue(limiter.shouldDeliver(key: "test", now: t300))
        limiter.markDelivered(key: "test", now: t300)

        // 1 second after second delivery — suppressed
        let t301 = now.addingTimeInterval(301)
        XCTAssertFalse(limiter.shouldDeliver(key: "test", now: t301))

        // 5 minutes after second delivery — allowed
        let t600 = now.addingTimeInterval(600)
        XCTAssertTrue(limiter.shouldDeliver(key: "test", now: t600))
    }

    // MARK: - Pruning

    func testOldEntriesArePruned() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        // Mark several keys
        limiter.markDelivered(key: "old-1", now: now)
        limiter.markDelivered(key: "old-2", now: now)

        // 11 minutes later (> 2x interval), mark a new key — old entries should be pruned
        let later = now.addingTimeInterval(660) // 11 minutes
        limiter.markDelivered(key: "new", now: later)

        // Old keys should have been pruned from the dictionary
        XCTAssertNil(limiter.lastNotifications["old-1"])
        XCTAssertNil(limiter.lastNotifications["old-2"])
        XCTAssertNotNil(limiter.lastNotifications["new"])
    }

    func testRecentEntriesAreNotPruned() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        limiter.markDelivered(key: "recent", now: now)

        // 1 minute later, mark another key
        let later = now.addingTimeInterval(60)
        limiter.markDelivered(key: "other", now: later)

        // "recent" should NOT be pruned (it's within 2x interval)
        XCTAssertNotNil(limiter.lastNotifications["recent"])
        XCTAssertNotNil(limiter.lastNotifications["other"])
    }

    // MARK: - Key Format Matches Production

    func testProductionKeyFormats() {
        // Verify the key formats used by NotificationService methods
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        // sendSensitiveDataAlert uses "sensitive:<server>:<tool>"
        let sensitiveKey = "sensitive:github:create_issue"
        XCTAssertTrue(limiter.shouldDeliver(key: sensitiveKey, now: now))
        limiter.markDelivered(key: sensitiveKey, now: now)
        XCTAssertFalse(limiter.shouldDeliver(key: sensitiveKey, now: now.addingTimeInterval(1)))

        // sendQuarantineAlert uses "quarantine:<server>"
        let quarantineKey = "quarantine:github"
        XCTAssertTrue(limiter.shouldDeliver(key: quarantineKey, now: now))

        // sendOAuthExpiryAlert uses "oauth:<server>"
        let oauthKey = "oauth:github"
        XCTAssertTrue(limiter.shouldDeliver(key: oauthKey, now: now))

        // sendCoreError uses "core-error"
        let coreErrorKey = "core-error"
        XCTAssertTrue(limiter.shouldDeliver(key: coreErrorKey, now: now))

        // sendUpdateAvailable uses "update:<version>"
        let updateKey = "update:v0.21.0"
        XCTAssertTrue(limiter.shouldDeliver(key: updateKey, now: now))
    }

    // MARK: - Notification Categories and Actions

    func testNotificationCategoryRawValues() {
        XCTAssertEqual(NotificationCategory.sensitiveData.rawValue, "SENSITIVE_DATA")
        XCTAssertEqual(NotificationCategory.quarantine.rawValue, "QUARANTINE")
        XCTAssertEqual(NotificationCategory.oauthExpiry.rawValue, "OAUTH_EXPIRY")
        XCTAssertEqual(NotificationCategory.coreError.rawValue, "CORE_ERROR")
        XCTAssertEqual(NotificationCategory.updateAvailable.rawValue, "UPDATE_AVAILABLE")
    }

    func testNotificationActionRawValues() {
        XCTAssertEqual(NotificationAction.viewDetails.rawValue, "VIEW_DETAILS")
        XCTAssertEqual(NotificationAction.approve.rawValue, "APPROVE")
        XCTAssertEqual(NotificationAction.login.rawValue, "LOG_IN")
        XCTAssertEqual(NotificationAction.restart.rawValue, "RESTART")
        XCTAssertEqual(NotificationAction.dismiss.rawValue, "DISMISS")
        XCTAssertEqual(NotificationAction.update.rawValue, "UPDATE")
    }

    // MARK: - Edge Cases

    func testEmptyKeyWorks() {
        var limiter = RateLimiter(rateLimitInterval: 300)
        let now = Date()

        XCTAssertTrue(limiter.shouldDeliver(key: "", now: now))
        limiter.markDelivered(key: "", now: now)
        XCTAssertFalse(limiter.shouldDeliver(key: "", now: now.addingTimeInterval(1)))
    }

    func testZeroIntervalAlwaysAllows() {
        var limiter = RateLimiter(rateLimitInterval: 0)
        let now = Date()

        limiter.markDelivered(key: "test", now: now)
        // With 0 interval, even immediate check should pass (0 >= 0)
        XCTAssertTrue(limiter.shouldDeliver(key: "test", now: now))
    }
}
