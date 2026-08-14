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

    /// Forget the setup traffic so a test asserts only what it provoked.
    func resetRecordings() {
        appliedPolicies.removeAll()
        checks.removeAll()
    }
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

    /// A service whose core policy has already arrived, which is the state
    /// every test below except the FR-015 startup ones is about.
    private func makeService(
        env: [String: String] = [:],
        bundlePath: String = "/Applications/MCPProxy.app",
        hostVersion: String? = "dev",
        feed: StubFeedUpdater? = StubFeedUpdater(),
        corePolicy: CoreUpdatePolicy? = .legacyDefault
    ) -> (UpdateService, StubFeedUpdater?) {
        // hostVersion defaults to "dev" (unparseable ⇒ no build-identity clamp)
        // so the channel-forwarding tests below are deterministic instead of
        // depending on whatever bundle bundlePath resolves to on the machine.
        let counter = legacyCounter
        let service = UpdateService(
            environment: env,
            hostBundleURL: URL(fileURLWithPath: bundlePath),
            hostVersion: hostVersion,
            feedUpdater: feed,
            legacyCheck: { counter.count += 1 }
        )
        if let corePolicy {
            service.applyCorePolicy(corePolicy)
            feed?.resetRecordings()
        }
        return (service, feed)
    }

    // MARK: - FR-015: nothing unattended before the policy arrives

    func testNoUnattendedCheckRunsBeforeTheCoreReportsItsPolicy() {
        let (service, feed) = makeService(corePolicy: nil)
        service.checkForUpdatesInBackground()
        XCTAssertEqual(feed?.checks, [],
                       "the launch check rides on the core's version, which Combine "
                       + "delivers before the policy — checking here checks under a "
                       + "policy the user may have switched off")
        XCTAssertEqual(legacyCounter.count, 0)
        XCTAssertFalse(service.policy.automaticChecksAllowed)
    }

    func testAUserInitiatedCheckStillWorksBeforeThePolicyArrives() {
        let (service, feed) = makeService(corePolicy: nil)
        service.checkForUpdates()
        XCTAssertEqual(feed?.checks, [true],
                       "FR-015: someone standing at the menu is not an unattended check")
    }

    func testTheCheckResumesOnceThePolicyArrives() {
        let (service, feed) = makeService(corePolicy: nil)
        service.checkForUpdatesInBackground()
        XCTAssertEqual(feed?.checks, [])

        service.applyCorePolicy(.legacyDefault)
        service.checkForUpdatesInBackground()
        XCTAssertEqual(feed?.checks, [false])
        XCTAssertEqual(legacyCounter.count, 1)
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

    func testStableHostAppNeverForwardsRCToTheFeed() {
        // A stable tray app attached to an rc core must clamp itself to stable
        // end-to-end: the feed updater must receive channel=stable so Sparkle
        // fetches the stable feed / empty allowedChannels (build-identity rule).
        // Start with no core policy (awaitingCore) so the rc→stable clamp
        // produces a policy that genuinely differs from the baseline and is
        // therefore forwarded to the feed (applyCorePolicy is idempotent).
        let (service, feed) = makeService(hostVersion: "0.56.0", corePolicy: nil)
        service.applyCorePolicy(
            CoreUpdatePolicy(enabled: true, channel: "rc", nudgesSuppressed: false)
        )
        XCTAssertEqual(service.policy.channel, .stable,
                       "a stable app must not track rc even behind an rc core")
        XCTAssertEqual(feed?.appliedPolicies.last?.channel, .stable,
                       "the feed must receive the clamped stable channel")
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

    /// FR-016 / the "update feed unreachable" edge case. Nothing publishes the
    /// appcast at its Info.plist URL yet (see the activation checklist in
    /// docs/features/auto-update.md), so a 404 is the state most installs are
    /// in: it must read as "no update from the feed" and hand the menu to the
    /// legacy browser path, not as an error that eats the offer.
    func testAnUnreachableFeedFallsBackToBrowserGuidance() {
        let (service, _) = makeService()
        service.setCoreReportedVersion("0.55.0")
        service.feedUpdater(didFailWith:
            "The update feed could not be loaded (404).")

        XCTAssertEqual(service.menuEntries, [.browserGuidance(version: "0.55.0")])
        XCTAssertNotNil(service.lastErrorMessage, "the reason stays available for the log")
    }

    func testAFeedFailureDoesNotStrandAPreviousOffer() {
        // A transient failure after a successful check must not leave a
        // one-click item pointing at an update the feed can no longer serve.
        let (service, _) = makeService()
        service.feedUpdater(didFindVersion: "0.55.0")
        service.feedUpdaterDidNotFindUpdate()
        service.feedUpdater(didFailWith: "connection lost")
        XCTAssertEqual(service.menuEntries, [])
    }

    // MARK: - FR-012

    func testInstallHookStopsTheManagedCoreSynchronously() {
        let (service, _) = makeService()
        var stopped = 0
        service.stopManagedCore = { stopped += 1; return true }
        XCTAssertTrue(service.feedUpdaterWillInstallUpdate())
        XCTAssertEqual(stopped, 1,
                       "the core must be down BEFORE the delegate call returns — Sparkle "
                       + "proceeds with the install the moment it does")
    }

    func testInstallHookIsSafeWithoutACore() {
        let (service, _) = makeService()
        XCTAssertTrue(service.feedUpdaterWillInstallUpdate(),
                      "no core to stop is a stopped core; the update must not be blocked "
                      + "by the absence of the thing it was going to shut down")
    }

    func testAnUnstoppableCoreVetoesTheInstall() {
        let (service, _) = makeService()
        service.stopManagedCore = { false }
        XCTAssertFalse(service.feedUpdaterWillInstallUpdate(),
                       "replacing the bundle under a live core is issue #957 happening again")
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
