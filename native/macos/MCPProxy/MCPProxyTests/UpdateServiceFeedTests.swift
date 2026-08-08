// UpdateServiceFeedTests.swift
// MCPProxyTests
//
// Spec 092 FR-010 / FR-012 / FR-015 / FR-017 — the service that owns both
// update brains, driven through the same seams Sparkle drives in production
// (`FeedUpdating` for the calls out, `FeedUpdaterObserver` for the calls back),
// and then through the REAL tray menu so a renamed slot fails here.

import XCTest
import AppKit
@testable import MCPProxy

/// Records what the service asks of a feed updater and lets a test answer.
final class StubFeedUpdater: FeedUpdating {
    var isAvailable: Bool
    var unavailableReason: String?
    private(set) var appliedPolicies: [EffectiveUpdatePolicy] = []
    private(set) var checks: [Bool] = []          // userInitiated flags

    init(isAvailable: Bool = true) { self.isAvailable = isAvailable }

    func apply(policy: EffectiveUpdatePolicy) { appliedPolicies.append(policy) }
    func check(userInitiated: Bool) { checks.append(userInitiated) }
}

@MainActor
final class UpdateServiceFeedTests: XCTestCase {

    /// Counts legacy checks so the suite can assert FR-017's fallback fires
    /// WITHOUT issuing a real GitHub request from a unit test.
    private final class LegacyCheckCounter { var count = 0 }
    private var legacyCounter = LegacyCheckCounter()

    override func setUp() {
        super.setUp()
        legacyCounter = LegacyCheckCounter()
    }

    private func makeService(
        env: [String: String] = [:],
        bundlePath: String = "/Applications/MCPProxy.app",
        feed: StubFeedUpdater? = StubFeedUpdater()
    ) -> (UpdateService, StubFeedUpdater?) {
        let counter = legacyCounter
        let service = UpdateService(
            environment: env,
            hostBundleURL: URL(fileURLWithPath: bundlePath),
            feedUpdater: feed,
            legacyCheck: { counter.count += 1 }
        )
        return (service, feed)
    }

    // MARK: - FR-015: gating

    func testUnattendedCheckIsSkippedWhenThePolicyDisablesIt() {
        let (service, feed) = makeService(env: ["MCPPROXY_DISABLE_AUTO_UPDATE": "true"])
        service.checkForUpdatesInBackground()
        XCTAssertEqual(feed?.checks, [], "an automatic check must not reach the feed")
        XCTAssertEqual(legacyCounter.count, 0,
                       "FR-015 governs EVERY tray-side check, not only the feed one")
    }

    func testUserInitiatedCheckRunsEvenWithTheKillSwitchOn() {
        let (service, feed) = makeService(env: ["MCPPROXY_DISABLE_AUTO_UPDATE": "true"])
        service.checkForUpdates()
        XCTAssertEqual(feed?.checks, [true],
                       "FR-015: user-initiated 'Check for Updates' remains available")
        XCTAssertTrue(service.canCheckForUpdates)
    }

    func testUnattendedCheckRunsWhenAllowed() {
        let (service, feed) = makeService()
        service.checkForUpdatesInBackground()
        XCTAssertEqual(feed?.checks, [false])
        XCTAssertEqual(legacyCounter.count, 1,
                       "the legacy check stays alive as FR-017's fallback source")
    }

    func testCorePolicyIsForwardedToTheFeedUpdater() {
        let (service, feed) = makeService()
        service.applyCorePolicy(
            CoreUpdatePolicy(enabled: true, channel: "rc", nudgesSuppressed: false)
        )
        XCTAssertEqual(service.policy.channel, .rc)
        XCTAssertEqual(feed?.appliedPolicies.last?.channel, .rc,
                       "the feed must learn the channel or FR-014 cannot hold")
    }

    func testDisablingThePolicyRetractsAnAlreadyAdvertisedUpdate() {
        let (service, _) = makeService()
        service.feedUpdater(didFindVersion: "0.55.0")
        service.setCoreReportedVersion("0.55.0")
        XCTAssertFalse(service.menuEntries.isEmpty)

        service.applyCorePolicy(
            CoreUpdatePolicy(enabled: false, channel: "stable", nudgesSuppressed: false)
        )
        XCTAssertTrue(service.menuEntries.isEmpty,
                      "a nudge the operator just disabled must not linger in the menu")
    }

    // MARK: - FR-017: one owner

    func testFeedOfferBeatsTheLegacyResultForTheSameVersion() {
        let (service, _) = makeService()
        service.feedUpdater(didFindVersion: "v0.55.0")
        service.setCoreReportedVersion("0.55.0")
        XCTAssertEqual(service.menuEntries, [.oneClick(version: "0.55.0")])
    }

    func testWithoutAFeedTheOfferDegradesToGuidance() {
        let (service, _) = makeService(feed: StubFeedUpdater(isAvailable: false))
        service.setCoreReportedVersion("0.55.0")
        XCTAssertEqual(service.menuEntries, [.browserGuidance(version: "0.55.0")])
    }

    func testAnUnavailableFeedNeverContributesAOneClickItem() {
        let (service, _) = makeService(feed: StubFeedUpdater(isAvailable: false))
        // Even if a stale offer is somehow published, it cannot be actioned.
        service.feedUpdater(didFindVersion: "0.55.0")
        XCTAssertEqual(service.menuEntries, [])
    }

    func testTheCoreReportedVersionNeverGoesBackwards() {
        let (service, _) = makeService(feed: StubFeedUpdater(isAvailable: false))
        service.setCoreReportedVersion("0.56.0")
        service.setCoreReportedVersion("0.55.0")
        XCTAssertEqual(service.menuEntries, [.browserGuidance(version: "0.56.0")])
    }

    func testFeedWithdrawalClearsTheOneClickItem() {
        let (service, _) = makeService()
        service.feedUpdater(didFindVersion: "0.55.0")
        service.feedUpdaterDidNotFindUpdate()
        XCTAssertEqual(service.menuEntries, [])
    }

    // MARK: - FR-016

    func testATranslocatedAppGetsAnExplanationInsteadOfAOneClickItem() {
        let (service, _) = makeService(
            bundlePath: "/private/var/folders/x/AppTranslocation/1/d/MCPProxy.app"
        )
        service.feedUpdater(didFindVersion: "0.55.0")
        guard case .blocked = service.menuEntries.first else {
            return XCTFail("expected the blocked reason first, got \(service.menuEntries)")
        }
        XCTAssertEqual(service.menuEntries.last, .browserGuidance(version: "0.55.0"))
        XCTAssertNotNil(service.blockedReason)
    }

    func testFeedErrorsAreRecordedRatherThanSwallowed() {
        let (service, _) = makeService()
        service.feedUpdater(didFailWith: "the update is improperly signed")
        XCTAssertEqual(service.lastErrorMessage, "the update is improperly signed")
    }

    // MARK: - FR-012

    func testInstallHookStopsTheManagedCoreSynchronously() {
        let (service, _) = makeService()
        var stopped = 0
        service.stopManagedCore = { stopped += 1 }
        service.feedUpdaterWillInstallUpdate()
        XCTAssertEqual(stopped, 1,
                       "the core must be down BEFORE the delegate call returns — Sparkle "
                       + "replaces the bundle the moment it does")
    }

    func testInstallHookIsSafeWithoutACore() {
        let (service, _) = makeService()
        service.feedUpdaterWillInstallUpdate()   // must not trap
    }

    // MARK: - FR-010 through the real menu

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    func testTheOneClickItemIsRenderedAndActionable() throws {
        let (service, _) = makeService()
        service.feedUpdater(didFindVersion: "0.55.0")

        let host = TestMenuHost()
        let controller = AppController(
            glanceDataSource: CountingGlanceDataSource(),
            menuHost: host,
            updateService: service
        )
        controller.appState.coreState = .connected
        controller.rebuildMenu()

        let titles = (host.menu?.items ?? []).map(\.title)
        let item = try XCTUnwrap((host.menu?.items ?? []).first {
            $0.title.hasPrefix("Update 0.55.0")
        }, "FR-010's item is missing from \(titles)")
        XCTAssertEqual(item.title, "Update 0.55.0 — ready to restart?")
        XCTAssertNotNil(item.action)
        XCTAssertTrue(item.target === controller)

        XCTAssertFalse(titles.contains { $0.hasPrefix("Update available:") },
                       "FR-017: the legacy nudge must not compete with the one-click item")
    }

    func testTheBlockedReasonIsRenderedAsItsOwnItem() throws {
        let (service, _) = makeService(
            bundlePath: "/private/var/folders/x/AppTranslocation/1/d/MCPProxy.app"
        )
        service.feedUpdater(didFindVersion: "0.55.0")

        let host = TestMenuHost()
        let controller = AppController(
            glanceDataSource: CountingGlanceDataSource(),
            menuHost: host,
            updateService: service
        )
        controller.appState.coreState = .connected
        controller.rebuildMenu()

        let items = host.menu?.items ?? []
        let blocked = try XCTUnwrap(items.first { $0.title.contains("Can’t update") },
                                    "FR-016 must be visible, not logged")
        XCTAssertNotNil(blocked.action, "clicking it must explain what to do")
        XCTAssertFalse(items.contains { $0.title.hasPrefix("Update 0.55.0 —") },
                       "no one-click item where a one-click install cannot work")
    }
}
