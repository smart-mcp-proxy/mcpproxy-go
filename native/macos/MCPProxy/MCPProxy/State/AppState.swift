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

// The former `HealthIndicator` enum and `AppState.healthLevel` lived here to
// feed `Menu/TrayIcon.swift` — a SwiftUI menu-bar label that was never
// instantiated, so both were dead from the day they were written (found by the
// 2026-08 tray UX audit, F1). The status item is AppKit and now badges server
// severity itself; see `TrayStatusIcon` in Menu/TrayPresentation.swift.

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
            if coreState != oldValue { connectionGeneration &+= 1 }
            if coreState != .connected { clearGlanceState() }
        }
    }

    /// Which connection the tray is on. Bumped on every real `coreState`
    /// change, so a value captured before a fetch identifies the core that
    /// fetch was issued to.
    ///
    /// `coreState == .connected` alone cannot tell a response from THIS
    /// connection from one issued to a core that has since died and been
    /// replaced: reconnection restores `.connected`, and the guard then admits
    /// the dead core's data. Comparing generations does tell them apart.
    /// Re-assigning an unchanged state is deliberately not a new generation —
    /// discarding a good in-flight response over a redundant publish would cost
    /// liveness for nothing.
    @Published private(set) var connectionGeneration: Int = 0

    /// Whether `generation` still identifies the live connection. The predicate
    /// every glance publish must satisfy: connected, and connected to the same
    /// core the fetch was issued to.
    func isCurrentConnection(_ generation: Int) -> Bool {
        coreState == .connected && generation == connectionGeneration
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

    /// A session the Activity Log should scope itself to as soon as it exists
    /// (F10 — a tray glance row's hand-off).
    ///
    /// A notification alone cannot carry this: a window created BY the click
    /// subscribes its `onReceive` observers only once the view appears, so a
    /// post made now lands on the floor — the same trap `showMainWindow(tab:)`
    /// documents for the sidebar tab. ActivityView consumes and clears this on
    /// appear; the notification still covers the already-open window, and
    /// whichever arrives first clears it for the other.
    @Published var pendingActivitySessionFilter: String?
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

    /// Calls recorded in the CURRENT UTC hour, as of the last usage poll. `nil`
    /// means "not loaded yet".
    ///
    /// This is the POLLED number. What the header renders is
    /// `glanceCallsThisHour(now:)`, which adds the calls that have arrived over
    /// SSE since — see there for why the raw value cannot be shown.
    @Published var callsThisHour: Int?

    /// The UTC hour `callsThisHour` is a total FOR.
    ///
    /// Without it the polled number outlives its hour: at 09:00:00, with the
    /// last poll up to 30 seconds old, the header went on reporting 08:00's
    /// total as "this hour" — and did so while the live half of the same sum
    /// was already being filtered by hour, so the two halves disagreed about
    /// which hour they were describing.
    private var callsThisHourBucket: Date?

    /// When each live SSE call that the usage timeline will eventually count
    /// arrived, for as long as the poll has not answered for it.
    ///
    /// Not `@Published`: every write here happens inside `prependGlanceActivity`
    /// or `updateUsage`, both of which publish something the menu already
    /// rebuilds on, so publishing this too would only double the churn.
    private var liveCallsSinceUsagePoll: [Date] = []

    /// The instant the most recent completed usage poll was ISSUED — the line
    /// between "the core has already counted this call" and "it has not".
    ///
    /// A poll is an `await` across the network, and SSE rows land on the same
    /// actor while it is in flight, so the two interleave. Clearing the live
    /// list wholesale on any completed poll therefore dropped calls the poll
    /// could not have seen (header falls from 13 back to 12 under a visible
    /// thirteenth row), and re-admitting calls it already counted double-counted
    /// them (header climbs to 14). Both are settled by one boundary: a live call
    /// counts only while its timestamp is strictly after the poll that is
    /// currently answering for the hour.
    private var usagePollBoundary: Date = .distantPast

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

    /// Consecutive failures of one feed before the block admits it is not
    /// updating. At the 30-second poll cadence this is a minute and a half of
    /// silence — long enough that a core restart passes unremarked, short
    /// enough that a dead core is not presented as live for long.
    static let glanceStaleFailureThreshold = 3

    /// Whether any feed has been failing long enough to say so on screen.
    ///
    /// Published (and only flipped, never rewritten) because it changes what
    /// the menu renders: every write here feeds the debounced
    /// `objectWillChange → rebuildMenu()` sink, and a core that has been gone
    /// for an hour must not rebuild the menu twice a minute forever.
    @Published private(set) var glanceStale: Bool = false

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

    /// Spec 092 FR-015 — the effective update policy the attached core reports
    /// (`/api/v1/info` → `update_policy`). Nil before a core is reached and for
    /// cores older than 092; `UpdatePolicyResolver` maps that to the permissive
    /// default rather than to "updates off". Published so a config hot-reload
    /// picked up on the next connect reaches `UpdateService` without the
    /// service having to poll anything.
    @Published var coreUpdatePolicy: CoreUpdatePolicy? = nil

    /// Spec 092 FR-002 — an older core is running that the tray is NOT allowed
    /// to stop on its own. Nil in the steady state; when set, the menu offers
    /// the restart as an explicit user action. Published rather than derived so
    /// the decision is made once, by `CoreSupersede`, and merely rendered here.
    @Published var staleCorePrompt: StaleCorePrompt? = nil

    /// Spec 092 FR-003 — the app bundle ON DISK is a newer version than the one
    /// running (a drag-install upgrade over a running app). Nil in the steady
    /// state; when set, the menu offers a relaunch.
    @Published var replacedBundleVersion: String? = nil

    /// Base URL for the Web UI, populated from /api/v1/info on connect.
    /// Falls back to localhost:8080 until the actual URL is fetched.
    @Published var webUIBaseURL: String = "http://127.0.0.1:8080"

    /// Whether the user has explicitly stopped MCPProxy (distinct from idle/error states).
    /// Session-only by design (GH #410): the persistent choice is `startCoreOnLaunch`,
    /// so stopping the core to debug something does not silently leave the tray
    /// dead after the next reboot.
    @Published var isStopped: Bool = false

    /// Whether the tray itself is terminating (app quit / logout / restart, set
    /// in `applicationWillTerminate` before the core is torn down). Distinct
    /// from `isStopped` (a user "Stop" that keeps the tray running): on the
    /// quit path the core is terminated while the tray is still `.connected`
    /// and `isStopped` is false, so the resulting clean exit needs this
    /// explicit intent to be recognised as expected rather than a crash. A
    /// clean (status 0) exit with NONE of the intent flags set is an EXTERNAL
    /// termination the tray did not ask for, and must still trigger recovery —
    /// which is why status 0 alone does not count as expected.
    @Published var isTerminating: Bool = false

    /// The PID of the managed core the tray is intentionally stopping to let the
    /// Sparkle updater swap the app bundle (Spec 092 FR-012 pre-install stop);
    /// nil when no such stop is pending. Stop INTENT like `isTerminating`, but
    /// SCOPED TO THE EXACT PROCESS rather than a global flag: only the exit of
    /// THIS pid is treated as expected, so a later liveness-recovered core (a
    /// new pid) can still crash and be surfaced/recovered even if a stale intent
    /// were left behind — the pid simply won't match. Cleared when the stop is
    /// postponed and the core stays live (see `stopManagedCore`).
    @Published var stoppedForUpdatePID: Int32?

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
    /// Servers in an intentional non-connected state are excluded, via the same
    /// `isBadgeExempt` predicate the badge uses — it was left on the narrower
    /// OAuth-only check when that predicate was introduced, which quietly broke
    /// the "these three filter identically" invariant at birth (verification
    /// sweep, gap 1). Nothing consumes this today, which is exactly why the
    /// drift would have gone unnoticed until something did.
    ///
    /// MCP-1819/T3, the original reason: the backend classifies OAuth
    /// login-required as an error-severity MCPX_UNKNOWN_UNCLASSIFIED diagnostic,
    /// which would otherwise read as a "file a bug" hard error. Such a server is
    /// surfaced calmly via `serversNeedingAttention` instead — as is a
    /// quarantined one, via "Review quarantine…".
    var serversWithDiagnostic: [ServerStatus] {
        servers.filter { $0.hasAttentionDiagnostic && !$0.isBadgeExempt }
    }

    /// How many servers carry the severity the tray icon is currently badging.
    ///
    /// Must agree with `worstDiagnosticSeverity` exactly — including its
    /// `isBadgeExempt` filter — or the status item says "4 server errors" over
    /// a badge that counted three: that property looks only at ENABLED servers,
    /// while `serversWithDiagnostic` includes the disabled ones and both
    /// severities.
    func diagnosticCount(severity: String) -> Int {
        servers.filter {
            $0.enabled && !$0.isBadgeExempt && $0.diagnostic?.severity == severity
        }.count
    }

    /// Highest-severity diagnostic across enabled servers. Returns nil when
    /// no diagnostics are attached. Used by TrayIcon to colour the badge.
    ///
    /// Servers in an INTENTIONAL non-connected state are skipped (see
    /// `ServerStatus.isBadgeExempt`) so neither a pending sign-in nor a
    /// quarantine review tints the tray icon badge red/orange — the calm
    /// "Needs Attention" path owns those states instead.
    var worstDiagnosticSeverity: String? {
        var sawWarn = false
        for srv in servers where srv.enabled && !srv.isBadgeExempt {
            guard let d = srv.diagnostic else { continue }
            if d.severity == "error" { return "error" }
            if d.severity == "warn" { sawWarn = true }
        }
        return sawWarn ? "warn" : nil
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

    /// Whether a live SSE row will show up in the usage timeline's call count
    /// once the poll catches up.
    ///
    /// Mirrors `UsageAggregate.Apply` (internal/runtime/usage_aggregate.go),
    /// whose timeline matches the glance population: executed `tool_call`s
    /// (sheds rejected by the concurrency limiter never ran and stay out,
    /// spec 093), mcpproxy's own internal calls except the `call_tool_*`
    /// variant echoes (those mirror a dispatch that already emitted its own
    /// `tool_call` record), and blocked/rejected policy decisions. Anything
    /// wider or narrower here makes the header disagree with the bars until
    /// the next poll corrects it.
    static func countsTowardUsageTimeline(_ entry: ActivityEntry) -> Bool {
        guard let name = entry.toolName, !name.isEmpty else { return false }
        switch entry.type {
        case "tool_call":
            return entry.status != "rejected"
        case "internal_tool_call":
            // Mirrors GlanceSelection: management built-ins never row (rule 1),
            // call_tool_* echoes never do (their dispatch emitted its own
            // tool_call record), and other internal calls row on success only
            // for the discovery set, on failure always (rule 3). A successful
            // code_execution is therefore out of both the glance and the bars —
            // its sub-calls are tool_call records that already count — while a
            // failed one is in.
            guard !name.hasPrefix("call_tool_"),
                  !GlanceSelection.managementBuiltIns.contains(name) else { return false }
            return entry.status != "success"
                || GlanceSelection.glanceInternalTools.contains(name)
        case ActivityEntry.policyDecisionType:
            return entry.status == "blocked" || entry.status == "rejected"
        default:
            return false
        }
    }

    /// Calls recorded on the 24-hour axis ending at `now` — the same window
    /// the histogram draws and `GlancePresence.lookback` uses, so every number
    /// in the glance describes ONE frame.
    ///
    /// Buckets are UTC-hour aligned and sparse; anything whose hour has slid
    /// off the axis is dropped, exactly as `ActivityHistogram.bars` drops it.
    static func callsInLast24Hours(_ timeline: [UsageBucket], now: Date = Date()) -> Int {
        let oldestHour = floorToHour(now).addingTimeInterval(-23 * 3600)
        return timeline.reduce(0) { total, bucket in
            floorToHour(bucket.start) >= oldestHour ? total + max(0, bucket.calls) : total
        }
    }

    /// The header's call count over the menu's 24h frame: the polled window
    /// total plus the calls that arrived over SSE since that poll — the same
    /// two-source reconciliation as `glanceCallsThisHour` (GH #934), on the
    /// window the rest of the glance describes. Still `nil` before the first
    /// usage response, so the header omits the segment rather than inventing
    /// a count.
    @MainActor
    func glanceCallsLast24h(now: Date = Date()) -> Int? {
        guard let usageTimeline else { return nil }
        let base = AppState.callsInLast24Hours(usageTimeline, now: now)
        // Live increments are all newer than the poll they follow; the age
        // filter only matters for a menu left open across a very long gap.
        let live = liveCallsSinceUsagePoll.filter { now.timeIntervalSince($0) < 24 * 3600 }.count
        return base + live
    }

    /// The header's call count: the polled hour total plus the calls that have
    /// arrived over SSE since that poll and fall in the same hour.
    ///
    /// The rows in the glance block come from SSE and are on screen within
    /// milliseconds; the count came from a poll up to 30 seconds old, so the
    /// header routinely sat one or more calls below the rows beneath it (GH
    /// #934). Both now move on the same event.
    ///
    /// Still `nil` before the first usage response. A live call must not invent
    /// "1 call this hour" for a proxy that has served hundreds — until the poll
    /// answers there is no total to add to, and the header omits the segment.
    ///
    /// Once the hour rolls over, the polled total is dropped rather than
    /// carried: it is a total for the hour that just ended, and reporting it as
    /// this hour's is the same kind of untruth the live increment exists to
    /// remove. What is left is the calls seen since — 0 on a quiet proxy, which
    /// is exactly right, and corrected within one poll if it is not.
    @MainActor
    func glanceCallsThisHour(now: Date = Date()) -> Int? {
        guard let callsThisHour else { return nil }
        let hour = AppState.floorToHour(now)
        let live = liveCallsSinceUsagePoll.filter { AppState.floorToHour($0) == hour }.count
        // Dropped only when the total is KNOWN to be for another hour. A total
        // with no recorded bucket has no provenance to distrust — production
        // always writes the two together in `updateUsage`.
        if let bucket = callsThisHourBucket, bucket != hour { return live }
        return callsThisHour + live
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
    ///
    /// `polledAt` is when the request that produced this timeline was ISSUED,
    /// not when its answer arrived — the two differ by a round trip during
    /// which SSE rows keep landing, and the difference is the whole reason the
    /// parameter exists. It defaults to `now` for the callers (tests, mostly)
    /// that have no separate notion of the two.
    @MainActor
    func updateUsage(timeline: [UsageBucket], now: Date = Date(), polledAt: Date? = nil) {
        guard coreState == .connected else { return }
        if usageTimeline != timeline { usageTimeline = timeline }
        let calls = AppState.callsInCurrentHour(timeline, now: now)
        if callsThisHour != calls { callsThisHour = calls }
        // Which hour that total is FOR. Not @Published: it only ever changes
        // alongside `callsThisHour`, which already drives the rebuild.
        callsThisHourBucket = AppState.floorToHour(now)
        // This poll answered for everything that had already happened when it
        // was issued, so those live increments are dropped rather than added to
        // it — that is what keeps the two from accumulating (GH #934). What it
        // could NOT have seen is kept: a call that arrived while the request was
        // in flight is still ours to count, and dropping it made the header fall
        // back below the row already on screen for up to 30 seconds.
        let boundary = polledAt ?? now
        usagePollBoundary = boundary
        liveCallsSinceUsagePoll.removeAll { $0 <= boundary }
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
        let generation = connectionGeneration
        // Stamped BEFORE the await. Everything the core had counted by now is
        // in the answer; anything that arrives over SSE while this request is in
        // flight is not, and the two are told apart by this instant alone.
        let issuedAt = Date()
        do {
            let usage = try await source.usageAggregate(window: "24h", top: 1)
            guard isCurrentConnection(generation) else { return }
            updateUsage(timeline: usage.timeline, polledAt: issuedAt)
        } catch {
            guard isCurrentConnection(generation) else { return }
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
        let generation = connectionGeneration
        do {
            let entries = try await source.glanceActivity(limit: AppState.glanceActivityPageSize)
            guard isCurrentConnection(generation) else { return }
            updateGlanceActivity(entries)
            clearGlanceFailure(.activity)
        } catch {
            guard isCurrentConnection(generation) else { return }
            recordGlanceFailure(.activity, AppState.usageFailureMessage(for: error))
        }
    }

    /// Fetch the retained session feed and publish the outcome — success or
    /// failure. See `refreshGlanceActivity(from:)`; a permanently failing fetch
    /// here rendered "No recent clients", the same thing a genuinely quiet
    /// proxy renders.
    @MainActor
    func refreshGlanceSessions(from source: GlanceDataSource) async {
        let generation = connectionGeneration
        do {
            let sessions = try await source.recentSessions(limit: AppState.glanceSessionsPageSize)
            guard isCurrentConnection(generation) else { return }
            updateGlanceSessions(sessions)
            clearGlanceFailure(.sessions)
        } catch {
            guard isCurrentConnection(generation) else { return }
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
        refreshStaleMarker()
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
        refreshStaleMarker()
    }

    /// Recompute `glanceStale` from the streaks.
    @MainActor
    private func refreshStaleMarker() {
        let stale = glanceFailureStreak.values.contains { $0 >= AppState.glanceStaleFailureThreshold }
        if glanceStale != stale { glanceStale = stale }
    }

    /// Records requested per glance activity poll — the server's maximum.
    ///
    /// Rules 1-3 run client-side, AFTER the page arrives, so the five rows are
    /// only ever as deep as one page: a burst of `upstream_servers` /
    /// `quarantine_security` calls at the head of the log pushes real calls off
    /// it and the menu says "No tool calls yet" while they sit below the fold.
    /// At 50 that took 46 management calls; a real agent walking a server list
    /// makes them in bursts.
    ///
    /// 100 is not a round number, it is the ceiling: `ActivityFilter.Validate`
    /// (internal/storage/activity_models.go) clamps `limit` to 100, so a larger
    /// request is silently served as 100.
    ///
    /// Deeper than that would need paging, and paging is the wrong trade here
    /// even though `offset` does work end-to-end on this endpoint.
    /// `Manager.ListActivities` walks and unmarshals the ENTIRE activity bucket
    /// on every call — it never breaks early, because it counts `total` — and
    /// that bucket holds up to `activity_max_records` (100,000) records. So the
    /// cost of a request is set by the bucket, not by `limit`: doubling the page
    /// costs 50 more struct copies, while a second page costs a whole extra
    /// 100k-record walk every 30 seconds. One deeper page buys most of the
    /// headroom for a fraction of the cost.
    ///
    /// The residual is real and deliberately not papered over. Stated exactly:
    /// five rows are guaranteed only when five distinct REQUEST GROUPS survive
    /// all four selection rules within the newest 100 records matching the type
    /// filter. Groups, not records, because rule 4 collapses every record
    /// sharing a `request_id` into one row — a failed call contributes a wrapper
    /// and an upstream record, so five qualifying records can be as few as three
    /// rows. `testRowsGoNoDeeperThanOnePage` pins the depth boundary and
    /// `testFiveQualifyingRecordsSharingRequestIDsProduceFewerRows` the group one.
    static let glanceActivityPageSize = 100

    /// Sessions requested per poll — the entire retained set, unfiltered by
    /// status (FR-016a).
    ///
    /// 100 is the retention cap (`enforceSessionRetention`), so this asks for
    /// everything there is rather than a page of it. It has to: the Clients
    /// section deduplicates per client and counts the whole deduped set, and one
    /// client reconnecting through a long working day can occupy dozens of
    /// session records — at 25 it could fill the response by itself and hide
    /// every other client behind it.
    static let glanceSessionsPageSize = 100

    /// The Clients segment of the glance summary line — "2 active · 1 idle" —
    /// or nil when no client is either (FR-019).
    ///
    /// Counted over the FULL deduped set rather than the five rows the section
    /// shows: the headline answers "what is using the proxy", and the row cap is
    /// a display limit, not a fact about the world. `seen` clients keep their
    /// rows and stay out of this count — see `GlancePresence.summaryText`.
    @MainActor
    func glanceClientSummary(now: Date = Date()) -> String? {
        GlancePresence.summaryText(
            for: GlancePresence.clients(from: glanceSessions, now: now, limit: Int.max)
        )
    }

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
        let merged = AppState.mergeGlanceActivity(polled: entries,
                                                  into: glanceActivity,
                                                  unconfirmed: unconfirmedLiveKeys)
        unconfirmedLiveKeys = merged.unconfirmed
        func fingerprint(_ list: [ActivityEntry]) -> [String] {
            list.map { "\($0.id)|\($0.status)|\($0.hasSensitiveData == true)" }
        }
        if fingerprint(merged.rows) != fingerprint(glanceActivity) {
            glanceActivity = merged.rows
        }
    }

    /// Record keys of SSE rows no poll has confirmed yet — the only rows the
    /// merge may keep against a page that omits them.
    ///
    /// Not `@Published`: it changes on every poll and nothing renders it.
    private(set) var unconfirmedLiveKeys: Set<String> = []

    /// Reconcile a polled page with the rows already on screen.
    ///
    /// MERGE, not replace, and the choice is load-bearing. `AppState` is
    /// reached through `await`, so `prependGlanceActivity` can insert an SSE row
    /// at index 0 while the GET that produced `polled` is still suspended. A
    /// wholesale replace then erases a row the response could not have known
    /// about, and the call is missing from the menu until the next poll — the
    /// user watches a call appear and vanish for up to 30 seconds.
    ///
    /// Keyed on `GlanceSelection.recordKey` (the `requestId`), which is already
    /// the identity function for rule 4's collapse and for the row diff: the
    /// storage id and the SSE row's provisional `<request_id>:<type>` differ for
    /// one and the same call, so id-keying would keep both.
    ///
    /// Only an UNCONFIRMED row can be retained — one this tray prepended from an
    /// SSE event that no poll has carried back yet. That is the whole population
    /// the race can strand, and confining retention to it is what keeps the
    /// merge from resurrecting the dead: a canonical row the server has dropped
    /// (retention, an explicit delete) is absent from the page and may well be
    /// newer than everything left in it, so an "absent and newer" rule alone
    /// would keep showing it — permanently, on a core idle enough that no later
    /// page ever reaches back past it, while the successful polls keep clearing
    /// the staleness marker that would otherwise hint something is wrong.
    ///
    /// An unconfirmed row is retained when it is BOTH absent from the page and
    /// newer than everything the page carries; a page that has reached past it
    /// has answered for it. Two pages carry no "newest record", and they mean
    /// opposite things:
    ///
    /// * An EMPTY page contradicts nothing. It says the server had recorded no
    ///   matching calls when it answered, which is silence about a call it had
    ///   not written yet — so unconfirmed rows stand.
    /// * A page WITH records but no parsable timestamps is still the server's
    ///   own account of the feed, and "newer than" is unanswerable against it,
    ///   so the page wins.
    ///
    /// Returns the merged rows and the keys still unconfirmed: anything the page
    /// carried is the server's record now, and a row that was dropped is nobody's.
    static func mergeGlanceActivity(
        polled: [ActivityEntry],
        into existing: [ActivityEntry],
        unconfirmed: Set<String>
    ) -> (rows: [ActivityEntry], unconfirmed: Set<String>) {
        // Deliberately max(), not `polled.first`: the merge does not depend on
        // the page arriving newest-first.
        let newestPolled = polled.compactMap { GlanceFormatting.parseTimestamp($0.timestamp) }.max()
        let polledKeys = Set(polled.map(GlanceSelection.recordKey))

        let retained: [ActivityEntry]
        if polled.isEmpty {
            retained = existing.filter { unconfirmed.contains(GlanceSelection.recordKey(for: $0)) }
        } else if let newestPolled {
            retained = existing.filter { entry in
                let key = GlanceSelection.recordKey(for: entry)
                guard unconfirmed.contains(key), !polledKeys.contains(key) else { return false }
                guard let stamp = GlanceFormatting.parseTimestamp(entry.timestamp) else { return false }
                return stamp > newestPolled
            }
        } else {
            retained = []
        }

        let rows = Array((retained + polled).prefix(glanceActivityCap))
        let survived = Set(rows.map(GlanceSelection.recordKey))
        return (rows, Set(retained.map(GlanceSelection.recordKey)).intersection(survived))
    }

    /// Upper bound on rows kept in `glanceActivity`. Deliberately equal to
    /// `glanceActivityPageSize`, so SSE rows and polled rows agree on depth.
    static let glanceActivityCap = glanceActivityPageSize

    /// Prepend one optimistic row adapted from an SSE payload (newest first).
    /// Bounded so a busy agent cannot grow the feed without limit; the 30s
    /// reconciling poll replaces the list wholesale with canonical records.
    ///
    /// Guarded on the connection GENERATION, not merely on `.connected`, for the
    /// same reason the three fetches are: SSE teardown is asynchronous, the
    /// publish happens a MainActor hop after the event is read, and executors
    /// promise no ordering between that hop and the reconnect work. An event
    /// from the previous core can therefore resume after `.connected` has been
    /// restored — `.connected` alone would wave it through and prepend a dead
    /// core's call to the new core's feed. The caller captures `generation`
    /// where the stream is opened, since a stream belongs to one connection.
    ///
    /// Deliberately without the equality guard `updateGlanceSessions(_:)`
    /// carries — a prepend is by definition a change, and that guard exists only
    /// to stop redundant `@Published` churn on identical poll results. (That
    /// guard compares whole `MCPSession` values, not ids: the Clients rows
    /// render a live per-session call count, which an id-only comparison froze.)
    @MainActor
    func prependGlanceActivity(_ entry: ActivityEntry, generation: Int) {
        guard isCurrentConnection(generation) else { return }
        // Before the row it belongs to, so the two are published together: the
        // header must never be a number the rows underneath it contradict.
        //
        // Only calls the last poll cannot already have counted. An SSE event for
        // a call that happened before that poll was issued is a LATE row, not a
        // new call — the aggregate has it, and adding it again pushed the header
        // one above the rows until the next poll corrected it.
        if AppState.countsTowardUsageTimeline(entry),
           let stamp = GlanceFormatting.parseTimestamp(entry.timestamp),
           stamp > usagePollBoundary {
            liveCallsSinceUsagePoll.append(stamp)
        }
        // Unconfirmed until a poll carries it back: this is the only row the
        // merge is allowed to keep against a page that omits it.
        unconfirmedLiveKeys.insert(GlanceSelection.recordKey(for: entry))
        glanceActivity.insert(entry, at: 0)
        if glanceActivity.count > AppState.glanceActivityCap {
            glanceActivity.removeLast(glanceActivity.count - AppState.glanceActivityCap)
            // The cap and the key set must not be able to disagree: a key whose
            // row has just been capped away describes nothing, and the poll is
            // the only other thing that prunes the set — so while polls fail and
            // SSE keeps arriving, which is exactly when the staleness marker is
            // showing, the set would otherwise grow for as long as the core is up.
            unconfirmedLiveKeys.formIntersection(glanceActivity.map(GlanceSelection.recordKey))
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
        callsThisHourBucket = nil
        if !liveCallsSinceUsagePoll.isEmpty { liveCallsSinceUsagePoll = [] }
        usagePollBoundary = .distantPast
        if usageError != nil { usageError = nil }
        if !unconfirmedLiveKeys.isEmpty { unconfirmedLiveKeys = [] }
        if !glanceFailureStreak.isEmpty { glanceFailureStreak = [:] }
        if glanceError != nil { glanceError = nil }
        if glanceStale { glanceStale = false }
    }
}
