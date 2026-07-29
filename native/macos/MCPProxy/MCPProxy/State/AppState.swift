// AppState.swift
// MCPProxy
//
// Root observable state for the MCPProxy tray application.
// All UI views bind to this single source of truth.
//
// Type reuse:
//   - CoreState, CoreError, CoreOwnership, ReconnectionPolicy -> Core/CoreState.swift
//   - ServerStatus, ActivityEntry, HealthStatus, etc.          -> API/Models.swift

import Foundation
import Combine

// MARK: - Health Indicator (tray icon badge)

/// Tray icon badge level, derived from aggregated server health.
enum HealthIndicator: String, Sendable {
    case healthy
    case degraded
    case unhealthy
    case disconnected
}

// MARK: - App State

/// The root observable state object for the entire tray application.
/// All views bind to properties on this object.
///
/// Uses ObservableObject (not @Observable) for macOS 13 compatibility.
/// Server and activity data use the Codable model types from `API/Models.swift`.
/// Core lifecycle state uses the state machine from `Core/CoreState.swift`.
final class AppState: ObservableObject {

    // MARK: Core lifecycle

    /// Current core process state (uses CoreState from CoreState.swift).
    ///
    /// Tray Glance: any state other than `.connected` clears the glance feeds.
    /// The connect path flips to `.connected` BEFORE the first refresh completes
    /// (CoreProcessManager.launchAndConnect), so without this reset the menu
    /// would briefly present the previous core's numbers as live. The reset lives
    /// in `didSet` rather than in `transition(to:)` because two call sites assign
    /// `coreState` directly (CoreProcessManager.awaitExternalCore,
    /// MCPProxyApp.stopCore) and would otherwise bypass it.
    @Published var coreState: CoreState = .idle {
        didSet {
            if coreState != .connected { clearGlanceState() }
        }
    }

    /// Who owns the core process.
    @Published var ownership: CoreOwnership = .trayManaged

    // MARK: Server inventory (ServerStatus from Models.swift)

    @Published var servers: [ServerStatus] = []
    @Published var connectedCount: Int = 0
    @Published var totalServers: Int = 0
    @Published var totalTools: Int = 0

    // MARK: - Profiles (Profiles v2 T5)
    /// Configured profiles for the tray profile switcher.
    @Published var profiles: [ProfileSummary] = []
    /// Server-level default active profile slug; empty means "all servers".
    @Published var activeProfile: String = ""

    /// Set to true once the tray has received its first response from
    /// `/api/v1/servers`. Used by `statusSummary` to distinguish "haven't
    /// fetched yet" from "fetched and the list is genuinely empty", so the
    /// menu shows "Loading…" instead of misleading "No servers configured"
    /// during the cold-start window after the core becomes reachable.
    @Published var serversLoaded: Bool = false

    // MARK: Activity & security (ActivityEntry from Models.swift)

    @Published var recentActivity: [ActivityEntry] = []
    @Published var recentSessions: [APIClient.MCPSession] = []
    @Published var sensitiveDataAlertCount: Int = 0
    @Published var quarantinedToolsCount: Int = 0

    /// Monotonic counter bumped on each SSE activity event for live updates.
    @Published var activityVersion: Int = 0

    /// Monotonic counter bumped on SSE servers.changed / config.reloaded for live updates.
    @Published var serversVersion: Int = 0

    // MARK: Tray Glance feeds (separate from the shared recentActivity/recentSessions)

    /// Tool-call activity for the tray glance section, fetched with a `type`
    /// filter. Deliberately NOT the same feed as `recentActivity`: the native
    /// Dashboard renders the full activity log (security scans, quarantine
    /// changes, OAuth events) from that one, so narrowing it would gut the view.
    @Published var glanceActivity: [ActivityEntry] = []

    /// Active-only MCP sessions for the tray glance "Clients" rows. Separate from
    /// `recentSessions`, which ActivityView and DashboardView use to resolve
    /// session ids to client names and therefore must keep closed sessions.
    @Published var glanceSessions: [APIClient.MCPSession] = []

    /// Hourly call timeline for the last 24h. `nil` means "not loaded yet";
    /// an empty array means "loaded, and the proxy was idle".
    @Published var usageTimeline: [UsageBucket]?

    /// Calls recorded in the CURRENT UTC hour. `nil` means "not loaded yet".
    @Published var callsThisHour: Int?

    /// Last usage-refresh failure, surfaced as a muted row in the histogram
    /// submenu. `nil` means "no failure recorded"; the next successful refresh
    /// clears it. Without this the submenu could not tell "still loading" from
    /// "the fetch failed" — both leave `usageTimeline` nil, and a permanently
    /// failing refresh would sit on "Loading…" forever.
    @Published var usageError: String?

    /// Which glance feed a recorded failure came from.
    enum GlanceFeed: String, CaseIterable {
        case activity, sessions, usage
    }

    /// Consecutive failed refreshes per feed; a success resets that feed's
    /// entry. Per-feed rather than one shared counter because the three fetch
    /// independently — a single counter that any success reset would report a
    /// permanently failing feed as healthy every 30 seconds.
    private(set) var glanceFailureStreak: [GlanceFeed: Int] = [:]

    /// The most recent glance refresh failure, whichever feed produced it.
    private(set) var glanceError: String?

    // MARK: Token metrics (from status response)

    @Published var tokenMetrics: TokenMetrics?

    // MARK: API Client (shared with all views via AppState)

    /// The API client for the running core, set once connected.
    /// Views read this instead of receiving it as a parameter,
    /// which avoids the need to replace NSHostingView when the client becomes available.
    @Published var apiClient: APIClient?

    // MARK: Security status

    @Published var dockerAvailable: Bool = false
    @Published var quarantineEnabled: Bool = true

    // MARK: Metadata

    @Published var version: String = ""
    @Published var updateAvailable: String? = nil
    @Published var autoStartEnabled: Bool = false

    /// Base URL for the Web UI, populated from /api/v1/info on connect.
    /// Falls back to localhost:8080 until the actual URL is fetched.
    @Published var webUIBaseURL: String = "http://127.0.0.1:8080"

    /// Whether the user has explicitly stopped MCPProxy (distinct from idle/error states).
    /// Session-only by design (GH #410): the persistent choice is `startCoreOnLaunch`,
    /// so stopping the core to debug something does not silently leave the tray
    /// dead after the next reboot.
    @Published var isStopped: Bool = false

    /// GH #410 — whether the tray may start a core when it opens. The one piece of
    /// launcher state the tray persists; it says nothing about whether a core is
    /// running (that is always discovered live from the socket). Mirrors
    /// CoreLaunchPolicy so SwiftUI can bind to it.
    @Published var startCoreOnLaunch: Bool = CoreLaunchPolicy().startCoreOnLaunch {
        didSet { CoreLaunchPolicy().startCoreOnLaunch = startCoreOnLaunch }
    }

    /// True when MCPPROXY_TRAY_SKIP_CORE pins core autostart off, so the Settings
    /// toggle can render disabled instead of disagreeing with actual behaviour.
    let coreLaunchPinnedOffByEnvironment: Bool = CoreLaunchPolicy().isPinnedOffByEnvironment

    /// User-adjustable font scale (1.0 = default, persisted in UserDefaults).
    /// Standard macOS Cmd+/Cmd- changes this by 0.1 increments.
    @Published var fontScale: CGFloat = UserDefaults.standard.double(forKey: "fontScale") == 0
        ? 1.0 : CGFloat(UserDefaults.standard.double(forKey: "fontScale")) {
        didSet { UserDefaults.standard.set(Double(fontScale), forKey: "fontScale") }
    }

    // MARK: Computed properties

    /// Servers that need user intervention — NOT including intentionally disabled servers.
    /// Only: auth required (login), connection errors (restart), quarantine (approve).
    var serversNeedingAttention: [ServerStatus] {
        servers.filter { server in
            guard let action = server.health?.action, !action.isEmpty else { return false }
            // "enable" means disabled by user — intentional, not attention-worthy
            return action != "enable"
        }
    }

    /// Spec 044 — servers that have an attached, classified diagnostic with
    /// warn/error severity. These drive the "Fix issues" menu group and the
    /// tray badge tint.
    ///
    /// MCP-1819/T3: OAuth login-required servers are excluded. Pre-T1 the
    /// backend classifies that state as an error-severity
    /// MCPX_UNKNOWN_UNCLASSIFIED diagnostic, which would otherwise read as a
    /// "file a bug" hard error. A server that just needs sign-in is surfaced
    /// calmly via `serversNeedingAttention` (the "Sign in" affordance) instead.
    var serversWithDiagnostic: [ServerStatus] {
        servers.filter { $0.hasAttentionDiagnostic && !$0.isOAuthLoginRequired }
    }

    /// Highest-severity diagnostic across enabled servers. Returns nil when
    /// no diagnostics are attached. Used by TrayIcon to colour the badge.
    ///
    /// MCP-1819/T3: OAuth login-required servers are skipped so a server that
    /// merely needs sign-in does not tint the tray icon badge red/orange — the
    /// calm "Needs Attention / Sign in" path owns that state instead.
    var worstDiagnosticSeverity: String? {
        var sawWarn = false
        for srv in servers where srv.enabled && !srv.isOAuthLoginRequired {
            guard let d = srv.diagnostic else { continue }
            if d.severity == "error" { return "error" }
            if d.severity == "warn" { sawWarn = true }
        }
        return sawWarn ? "warn" : nil
    }

    /// Aggregate health indicator for the tray icon badge.
    /// Only considers ENABLED servers. Disabled servers are intentional — don't flag them.
    /// Uses majority-based logic: green if most are healthy, yellow if some degraded,
    /// red only if the majority are unhealthy.
    var healthLevel: HealthIndicator {
        guard coreState == .connected else {
            return .disconnected
        }

        let enabled = servers.filter { $0.enabled }
        if enabled.isEmpty {
            return .healthy
        }

        let unhealthyCount = enabled.filter { $0.health?.level == "unhealthy" }.count
        let degradedCount = enabled.filter { $0.health?.level == "degraded" }.count
        let total = enabled.count

        // Red only if more than half of enabled servers are unhealthy
        if unhealthyCount > total / 2 {
            return .unhealthy
        }
        // Yellow if any degraded or unhealthy (but not majority)
        if unhealthyCount > 0 || degradedCount > 0 {
            return .degraded
        }
        return .healthy
    }

    /// Whether the tray is connected to a running core.
    var isConnected: Bool {
        coreState == .connected
    }

    /// One-line summary suitable for display in the menu header.
    var statusSummary: String {
        if isStopped { return "Stopped" }
        switch coreState {
        case .connected:
            if !serversLoaded {
                return "Loading…"
            }
            if totalServers == 0 {
                return "No servers configured"
            }
            return "\(connectedCount)/\(totalServers) servers, \(totalTools) tools"
        case .idle:
            return "Idle"
        default:
            return coreState.displayName
        }
    }

    // MARK: Mutating helpers (called from background actors via MainActor)

    /// Update server list and recompute derived counts.
    /// Only publishes changes when the data actually differs to prevent
    /// MenuBarExtra from duplicating menu items on spurious re-renders.
    @MainActor
    func updateServers(_ newServers: [ServerStatus]) {
        let newConnected = newServers.filter { $0.connected }.count
        let newTools = newServers.reduce(0) { $0 + $1.toolCount }
        let newQuarantined = newServers.filter { $0.quarantined }.count

        // Always update server array on servers.changed events.
        // Health status, connection state, and tool counts can change
        // even when the server list itself hasn't changed.
        servers = newServers
        if totalServers != newServers.count { totalServers = newServers.count }
        if connectedCount != newConnected { connectedCount = newConnected }
        if totalTools != newTools { totalTools = newTools }
        if quarantinedToolsCount != newQuarantined { quarantinedToolsCount = newQuarantined }
        if !serversLoaded { serversLoaded = true }
    }

    /// Replace the recent activity list.
    /// Only publishes changes when the data actually differs.
    @MainActor
    func updateActivity(_ entries: [ActivityEntry]) {
        let newIDs = entries.map(\.id)
        let oldIDs = recentActivity.map(\.id)
        if newIDs != oldIDs {
            recentActivity = entries
        }
        let newSensitive = entries.filter { $0.hasSensitiveData == true }.count
        if sensitiveDataAlertCount != newSensitive { sensitiveDataAlertCount = newSensitive }
    }

    /// Transition the core state on the main actor.
    @MainActor
    func transition(to newState: CoreState) {
        coreState = newState
    }

    // MARK: Tray Glance helpers

    /// Truncate a date to the start of its UTC hour. Unix time is UTC by
    /// definition, so flooring the epoch seconds needs no Calendar or TimeZone.
    static func floorToHour(_ date: Date) -> Date {
        let seconds = date.timeIntervalSince1970
        return Date(timeIntervalSince1970: (seconds / 3600).rounded(.down) * 3600)
    }

    /// Calls recorded in the UTC hour containing `now`.
    ///
    /// Buckets are UTC-hour aligned and SPARSE — the endpoint omits hours with no
    /// activity. Picking "the newest bucket" would therefore show a count from
    /// hours ago as if it were current, so this matches on the bucket start and
    /// returns 0 when the current hour has no bucket.
    static func callsInCurrentHour(_ timeline: [UsageBucket], now: Date = Date()) -> Int {
        let currentHour = floorToHour(now)
        for bucket in timeline where floorToHour(bucket.start) == currentHour {
            return bucket.calls
        }
        return 0
    }

    /// Store the 24h timeline and derive the current-hour headline count.
    ///
    /// Ignored unless the core is `.connected`, like the other two glance
    /// updaters — see `updateGlanceActivity` for why.
    ///
    /// Both assignments are guarded. This file's rule (see `updateServers`) is
    /// "only publish when the data actually differs", because every `@Published`
    /// write feeds the debounced `objectWillChange → rebuildMenu()` sink in
    /// MCPProxyApp — an unguarded write here would rebuild the menu every 30s on
    /// a completely idle proxy. `UsageBucket` is `Equatable`, so the guard is free.
    @MainActor
    func updateUsage(timeline: [UsageBucket], now: Date = Date()) {
        guard coreState == .connected else { return }
        if usageTimeline != timeline { usageTimeline = timeline }
        let calls = AppState.callsInCurrentHour(timeline, now: now)
        if callsThisHour != calls { callsThisHour = calls }
        if usageError != nil { usageError = nil }
        clearGlanceFailure(.usage)
    }

    /// Record a failed usage refresh so the histogram submenu can say so
    /// instead of showing "Loading…" forever. Called from the usage refresh's
    /// catch block.
    ///
    /// Guarded on `.connected` for the same reason `updateUsage` and
    /// `updateGlanceActivity` are: a fetch already past its `guard let
    /// apiClient` when the core goes away resolves after `clearGlanceState()`,
    /// and its catch block would write the dead core's failure back over the
    /// state that was just emptied. The submenu would then say "Usage
    /// unavailable" about a core that is merely still starting.
    @MainActor
    func recordUsageFailure(_ message: String) {
        guard coreState == .connected else { return }
        if usageError != message { usageError = message }
        recordGlanceFailure(.usage, message)
    }

    /// Fetch the 24h usage aggregate and publish the outcome — success or
    /// failure.
    ///
    /// The catch is the whole reason `usageError` exists. A failure that only
    /// logged would leave the header count and the histogram submenu sitting on
    /// "Loading…" indefinitely, describing a fetch that is never coming back as
    /// one still in flight.
    ///
    /// Takes a `GlanceDataSource` rather than the concrete client so the
    /// failure path is reachable from a test; untested wiring is exactly how
    /// this state came to be unreachable in the first place.
    @MainActor
    func refreshUsage(from source: GlanceDataSource) async {
        do {
            let usage = try await source.usageAggregate(window: "24h", top: 1)
            updateUsage(timeline: usage.timeline)
        } catch {
            recordUsageFailure(AppState.usageFailureMessage(for: error))
        }
    }

    /// The text shown in the failed row's tooltip. `APIClientError` is a
    /// `LocalizedError`, so this is already "HTTP 503: …" or "Core is not
    /// ready" rather than a Swift type dump.
    static func usageFailureMessage(for error: Error) -> String {
        let text = error.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? "Usage refresh failed" : text
    }

    /// Fetch the glance activity feed and publish the outcome — success or
    /// failure.
    ///
    /// Takes a `GlanceDataSource` rather than the concrete client for the same
    /// reason `refreshUsage(from:)` does: this used to live in
    /// `CoreProcessManager` where the catch block was unreachable from a test,
    /// and an unreachable failure path is how the feed came to swallow errors.
    /// A permanently failing fetch rendered "No tool calls yet" — the same thing
    /// a genuinely idle proxy renders.
    @MainActor
    func refreshGlanceActivity(from source: GlanceDataSource) async {
        do {
            let entries = try await source.glanceActivity(limit: AppState.glanceActivityPageSize)
            updateGlanceActivity(entries)
            clearGlanceFailure(.activity)
        } catch {
            recordGlanceFailure(.activity, AppState.usageFailureMessage(for: error))
        }
    }

    /// Fetch the active-only session feed and publish the outcome — success or
    /// failure. See `refreshGlanceActivity(from:)`; a permanently failing fetch
    /// here rendered "No connected clients".
    @MainActor
    func refreshGlanceSessions(from source: GlanceDataSource) async {
        do {
            let sessions = try await source.activeSessions(limit: AppState.glanceSessionsPageSize)
            updateGlanceSessions(sessions)
            clearGlanceFailure(.sessions)
        } catch {
            recordGlanceFailure(.sessions, AppState.usageFailureMessage(for: error))
        }
    }

    /// Record a failed refresh of one glance feed.
    ///
    /// Guarded on `.connected` for the same reason every publish helper here is
    /// (see `updateGlanceActivity`): a fetch already in flight when the core
    /// goes away resolves into its catch block after `clearGlanceState()`, and
    /// the dead core's failure must not outlive it.
    @MainActor
    func recordGlanceFailure(_ feed: GlanceFeed, _ message: String) {
        guard coreState == .connected else { return }
        glanceFailureStreak[feed, default: 0] += 1
        if glanceError != message { glanceError = message }
    }

    /// Clear one feed's failure record after a successful refresh. Only that
    /// feed's — the others may still be failing.
    @MainActor
    func clearGlanceFailure(_ feed: GlanceFeed) {
        guard coreState == .connected else { return }
        glanceFailureStreak[feed] = 0
        if glanceFailureStreak.values.allSatisfy({ $0 == 0 }), glanceError != nil {
            glanceError = nil
        }
    }

    /// Records requested per glance activity poll.
    static let glanceActivityPageSize = 50

    /// Active sessions requested per poll.
    static let glanceSessionsPageSize = 25

    /// Replace the glance activity feed. Leaves `recentActivity` untouched.
    ///
    /// Ignored unless the core is `.connected`. `clearGlanceState()` alone is not
    /// enough: a fetch already past its `guard let apiClient` when the core goes
    /// away resolves after the reset and would write the dead core's data back
    /// over the cleared fields. `CoreProcessManager.shutdown()` transitions to
    /// `.shuttingDown` before it cancels `refreshTask`, and cancellation would not
    /// close the window anyway — a suspended fetch still resumes and still runs
    /// the update that follows it.
    ///
    /// `ActivityEntry`'s Equatable is id-only (API/Models.swift:570), so guarding
    /// on ids alone would drop the reconciling poll's late corrections: the
    /// sensitive-data flag is computed asynchronously and the final status
    /// arrives on a record whose id has not changed. Fingerprint the fields the
    /// glance rows actually render instead — still cheap, still churn-free.
    @MainActor
    func updateGlanceActivity(_ entries: [ActivityEntry]) {
        guard coreState == .connected else { return }
        func fingerprint(_ list: [ActivityEntry]) -> [String] {
            list.map { "\($0.id)|\($0.status)|\($0.hasSensitiveData == true)" }
        }
        if fingerprint(entries) != fingerprint(glanceActivity) {
            glanceActivity = entries
        }
    }

    /// Upper bound on rows kept in `glanceActivity`. Matches the page size the
    /// reconciling poll requests (`apiClient.glanceActivity(limit: 50)`), so SSE
    /// rows and polled rows agree on depth.
    static let glanceActivityCap = 50

    /// Prepend one optimistic row adapted from an SSE payload (newest first).
    /// Bounded so a busy agent cannot grow the feed without limit; the 30s
    /// reconciling poll replaces the list wholesale with canonical records.
    ///
    /// Guarded on `coreState == .connected` for the same reason as the three
    /// `update*` helpers: SSE teardown is asynchronous, so an event already in
    /// flight when the core goes away would otherwise write a dead core's row
    /// back over state `clearGlanceState()` had just emptied.
    ///
    /// Deliberately without the id-list equality guard `updateGlanceSessions(_:)`
    /// carries — a prepend is by definition a change, and that guard exists only
    /// to stop redundant `@Published` churn on identical poll results.
    @MainActor
    func prependGlanceActivity(_ entry: ActivityEntry) {
        guard coreState == .connected else { return }
        glanceActivity.insert(entry, at: 0)
        if glanceActivity.count > AppState.glanceActivityCap {
            glanceActivity.removeLast(glanceActivity.count - AppState.glanceActivityCap)
        }
    }

    /// Replace the glance (active-only) session feed. Leaves `recentSessions` untouched.
    ///
    /// Ignored unless the core is `.connected` — see `updateGlanceActivity`.
    ///
    /// Guarded on the whole value, not on ids: the tray's Clients rows render a
    /// live per-session call count and last-activity age, so a session whose
    /// `toolCallCount` moved between polls must republish. An id-only guard
    /// froze both numbers at the first poll's values for as long as the session
    /// list's membership held. `MCPSession` is `Equatable` for exactly this.
    @MainActor
    func updateGlanceSessions(_ sessions: [APIClient.MCPSession]) {
        guard coreState == .connected else { return }
        if sessions != glanceSessions {
            glanceSessions = sessions
        }
    }

    /// Drop every glance feed. Called from `coreState.didSet` on any state other
    /// than `.connected` so a stopped or reconnecting core never shows the
    /// previous core's numbers as live.
    ///
    /// Deliberately NOT `@MainActor`: a property observer is a nonisolated
    /// context, so an isolated method could not be called from it without
    /// `await`. `AppState` itself is not `@MainActor` either, so a plain method
    /// is already nonisolated. Every real assignment to `coreState` happens on
    /// the main actor anyway — `transition(to:)`, plus the two `MainActor.run`
    /// blocks in CoreProcessManager.awaitExternalCore and MCPProxyApp.stopCore.
    func clearGlanceState() {
        if !glanceActivity.isEmpty { glanceActivity = [] }
        if !glanceSessions.isEmpty { glanceSessions = [] }
        if usageTimeline != nil { usageTimeline = nil }
        if callsThisHour != nil { callsThisHour = nil }
        if usageError != nil { usageError = nil }
        if !glanceFailureStreak.isEmpty { glanceFailureStreak = [:] }
        if glanceError != nil { glanceError = nil }
    }
}
