// ActivityView.swift
// MCPProxy
//
// Shows the activity log with summary stats, view segments (Tool calls · Sessions ·
// System events · All), URL-contract filters (ScopeFilter, Spec 109-k),
// a tabular list on the left with column headers, and a detail panel on the right.
// Features: SSE live updates, dynamic timestamps, colored JSON, intent display, export.

import SwiftUI
import UniformTypeIdentifiers

// MARK: - Activity View

struct ActivityView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale
    @State private var activities: [ActivityEntry] = []
    @State private var selectedActivityID: String?
    @State private var isLoading = false
    @State private var totalCount: Int = 0
    @State private var isExporting = false

    // Summary stats
    @State private var summary: ActivitySummary?
    @State private var isSummaryLoading = false

    // Filter state
    /// Every URL-contract parameter (Spec 109-k): view segment, server, tool,
    /// session, status, time range, type, caller, and the Spec 108 scope
    /// filters. Its REST mapping is `ScopeFilter.restRequest(for: .activity)`,
    /// shared with the Web UI and the CLI.
    @State private var scope = ScopeFilter()
    @State private var filterText = ""
    /// When set, the list shows only the sub-calls of this code_execution
    /// (records whose parent_id equals it). Set by "View sub-calls" in the
    /// detail panel; cleared by the filter chip or "View parent call".
    @State private var filterParentId: String?
    /// Monotonic ticket for loadActivities: only the NEWEST in-flight load may
    /// publish its results, so a slow unfiltered fetch can never overwrite the
    /// sub-call view the user just asked for (or vice versa).
    @State private var loadGeneration = 0

    /// Sessions view rows (`GET /api/v1/sessions`).
    @State private var sessions: [APIClient.MCPSession] = []
    /// Folded System-events runs the user expanded in place (FR-071).
    @State private var expandedRuns: Set<String> = []
    /// The one pending reload. Several triggers can land in one main-actor
    /// turn (a hand-off assigns `scope`, which also fires `onChange`); each
    /// cancels the previous before it has issued its request, so they
    /// coalesce into a single fetch.
    @State private var reloadTask: Task<Void, Never>?

    private var apiClient: APIClient? { appState.apiClient }

    // Available filter options
    private let typeOptions: [(label: String, value: String)] = [
        ("All Types", "all"),
        ("Tool Call", "tool_call"),
        ("Internal Tool Call", "internal_tool_call"),
        ("Quarantine Change", "tool_quarantine_change"),
        ("System Start", "system_start"),
        ("System Stop", "system_stop"),
        ("Config Change", "config_change"),
        ("Policy Decision", "policy_decision"),
        ("Server Change", "server_change"),
    ]

    /// Call outcomes the REST validator accepts (url-filter-contract.md
    /// `status`); the Web-only "other" bucket has no native equivalent.
    private let statusOptions: [(label: String, value: String)] = [
        ("All Statuses", "all"),
        ("Success", "success"),
        ("Error", "error"),
        ("Blocked", "blocked"),
        ("Rejected", "rejected"),
    ]

    private let timeOptions: [(label: String, value: String)] = [
        ("Any Time", "all"),
        ("Last Hour", "-1h"),
        ("Last 24 Hours", "-24h"),
        ("Last 7 Days", "-7d"),
        ("Last 30 Days", "-30d"),
    ]

    private let callerOptions: [(label: String, value: String)] = [
        ("Any Caller", "all"),
        ("Admin", "admin"),
        ("Agent Token", "agent"),
    ]

    /// Unique server names from activity list + appState servers.
    private var serverOptions: [(label: String, value: String)] {
        var names = Set<String>()
        for entry in activities {
            if let name = entry.serverName, !name.isEmpty { names.insert(name) }
        }
        for server in appState.servers { names.insert(server.name) }
        if let current = scope.server, !current.isEmpty { names.insert(current) }
        var options: [(label: String, value: String)] = [("All Servers", "all")]
        for name in names.sorted() { options.append((name, name)) }
        return options
    }

    /// Canonical `server:tool` names seen in the list (plus the active one).
    private var toolOptions: [(label: String, value: String)] {
        var names = Set<String>()
        for entry in activities {
            if let server = entry.serverName, !server.isEmpty,
               let tool = entry.toolName, !tool.isEmpty {
                names.insert("\(server):\(tool)")
            }
        }
        if let current = scope.tool, !current.isEmpty { names.insert(current) }
        var options: [(label: String, value: String)] = [("All Tools", "all")]
        for name in names.sorted() { options.append((name, name)) }
        return options
    }

    /// Time presets, plus the active value when a link set a custom range.
    private var effectiveTimeOptions: [(label: String, value: String)] {
        guard let from = scope.from, !from.isEmpty,
              !timeOptions.contains(where: { $0.value == from }) else { return timeOptions }
        return timeOptions + [("Since \(from)", from)]
    }

    /// Binding that maps the pickers' "all" sentinel to a nil filter value.
    private func optionBinding(_ keyPath: WritableKeyPath<ScopeFilter, String?>) -> Binding<String> {
        Binding(
            get: { scope[keyPath: keyPath].flatMap { $0.isEmpty ? nil : $0 } ?? "all" },
            set: { scope[keyPath: keyPath] = ($0 == "all") ? nil : $0 }
        )
    }

    /// Activities filtered by text search (client-side on top of API filters).
    private var filteredActivities: [ActivityEntry] {
        guard !filterText.isEmpty else { return activities }
        let query = filterText.lowercased()
        return activities.filter { entry in
            (entry.serverName?.lowercased().contains(query) ?? false) ||
            (entry.toolName?.lowercased().contains(query) ?? false) ||
            entry.type.lowercased().contains(query) ||
            entry.status.lowercased().contains(query) ||
            (entry.intentReason?.lowercased().contains(query) ?? false)
        }
    }

    /// Rows as displayed: System events fold (FR-071); every other view is
    /// one row per record.
    private var displayRows: [ActivityFoldRow] {
        ActivityFolding.fold(filteredActivities, enabled: scope.view == .system)
    }

    /// The Activity request for the current filters, or nil when they
    /// contradict each other (rule 8) and nothing may be fetched.
    private var activityRequest: ScopeRequest? {
        scope.restRequest(for: .activity, scopeFiltersAvailable: appState.scopeFiltersAvailable)
    }

    /// The filters currently in force, as encoded `key=value` pairs.
    ///
    /// Shared with the export so a filtered view and its export can never
    /// disagree — "Export with current filters" used to drop the sub-call and
    /// session scopes and hand back the whole log. Values are escaped: a
    /// server name with a space or `&` is otherwise a broken (or extra) query
    /// parameter.
    private var activeFilterParams: [String] {
        var parts: [String] = []
        if let request = activityRequest, !request.queryString.isEmpty {
            parts.append(request.queryString)
        }
        if let parentId = filterParentId, !parentId.isEmpty {
            parts.append("parent_id=\(APIClient.escapeQueryValue(parentId))")
        }
        return parts
    }

    /// Apply a hand-off from an in-app link (tray glance row, Clients row …).
    private func apply(_ filter: ScopeFilter) {
        scope = filter
        filterParentId = nil
        selectedActivityID = nil
        scheduleReload()
    }

    /// Reload whatever the current view shows, coalescing triggers that land
    /// in the same turn into one request (see `reloadTask`).
    private func scheduleReload() {
        reloadTask?.cancel()
        reloadTask = Task {
            await Task.yield()
            guard !Task.isCancelled else { return }
            await reload()
        }
    }

    // MARK: - Parent/child navigation (code_execution sub-calls)

    /// Filter the list down to the children of one code_execution call.
    /// The parent's own selection is dropped: it is not in the filtered list,
    /// and keeping it would silently reopen its detail panel the moment the
    /// chip is cleared and the parent scrolls back in.
    private func showSubCalls(of parentRequestId: String) {
        filterParentId = parentRequestId
        selectedActivityID = nil
        Task { await loadActivities() }
    }

    /// Jump from a child back to its parent code_execution record: drop the
    /// sub-call filter, reload the normal list, and select the parent. When
    /// the parent has already scrolled past the 100-row page, fetch it by
    /// request_id and pin it to the top so the jump never dead-ends.
    private func showParent(requestId: String) async {
        filterParentId = nil
        await loadActivities()
        if let parent = activities.first(where: { $0.requestId == requestId }) {
            selectedActivityID = parent.id
            return
        }
        guard let client = apiClient else { return }
        if let data = try? await client.fetchRaw(path: "/api/v1/activity?request_id=\(requestId)&limit=1"),
           let wrapper = try? JSONDecoder().decode(APIResponse<ActivityListResponse>.self, from: data),
           let parent = wrapper.data?.activities.first {
            activities.insert(parent, at: 0)
            selectedActivityID = parent.id
        }
    }

    // MARK: - Column widths
    // Kept tight so that with the detail panel open the list can still coexist
    // with the outer NavigationSplitView sidebar at the default 800pt window width.
    private let colTime: CGFloat = 56
    private let colType: CGFloat = 92
    private let colServer: CGFloat = 96
    private let colDetails: CGFloat = 0  // flexible (minWidth enforced in layout)
    private let colIntent: CGFloat = 52
    private let colStatus: CGFloat = 64
    private let colDuration: CGFloat = 56

    var body: some View {
        HStack(spacing: 0) {
            // Left: activity table with filters
            VStack(alignment: .leading, spacing: 0) {
                activityListHeader
                summaryStatsBar
                filterBar
                Divider()

                // Rule 8 only where server and tool apply: in Sessions both
                // are "not applicable here" chips, and /sessions was fetched.
                if scope.view != .sessions, let conflict = scope.conflictMessage {
                    conflictState(conflict)
                } else if scope.view == .sessions {
                    sessionsList
                } else if isLoading && activities.isEmpty {
                    ProgressView("Loading...")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if filteredActivities.isEmpty {
                    emptyState
                } else {
                    // Column headers
                    tableHeader

                    Divider()

                    // TimelineView re-renders every 20s to update relative timestamps
                    TimelineView(.periodic(from: .now, by: 20)) { context in
                        ScrollView {
                            LazyVStack(spacing: 0) {
                                ForEach(displayRows) { row in
                                    if row.count > 1 {
                                        foldedRow(row, currentDate: context.date)
                                    } else {
                                        entryRow(row.lead, currentDate: context.date)
                                    }
                                }
                            }
                        }
                        .accessibilityIdentifier("activity-list")
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)

            // Right: detail panel (only rendered when an entry is selected)
            // NOTE: avoid HSplitView here — it fights the outer NavigationSplitView's
            // sidebar column for width and can cause the app sidebar to collapse when
            // the detail panel appears. A plain HStack with a fixed-width detail column
            // keeps the sidebar stable.
            if scope.view != .sessions,
               let selectedID = selectedActivityID,
               let selected = activities.first(where: { $0.id == selectedID }) {
                Divider()
                ActivityDetailView(
                    entry: selected,
                    recentSessions: appState.recentSessions,
                    onDismiss: { selectedActivityID = nil },
                    onShowSubCalls: { parentRequestId in
                        showSubCalls(of: parentRequestId)
                    },
                    onShowParent: { parentRequestId in
                        Task { await showParent(requestId: parentRequestId) }
                    }
                )
                .frame(minWidth: 300, idealWidth: 380, maxWidth: 520, maxHeight: .infinity)
                .layoutPriority(1)
            }
        }
        .task {
            // An in-app link that opened this window left its filter here;
            // consume it before the first load so the list is never briefly
            // unfiltered.
            if let pending = appState.consumeScopeFilter() {
                scope = pending
            }
            scheduleReload()
            await loadSummary()
        }
        // One reload per filter change, whichever control made it.
        .onChange(of: scope) { _ in
            selectedActivityID = nil
            scheduleReload()
        }
        // The scope filters appear (and are sent) once the core lists them.
        .onChange(of: appState.scopeFiltersAvailable) { _ in
            scheduleReload()
        }
        // SSE live update: reload when activityVersion is bumped
        .onChange(of: appState.activityVersion) { _ in
            scheduleReload()
            Task { await loadSummary() }
        }
        // F10 / Spec 109-k: an in-app link hands over its filter.
        .onReceive(NotificationCenter.default.publisher(for: .activityFilter)) { note in
            guard let filter = note.object as? ScopeFilter else { return }
            // This view is live, so the hand-off is settled here — clear the
            // pending value so a later-appearing view does not re-apply it.
            _ = appState.consumeScopeFilter()
            apply(filter)
        }
    }

    // MARK: - Rows

    @ViewBuilder
    private func entryRow(_ entry: ActivityEntry, currentDate: Date, indent: Bool = false) -> some View {
        ActivityTableRow(
            entry: entry,
            currentDate: currentDate,
            isSelected: entry.id == selectedActivityID,
            colTime: colTime,
            colType: colType,
            colServer: colServer,
            colIntent: colIntent,
            colStatus: colStatus,
            colDuration: colDuration,
            fontScale: fontScale
        )
        .padding(.leading, indent ? 16 : 0)
        .contentShape(Rectangle())
        .onTapGesture {
            selectedActivityID = entry.id
        }

        Divider().padding(.leading, 8)
    }

    /// A folded System-events run: one summary line that expands in place.
    @ViewBuilder
    private func foldedRow(_ row: ActivityFoldRow, currentDate: Date) -> some View {
        let expanded = expandedRuns.contains(row.id)
        Button {
            if expanded { expandedRuns.remove(row.id) } else { expandedRuns.insert(row.id) }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: expanded ? "chevron.down" : "chevron.right")
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.secondary)
                Text(row.summary ?? "")
                    .font(.scaled(.caption, scale: fontScale))
                    .lineLimit(1)
                Spacer()
                Text("×\(row.count)")
                    .font(.scaled(.caption2, scale: fontScale).monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("activity-folded-row")
        .accessibilityLabel("\(row.summary ?? ""), \(expanded ? "collapse" : "expand")")

        Divider().padding(.leading, 8)

        if expanded {
            ForEach(row.members) { member in
                entryRow(member, currentDate: currentDate, indent: true)
            }
        }
    }

    // MARK: - Sessions view

    @ViewBuilder
    private var sessionsList: some View {
        if isLoading && sessions.isEmpty {
            ProgressView("Loading...")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if sessions.isEmpty {
            VStack(spacing: 12) {
                Image(systemName: "person.2")
                    .font(.system(size: 48 * fontScale))
                    .foregroundStyle(.tertiary)
                Text("No sessions recorded")
                    .font(.scaled(.title3, scale: fontScale))
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(sessions) { session in
                        sessionRow(session)
                        Divider().padding(.leading, 8)
                    }
                }
            }
            .accessibilityIdentifier("activity-sessions-list")
        }
    }

    private func sessionRow(_ session: APIClient.MCPSession) -> some View {
        Button {
            // Link map: the session's calls, by work session id when it has one.
            scope = ScopeFilter.forSessionRow(session)
        } label: {
            HStack(spacing: 8) {
                Circle()
                    .fill(session.status == "active" ? Color.green : Color.secondary.opacity(0.5))
                    .frame(width: 8, height: 8)
                VStack(alignment: .leading, spacing: 2) {
                    Text(session.clientName ?? "Unknown client")
                        .font(.scaled(.callout, scale: fontScale))
                    Text(Self.shortCorrelationId(session.workSessionId ?? session.id))
                        .font(.scaled(.caption2, scale: fontScale).monospaced())
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let calls = session.toolCallCount {
                    Text("\(calls) calls")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
                Image(systemName: "chevron.right")
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .background(scope.highlights(session) ? Color.accentColor.opacity(0.15) : Color.clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Show this session's tool calls")
        .accessibilityIdentifier("activity-session-row")
    }

    // MARK: - Table Header

    @ViewBuilder
    private var tableHeader: some View {
        HStack(spacing: 0) {
            Text("Time")
                .frame(width: colTime, alignment: .leading)
            Text("Type")
                .frame(width: colType, alignment: .leading)
            Text("Server")
                .frame(width: colServer, alignment: .leading)
            Text("Details")
                .lineLimit(1)
                .frame(minWidth: 60, maxWidth: .infinity, alignment: .leading)
            Text("Intent")
                .frame(width: colIntent, alignment: .center)
            Text("Status")
                .frame(width: colStatus, alignment: .center)
            Text("Duration")
                .frame(width: colDuration, alignment: .trailing)
        }
        .font(.scaled(.caption, scale: fontScale).weight(.semibold))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color(nsColor: .controlBackgroundColor))
    }

    // MARK: - Header

    @ViewBuilder
    private var activityListHeader: some View {
        HStack {
            Text("Activity Log")
                .font(.scaled(.title2, scale: fontScale).bold())
            Spacer()
            if isLoading || isExporting {
                ProgressView()
                    .controlSize(.small)
            }

            // Export menu (always unfolded; not offered for Sessions or a
            // contradictory filter, which have no activity query to export)
            Menu {
                Button("Export JSON...") { exportActivity(format: "json") }
                Button("Export CSV...") { exportActivity(format: "csv") }
            } label: {
                Image(systemName: "square.and.arrow.up")
            }
            .menuStyle(.borderlessButton)
            .frame(width: 28)
            .disabled(scope.view == .sessions || activityRequest == nil)
            .help("Export activity log")
            .accessibilityIdentifier("activity-export-button")

            Button {
                scheduleReload()
                Task { await loadSummary() }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.borderless)
            .help("Refresh activity log")
        }
        .padding(.horizontal)
        .padding(.top)
        .padding(.bottom, 8)
    }

    // MARK: - Summary Stats Bar

    @ViewBuilder
    private var summaryStatsBar: some View {
        HStack(spacing: 16) {
            if let s = summary {
                SummaryStatPill(label: "Total 24h", value: "\(s.totalCount)", color: .blue, fontScale: fontScale)
                SummaryStatPill(label: "Success", value: "\(s.successCount)", color: .green, fontScale: fontScale)
                SummaryStatPill(label: "Errors", value: "\(s.errorCount)", color: .red, fontScale: fontScale)
                SummaryStatPill(label: "Blocked", value: "\(s.blockedCount)", color: .orange, fontScale: fontScale)
            } else if isSummaryLoading {
                ProgressView()
                    .controlSize(.small)
                Text("Loading summary...")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            } else {
                Text("Summary unavailable")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
            Spacer()
        }
        .padding(.horizontal)
        .padding(.bottom, 8)
    }

    // MARK: - Filter Bar

    @ViewBuilder
    private var filterBar: some View {
        VStack(spacing: 6) {
            // View segments (FR-070): Tool calls · Sessions · System events · All
            Picker("View", selection: $scope.view) {
                ForEach(ActivityViewMode.allCases) { mode in
                    Text(mode.label).tag(mode)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .accessibilityIdentifier("activity-view-segments")

            if scope.view != .sessions {
                HStack(spacing: 12) {
                    Picker("Type", selection: optionBinding(\.type)) {
                        ForEach(typeOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 180)
                    .accessibilityIdentifier("activity-filter-type")

                    Picker("Server", selection: optionBinding(\.server)) {
                        ForEach(serverOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 180)
                    .accessibilityIdentifier("activity-filter-server")

                    Picker("Tool", selection: optionBinding(\.tool)) {
                        ForEach(toolOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 200)
                    .accessibilityIdentifier("activity-filter-tool")
                }

                HStack(spacing: 12) {
                    Picker("Status", selection: optionBinding(\.status)) {
                        ForEach(statusOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 180)
                    .accessibilityIdentifier("activity-filter-status")

                    Picker("Time", selection: optionBinding(\.from)) {
                        ForEach(effectiveTimeOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 180)
                    .accessibilityIdentifier("activity-filter-time")

                    Picker("Caller", selection: optionBinding(\.authType)) {
                        ForEach(callerOptions, id: \.value) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .frame(maxWidth: 180)
                    .accessibilityIdentifier("activity-filter-auth-type")
                }

                // Text search
                HStack {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(.secondary)
                    TextField("Search by server, tool, or type...", text: $filterText)
                        .textFieldStyle(.plain)
                    if !filterText.isEmpty {
                        Button {
                            filterText = ""
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.borderless)
                    }
                }
            }

            // Sub-call filter chip: the list is narrowed to one code_execution's
            // children until the chip is dismissed.
            if let parentId = filterParentId, scope.view != .sessions {
                filterChip(
                    icon: "arrow.turn.down.right",
                    text: "Sub-calls of \(Self.shortCorrelationId(parentId))",
                    clearLabel: "Clear sub-call filter",
                    identifier: "activity-parent-filter",
                    onClear: {
                        filterParentId = nil
                        Task { await loadActivities() }
                    }
                )
            }

            // Session filter chip (F10): says which client's calls are on
            // screen, and how to get back to everything. In the Sessions view
            // it only highlights that row.
            if let sessionId = scope.session, !sessionId.isEmpty {
                filterChip(
                    icon: "person.crop.circle",
                    text: "Session \(Self.shortCorrelationId(sessionId))",
                    clearLabel: "Clear session filter",
                    identifier: "activity-session-filter",
                    onClear: { scope.session = nil }
                )
            }

            // Spec 108 scope filters: only once the core advertises them.
            ForEach(scope.visibleScopeParams(scopeFiltersAvailable: appState.scopeFiltersAvailable), id: \.self) { name in
                filterChip(
                    icon: "line.3.horizontal.decrease.circle",
                    text: "\(name): \(scopeValue(name))",
                    clearLabel: "Clear \(name) filter",
                    identifier: "activity-\(name)-filter",
                    onClear: { clearScopeParam(name) }
                )
            }

            // Rule 5: set but not applicable in this view — shown, never
            // silently dropped, and never implying it filtered anything.
            let inapplicable = scope.inapplicableParams(for: .activity)
            if !inapplicable.isEmpty {
                HStack(spacing: 6) {
                    Image(systemName: "slash.circle")
                        .font(.scaled(.caption, scale: fontScale))
                    Text("Not applicable here: \(inapplicable.joined(separator: ", "))")
                        .font(.scaled(.caption, scale: fontScale))
                        .lineLimit(1)
                    Spacer()
                }
                .foregroundStyle(.secondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .accessibilityIdentifier("activity-inapplicable-chips")
            }
        }
        .padding(.horizontal)
        .padding(.bottom, 8)
    }

    private func scopeValue(_ name: String) -> String {
        switch name {
        case "profile": return scope.profile ?? ""
        case "client": return scope.client ?? ""
        case "token": return scope.token ?? ""
        default: return ""
        }
    }

    private func clearScopeParam(_ name: String) {
        switch name {
        case "profile": scope.profile = nil
        case "client": scope.client = nil
        case "token": scope.token = nil
        default: break
        }
    }

    private func filterChip(icon: String, text: String, clearLabel: String,
                            identifier: String, onClear: @escaping () -> Void) -> some View {
        HStack(spacing: 6) {
            Image(systemName: icon)
                .font(.scaled(.caption, scale: fontScale))
            Text(text)
                .font(.scaled(.caption, scale: fontScale))
                .lineLimit(1)
                .truncationMode(.middle)
            Button(action: onClear) {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.borderless)
            .help("Show all activity")
            .accessibilityLabel(clearLabel)
            .accessibilityIdentifier(identifier.replacingOccurrences(of: "activity-", with: "activity-clear-"))
            Spacer()
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(Color.accentColor.opacity(0.12))
        .clipShape(Capsule())
        .accessibilityIdentifier("\(identifier)-chip")
    }

    /// Correlation ids are long ("<nanos>-code_execution-17"); the chip shows
    /// the tail, which is the part a human can actually tell apart.
    static func shortCorrelationId(_ id: String) -> String {
        id.count <= 24 ? id : "…" + id.suffix(24)
    }

    // MARK: - Empty State

    /// Rule 8: contradictory filters — no request was made.
    private func conflictState(_ message: String) -> some View {
        VStack(spacing: 12) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 48 * fontScale))
                .foregroundStyle(.orange)
            Text(message)
                .font(.scaled(.title3, scale: fontScale))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            HStack {
                Button("Clear server") { scope.server = nil }
                Button("Clear tool") { scope.tool = nil }
            }
        }
        .padding()
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityIdentifier("activity-conflict-state")
    }

    @ViewBuilder
    private var emptyState: some View {
        if appState.coreState != .connected {
            VStack(spacing: 12) {
                Image(systemName: appState.isStopped ? "stop.circle.fill" : "clock.arrow.circlepath")
                    .font(.system(size: 48 * fontScale))
                    .foregroundStyle(.tertiary)
                Text(appState.isStopped ? "MCPProxy Core is Stopped" : "MCPProxy Core is Not Running")
                    .font(.scaled(.title3, scale: fontScale))
                    .foregroundStyle(.secondary)
                Text("Start the core to see activity")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            VStack(spacing: 12) {
                Image(systemName: "clock.arrow.circlepath")
                    .font(.system(size: 48 * fontScale))
                    .foregroundStyle(.tertiary)
                Text("No activity recorded")
                    .font(.scaled(.title3, scale: fontScale))
                    .foregroundStyle(.secondary)
                Text("Tool calls and server events will appear here")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    // MARK: - Data Loading

    private func loadSummary() async {
        guard let client = apiClient else { return }
        isSummaryLoading = true
        defer { isSummaryLoading = false }
        do {
            summary = try await client.activitySummary()
        } catch {
            // Non-fatal; summary just won't display
        }
    }

    /// Load whatever the current view shows.
    private func reload() async {
        if scope.view == .sessions {
            await loadSessions()
        } else {
            await loadActivities()
        }
    }

    private func loadSessions() async {
        loadGeneration += 1
        let ticket = loadGeneration
        guard let client = apiClient,
              let request = activityRequest else {
            isLoading = false  // see loadActivities: this ticket superseded any in-flight load
            return
        }
        isLoading = true
        defer { if ticket == loadGeneration { isLoading = false } }
        let query = (["limit=100"] + (request.queryString.isEmpty ? [] : [request.queryString]))
            .joined(separator: "&")
        do {
            let data = try await client.fetchRaw(path: "\(request.path)?\(query)")
            guard ticket == loadGeneration else { return }
            let decoder = JSONDecoder()
            if let wrapper = try? decoder.decode(APIResponse<APIClient.SessionsResponse>.self, from: data),
               let payload = wrapper.data {
                sessions = payload.sessions
            } else if let direct = try? decoder.decode(APIClient.SessionsResponse.self, from: data) {
                sessions = direct.sessions
            }
        } catch {
            guard ticket == loadGeneration else { return }
            sessions = appState.recentSessions
        }
    }

    private func loadActivities() async {
        loadGeneration += 1
        let ticket = loadGeneration
        // Rule 8: contradictory filters issue no request at all.
        guard activityRequest != nil else {
            activities = []
            totalCount = 0
            // This ticket superseded any in-flight load, whose guarded defer
            // will no longer clear the spinner — so this path must.
            isLoading = false
            return
        }
        isLoading = true
        defer { if ticket == loadGeneration { isLoading = false } }
        guard let client = apiClient else {
            activities = appState.recentActivity
            return
        }

        do {
            let query = (["limit=100"] + activeFilterParams).joined(separator: "&")
            let data = try await client.fetchRaw(path: "/api/v1/activity?\(query)")
            // A newer load was issued while this one was in flight (the user
            // toggled the sub-call chip, changed a filter …) — its answer is
            // the one the current filter state describes, not this one.
            guard ticket == loadGeneration else { return }
            let decoder = JSONDecoder()
            if let wrapper = try? decoder.decode(APIResponse<ActivityListResponse>.self, from: data),
               let payload = wrapper.data {
                activities = payload.activities
                totalCount = payload.total
            } else if let direct = try? decoder.decode(ActivityListResponse.self, from: data) {
                activities = direct.activities
                totalCount = direct.total
            }
        } catch {
            guard ticket == loadGeneration else { return }
            activities = appState.recentActivity
        }
    }

    // MARK: - Export

    private func exportActivity(format: String) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "activity-export.\(format)"
        if format == "csv" {
            panel.allowedContentTypes = [UTType.commaSeparatedText]
        } else {
            panel.allowedContentTypes = [UTType.json]
        }
        panel.canCreateDirectories = true

        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task {
                isExporting = true
                defer { isExporting = false }
                guard let client = apiClient else { return }
                do {
                    // Build export query with current filters — every one of
                    // them, from the same source the list uses.
                    let exportQuery = (["format=\(format)"] + activeFilterParams).joined(separator: "&")
                    let data = try await client.fetchRaw(path: "/api/v1/activity/export?\(exportQuery)")
                    try data.write(to: url)
                    NSWorkspace.shared.activateFileViewerSelecting([url])
                } catch {
                    NSLog("[MCPProxy] Export failed: %@", error.localizedDescription)
                }
            }
        }
    }
}

// MARK: - Activity Table Row

struct ActivityTableRow: View {
    let entry: ActivityEntry
    let currentDate: Date
    let isSelected: Bool
    let colTime: CGFloat
    let colType: CGFloat
    let colServer: CGFloat
    let colIntent: CGFloat
    let colStatus: CGFloat
    let colDuration: CGFloat
    var fontScale: CGFloat = 1.0

    var body: some View {
        HStack(spacing: 0) {
            // Time column
            Text(relativeTime(entry.timestamp))
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(width: colTime, alignment: .leading)

            // Type column (icon + label)
            HStack(spacing: 4) {
                Image(systemName: typeIcon)
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(typeIconColor)
                    .frame(width: 14)
                Text(displayType)
                    .font(.scaled(.caption, scale: fontScale))
                    .lineLimit(1)
            }
            .frame(width: colType, alignment: .leading)

            // Server column
            Text(entry.serverName ?? "-")
                .font(.scaled(.caption, scale: fontScale))
                .lineLimit(1)
                .frame(width: colServer, alignment: .leading)

            // Details column (tool name)
            HStack(spacing: 4) {
                // A sub-call made inside a code_execution script: marked so the
                // list reads as parent-with-children, not five unrelated calls.
                if let parentId = entry.parentId, !parentId.isEmpty {
                    Image(systemName: "arrow.turn.down.right")
                        .foregroundStyle(.secondary)
                        .font(.scaled(.caption2, scale: fontScale))
                        .help("Sub-call of a code_execution")
                        .accessibilityLabel("Sub-call of a code execution")
                }
                Text(entry.toolName ?? "-")
                    .font(.scaled(.caption, scale: fontScale))
                    .lineLimit(1)
                    .truncationMode(.middle)

                // Sensitive data indicator
                if entry.hasSensitiveData == true {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.scaled(.caption2, scale: fontScale))
                        .help("Contains sensitive data")
                        .accessibilityLabel("Contains sensitive data")
                }
            }
            .frame(minWidth: 60, maxWidth: .infinity, alignment: .leading)

            // Intent column
            if let op = entry.intentOperationType {
                IntentBadge(operationType: op, fontScale: fontScale)
                    .frame(width: colIntent, alignment: .center)
            } else {
                Text("-")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .frame(width: colIntent, alignment: .center)
            }

            // Status column
            ActivityStatusBadge(status: entry.status, fontScale: fontScale)
                .frame(width: colStatus, alignment: .center)

            // Duration column
            if let duration = entry.durationMs {
                Text("\(duration)ms")
                    .font(.scaledMonospacedDigit(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .frame(width: colDuration, alignment: .trailing)
            } else {
                Text("-")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .frame(width: colDuration, alignment: .trailing)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
        .background(isSelected ? Color.accentColor.opacity(0.15) : Color.clear)
    }

    // MARK: - Helpers

    private var typeIcon: String {
        switch entry.type {
        case "tool_call": return "wrench.fill"
        case "internal_tool_call": return "gearshape.fill"
        case "tool_quarantine_change": return "shield.fill"
        case "system_start": return "play.circle.fill"
        case "system_stop": return "stop.circle.fill"
        case "config_change": return "slider.horizontal.3"
        case "policy_decision": return "hand.raised.fill"
        case "server_change": return "arrow.triangle.2.circlepath"
        default: return "circle.fill"
        }
    }

    private var typeIconColor: Color {
        switch entry.type {
        case "tool_call": return .blue
        case "internal_tool_call": return .indigo
        case "tool_quarantine_change": return .orange
        case "system_start": return .green
        case "system_stop": return .red
        case "config_change": return .purple
        case "policy_decision": return .orange
        case "server_change": return .teal
        default: return .gray
        }
    }

    private var displayType: String {
        switch entry.type {
        case "tool_call": return "Tool Call"
        case "internal_tool_call": return "Internal Tool"
        case "tool_quarantine_change": return "Quarantine"
        case "system_start": return "System Start"
        case "system_stop": return "System Stop"
        case "config_change": return "Config Change"
        case "policy_decision": return "Policy"
        case "server_change": return "Server Change"
        default: return entry.type
        }
    }

    private func relativeTime(_ timestamp: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = formatter.date(from: timestamp)
        if date == nil {
            formatter.formatOptions = [.withInternetDateTime]
            date = formatter.date(from: timestamp)
        }
        guard let d = date else { return timestamp }

        let elapsed = currentDate.timeIntervalSince(d)
        if elapsed < 60 { return "just now" }
        if elapsed < 3600 { return "\(Int(elapsed / 60))m ago" }
        if elapsed < 86400 { return "\(Int(elapsed / 3600))h ago" }
        return "\(Int(elapsed / 86400))d ago"
    }
}

// MARK: - Activity Status Badge

struct ActivityStatusBadge: View {
    let status: String
    var fontScale: CGFloat = 1.0

    var body: some View {
        Text(displayLabel)
            .font(.scaled(.caption2, scale: fontScale).weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(badgeColor.opacity(0.15))
            .foregroundStyle(badgeColor)
            .clipShape(Capsule())
            .accessibilityLabel("Status: \(displayLabel)")
    }

    private var displayLabel: String {
        switch status {
        case "success": return "Success"
        case "error": return "Error"
        case "blocked": return "Blocked"
        case "tool_description_changed": return "Changed"
        default: return status
        }
    }

    private var badgeColor: Color {
        switch status {
        case "success": return .green
        case "error": return .red
        case "blocked": return .orange
        case "tool_description_changed": return .yellow
        default: return .gray
        }
    }
}

// MARK: - Summary Stat Pill

struct SummaryStatPill: View {
    let label: String
    let value: String
    let color: Color
    var fontScale: CGFloat = 1.0

    var body: some View {
        HStack(spacing: 4) {
            Text(value)
                .font(.scaled(.subheadline, scale: fontScale).bold().monospacedDigit())
                .foregroundStyle(color)
            Text(label)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .background(.quaternary)
        .cornerRadius(8)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(label): \(value)")
    }
}

// MARK: - Intent Badge

struct IntentBadge: View {
    let operationType: String
    var fontScale: CGFloat = 1.0

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: iconName)
                .font(.system(size: 8 * fontScale))
            Text(operationType)
                .font(.scaled(.caption2, scale: fontScale).weight(.semibold))
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(backgroundColor.opacity(0.15))
        .foregroundStyle(backgroundColor)
        .clipShape(Capsule())
        .accessibilityLabel("Intent: \(operationType)")
    }

    private var iconName: String {
        switch operationType {
        case "read": return "book.fill"
        case "write": return "pencil"
        case "destructive": return "exclamationmark.triangle.fill"
        default: return "questionmark"
        }
    }

    private var backgroundColor: Color {
        switch operationType {
        case "read": return .green
        case "write": return .blue
        case "destructive": return .red
        default: return .gray
        }
    }
}

// MARK: - Activity Detail View

struct ActivityDetailView: View {
    let entry: ActivityEntry
    var recentSessions: [APIClient.MCPSession] = []
    var onDismiss: (() -> Void)? = nil
    /// Invoked with the entry's request_id when the user asks to see the
    /// sub-calls of this code_execution record.
    var onShowSubCalls: ((String) -> Void)? = nil
    /// Invoked with the entry's parent_id when the user asks to jump from a
    /// sub-call back to its parent code_execution record.
    var onShowParent: ((String) -> Void)? = nil
    @Environment(\.fontScale) var fontScale
    @State private var copiedField: String?

    /// The record is the PARENT of sandbox sub-calls: the built-in
    /// code_execution call whose request_id its children carry as parent_id.
    private var isCodeExecutionParent: Bool {
        entry.type == "internal_tool_call"
            && entry.toolName == "code_execution"
            && !(entry.requestId ?? "").isEmpty
    }

    /// The `code` argument of a code_execution record, when present — pulled
    /// out of the JSON so it can render as source instead of an escaped string.
    private var codeArgument: String? {
        guard case .string(let code)? = entry.arguments?["code"], !code.isEmpty else { return nil }
        return code
    }

    /// Request arguments minus the extracted `code` field.
    private var argumentsWithoutCode: [String: JSONValue]? {
        guard var args = entry.arguments, codeArgument != nil else { return entry.arguments }
        args.removeValue(forKey: "code")
        return args.isEmpty ? nil : args
    }

    /// Resolve a human-readable client name from the session ID.
    private var clientName: String? {
        guard let sessionId = entry.sessionId, !sessionId.isEmpty else { return nil }
        if let session = recentSessions.first(where: { $0.id == sessionId }) {
            if let name = session.clientName, !name.isEmpty {
                if let version = session.clientVersion, !version.isEmpty {
                    return "\(name) \(version)"
                }
                return name
            }
        }
        // Fallback: infer from session ID prefix heuristics
        let lower = sessionId.lowercased()
        if lower.contains("claude") { return "Claude Code" }
        if lower.contains("cursor") { return "Cursor" }
        if lower.contains("vscode") || lower.contains("copilot") { return "VS Code" }
        if lower.contains("codex") { return "Codex CLI" }
        if lower.contains("gemini") { return "Gemini" }
        if lower.contains("windsurf") { return "Windsurf" }
        return nil
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // Sensitive data warning banner
                if entry.hasSensitiveData == true {
                    sensitiveDataBanner
                }

                // Header
                detailHeader

                Divider()

                // Metadata grid
                metadataGrid

                // Parent/child navigation for code_execution records
                if isCodeExecutionParent || !(entry.parentId ?? "").isEmpty {
                    parentChildNavigation
                }

                // Intent Declaration
                if entry.intent != nil {
                    Divider()
                    intentSection
                }

                // Executed code, rendered as source (code_execution records)
                if let code = codeArgument {
                    Divider()
                    codeSection(code: code)
                }

                // Request Arguments (minus the code, which has its own section)
                if let args = argumentsWithoutCode, !args.isEmpty {
                    Divider()
                    jsonSection(
                        label: "Request Arguments",
                        value: .object(args),
                        field: "arguments"
                    )
                }

                // Response Body
                if let response = entry.response, !response.isEmpty {
                    Divider()
                    responseSection(response: response)
                }

                // Additional Details (metadata minus intent)
                if let additional = entry.additionalMetadata, !additional.isEmpty {
                    Divider()
                    jsonSection(
                        label: "Additional Details",
                        value: .object(additional),
                        field: "metadata"
                    )
                }

                // Error message
                if let errorMessage = entry.errorMessage, !errorMessage.isEmpty {
                    Divider()
                    errorSection(message: errorMessage)
                }
            }
            .padding()
        }
    }

    // MARK: - Sensitive Data Banner

    @ViewBuilder
    private var sensitiveDataBanner: some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.scaled(.title3, scale: fontScale))
                .foregroundStyle(.red)
            VStack(alignment: .leading, spacing: 2) {
                Text("Sensitive Data Detected")
                    .font(.scaled(.headline, scale: fontScale))
                    .foregroundStyle(.primary)
                if let severity = entry.maxSeverity {
                    Text("Max severity: \(severity)")
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
                if let types = entry.detectionTypes, !types.isEmpty {
                    Text(types.joined(separator: ", "))
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
        .padding(16)
        .background(Color.red.opacity(0.15))
        .cornerRadius(8)
        .accessibilityLabel("Warning: Sensitive data detected")
    }

    // MARK: - Header

    @ViewBuilder
    private var detailHeader: some View {
        HStack {
            Image(systemName: detailStatusIcon)
                .foregroundStyle(detailStatusColor)
                .font(.scaled(.title2, scale: fontScale))
            VStack(alignment: .leading, spacing: 2) {
                Text(detailTitle)
                    .font(.scaled(.title3, scale: fontScale).bold())
                HStack(spacing: 8) {
                    Text("Status: \(entry.status)")
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.secondary)
                    if let op = entry.intentOperationType {
                        IntentBadge(operationType: op, fontScale: fontScale)
                    }
                }
            }
            Spacer()
            if let onDismiss = onDismiss {
                Button {
                    onDismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
                .help("Close detail panel")
                .accessibilityLabel("Close detail panel")
                .accessibilityIdentifier("activity-detail-close")
            }
        }
    }

    // MARK: - Metadata Grid

    @ViewBuilder
    private var metadataGrid: some View {
        LazyVGrid(columns: [
            GridItem(.fixed(120), alignment: .trailing),
            GridItem(.flexible(), alignment: .leading)
        ], alignment: .leading, spacing: 8) {
            metadataRow(label: "ID", value: entry.id)
            metadataRow(label: "Type", value: entry.type)
            metadataRow(label: "Timestamp", value: entry.timestamp)

            if let server = entry.serverName, !server.isEmpty {
                metadataRow(label: "Server", value: server)
            }
            if let tool = entry.toolName, !tool.isEmpty {
                metadataRow(label: "Tool", value: tool)
            }
            if let source = entry.source, !source.isEmpty {
                metadataRow(label: "Source", value: source)
            }
            if let duration = entry.durationMs {
                metadataRow(label: "Duration", value: "\(duration) ms")
            }
            if let requestId = entry.requestId, !requestId.isEmpty {
                metadataRow(label: "Request ID", value: requestId)
            }
            if let sessionId = entry.sessionId, !sessionId.isEmpty {
                metadataRow(label: "Session ID", value: sessionId)
            }
            if let client = clientName {
                metadataRow(label: "Client", value: client)
            }
        }
    }

    // MARK: - Intent Section

    @ViewBuilder
    private var intentSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Intent Declaration")
                .font(.scaled(.headline, scale: fontScale))

            LazyVGrid(columns: [
                GridItem(.fixed(120), alignment: .trailing),
                GridItem(.flexible(), alignment: .leading)
            ], alignment: .leading, spacing: 6) {
                if let op = entry.intentOperationType {
                    Text("Operation")
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.secondary)
                    IntentBadge(operationType: op, fontScale: fontScale)
                }
                if let sensitivity = entry.intentSensitivity {
                    metadataRow(label: "Sensitivity", value: sensitivity)
                }
                if let reason = entry.intentReason {
                    Text("Reason")
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text(reason)
                        .font(.scaled(.subheadline, scale: fontScale))
                        .textSelection(.enabled)
                        .foregroundStyle(.primary)
                }
            }
        }
    }

    // MARK: - Parent/child navigation

    @ViewBuilder
    private var parentChildNavigation: some View {
        VStack(alignment: .leading, spacing: 6) {
            if isCodeExecutionParent, let requestId = entry.requestId, let onShowSubCalls {
                Button {
                    onShowSubCalls(requestId)
                } label: {
                    Label("View sub-calls", systemImage: "arrow.turn.down.right")
                        .font(.scaled(.subheadline, scale: fontScale))
                }
                .help("Show only the tool calls this code_execution made")
                .accessibilityIdentifier("activity-view-subcalls")
            }
            if let parentId = entry.parentId, !parentId.isEmpty {
                HStack(spacing: 6) {
                    Text("Sub-call of code_execution")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                    if let onShowParent {
                        Button {
                            onShowParent(parentId)
                        } label: {
                            Label("View parent call", systemImage: "arrow.uturn.up")
                                .font(.scaled(.subheadline, scale: fontScale))
                        }
                        .help("Jump to the code_execution that made this call")
                        .accessibilityIdentifier("activity-view-parent")
                    }
                }
            }
        }
    }

    // MARK: - Code Section (code_execution source)

    @ViewBuilder
    private func codeSection(code: String) -> some View {
        let language = languageBadge
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Code")
                    .font(.scaled(.headline, scale: fontScale))
                Text(language)
                    .font(.scaled(.caption2, scale: fontScale).bold())
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(Color.orange.opacity(0.15))
                    .foregroundStyle(.orange)
                    .clipShape(Capsule())
                Spacer()
                copyButton(text: code, field: "code")
            }

            Text(Self.formatScriptForDisplay(code))
                .font(.scaledMonospaced(.caption, scale: fontScale))
                .textSelection(.enabled)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(.controlBackgroundColor))
                .cornerRadius(8)
                .accessibilityIdentifier("activity-code-block")
        }
    }

    private var languageBadge: String {
        if case .string(let lang)? = entry.arguments?["language"], !lang.isEmpty {
            return lang.uppercased()
        }
        return "JAVASCRIPT"
    }

    /// Break a one-line script into readable lines for display: agents send
    /// `code` as a single line, and rendering it verbatim is an unreadable
    /// wall. Statement ends (`;`) and block braces get a newline — but only
    /// OUTSIDE string literals, so a semicolon inside 'a; b' never splits.
    /// Scripts that already contain newlines are shown verbatim: their author
    /// formatted them.
    ///
    /// DISPLAY-ONLY, deliberately conservative: regex literals and template
    /// substitutions with nested backticks are not parsed (that needs a real
    /// tokenizer), so a `/a;b/` regex may gain a line break on screen. The
    /// Copy button always yields the verbatim original, never this rendering.
    static func formatScriptForDisplay(_ code: String) -> String {
        guard !code.contains("\n") else { return code }
        var out = ""
        var quote: Character?
        var escaped = false
        var depth = 0
        var parenDepth = 0
        var skipLeadingSpace = false
        for ch in code {
            if let q = quote {
                out.append(ch)
                if escaped {
                    escaped = false
                } else if ch == "\\" {
                    escaped = true
                } else if ch == q {
                    quote = nil
                }
                continue
            }
            if skipLeadingSpace {
                if ch == " " { continue }
                skipLeadingSpace = false
            }
            switch ch {
            case "'", "\"", "`":
                quote = ch
                out.append(ch)
            case ";" where parenDepth == 0:
                out.append(ch)
                out.append("\n")
                out.append(String(repeating: "  ", count: depth))
                skipLeadingSpace = true
            case "(":
                parenDepth += 1
                out.append(ch)
            case ")":
                parenDepth = max(0, parenDepth - 1)
                out.append(ch)
            case "{":
                depth += 1
                out.append(ch)
            case "}":
                depth = max(0, depth - 1)
                out.append(ch)
            default:
                out.append(ch)
            }
        }
        return out
    }

    // MARK: - JSON Section (colored)

    @ViewBuilder
    private func jsonSection(label: String, value: JSONValue, field: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(label)
                    .font(.scaled(.headline, scale: fontScale))
                Text("JSON")
                    .font(.scaled(.caption2, scale: fontScale).bold())
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(Color.blue.opacity(0.15))
                    .foregroundStyle(.blue)
                    .clipShape(Capsule())
                Text("\(value.byteCount) bytes")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
                copyButton(text: value.prettyString, field: field)
            }

            coloredJSON(value)
                .font(.scaledMonospaced(.caption, scale: fontScale))
                .textSelection(.enabled)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(.controlBackgroundColor))
                .cornerRadius(8)
        }
    }

    // MARK: - Response Section

    @ViewBuilder
    private func responseSection(response: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Response Body")
                    .font(.scaled(.headline, scale: fontScale))

                if entry.parsedResponse != nil {
                    Text("JSON")
                        .font(.scaled(.caption2, scale: fontScale).bold())
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.blue)
                        .foregroundColor(.white)
                        .cornerRadius(4)
                }
                Text("\(response.utf8.count) bytes")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                if entry.responseTruncated == true {
                    Text("truncated")
                        .font(.scaled(.caption2, scale: fontScale))
                        .padding(.horizontal, 4)
                        .padding(.vertical, 1)
                        .background(Color.orange.opacity(0.2))
                        .foregroundStyle(.orange)
                        .cornerRadius(3)
                }
                Spacer()
                copyButton(text: response, field: "response")
            }

            if let parsed = entry.parsedResponse {
                coloredJSON(parsed)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(.controlBackgroundColor))
                    .cornerRadius(8)
            } else {
                Text(response)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(.controlBackgroundColor))
                    .cornerRadius(8)
            }
        }
    }

    // MARK: - Error Section

    @ViewBuilder
    private func errorSection(message: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Error")
                    .font(.scaled(.headline, scale: fontScale))
                    .foregroundStyle(.red)
                Spacer()
                copyButton(text: message, field: "error")
            }
            Text(message)
                .font(.scaledMonospaced(.body, scale: fontScale))
                .foregroundStyle(.red.opacity(0.8))
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.red.opacity(0.05))
                .cornerRadius(6)
        }
    }

    // MARK: - Colored JSON Rendering

    /// Render a JSONValue as a colored SwiftUI Text using concatenation.
    private func coloredJSON(_ value: JSONValue, indent: Int = 0) -> Text {
        switch value {
        case .string(let s):
            return Text("\"\(s)\"").foregroundColor(.teal)

        case .number(let n):
            let formatted = n.truncatingRemainder(dividingBy: 1) == 0 && abs(n) < 1e15
                ? "\(Int64(n))" : "\(n)"
            return Text(formatted).foregroundColor(.orange)

        case .bool(let b):
            return Text(b ? "true" : "false").foregroundColor(.purple)

        case .null:
            return Text("null").foregroundColor(.gray)

        case .array(let arr):
            if arr.isEmpty { return Text("[]") }
            var result = Text("[\n")
            for (i, element) in arr.enumerated() {
                result = result + Text(indentStr(indent + 1))
                    + coloredJSON(element, indent: indent + 1)
                if i < arr.count - 1 { result = result + Text(",") }
                result = result + Text("\n")
            }
            return result + Text(indentStr(indent)) + Text("]")

        case .object(let dict):
            if dict.isEmpty { return Text("{}") }
            let sorted = dict.sorted { $0.key < $1.key }
            var result = Text("{\n")
            for (i, (key, val)) in sorted.enumerated() {
                result = result + Text(indentStr(indent + 1))
                    + Text("\"\(key)\"").foregroundColor(.blue)
                    + Text(": ")
                    + coloredJSON(val, indent: indent + 1)
                if i < sorted.count - 1 { result = result + Text(",") }
                result = result + Text("\n")
            }
            return result + Text(indentStr(indent)) + Text("}")
        }
    }

    private func indentStr(_ level: Int) -> String {
        String(repeating: "  ", count: level)
    }

    // MARK: - Helpers

    @ViewBuilder
    private func copyButton(text: String, field: String) -> some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            copiedField = field
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                if copiedField == field { copiedField = nil }
            }
        } label: {
            HStack(spacing: 3) {
                Image(systemName: copiedField == field ? "checkmark" : "doc.on.doc")
                if copiedField == field {
                    Text("Copied")
                        .font(.caption2)
                }
            }
        }
        .buttonStyle(.borderless)
        .help("Copy to clipboard")
    }

    @ViewBuilder
    private func metadataRow(label: String, value: String) -> some View {
        Text(label)
            .font(.scaled(.subheadline, scale: fontScale))
            .foregroundStyle(.secondary)
        Text(value)
            .font(.scaledMonospaced(.subheadline, scale: fontScale))
            .textSelection(.enabled)
    }

    private var detailTitle: String {
        var parts: [String] = []
        if let server = entry.serverName, !server.isEmpty { parts.append(server) }
        if let tool = entry.toolName, !tool.isEmpty { parts.append(tool) }
        return parts.isEmpty ? entry.type : parts.joined(separator: ":")
    }

    private var detailStatusIcon: String {
        switch entry.status {
        case "error": return "xmark.circle.fill"
        case "blocked": return "hand.raised.fill"
        case "success": return "checkmark.circle.fill"
        case "tool_description_changed": return "pencil.circle.fill"
        default: return "circle.fill"
        }
    }

    private var detailStatusColor: Color {
        switch entry.status {
        case "error": return .red
        case "blocked": return .orange
        case "success": return .green
        case "tool_description_changed": return .yellow
        default: return .gray
        }
    }
}
