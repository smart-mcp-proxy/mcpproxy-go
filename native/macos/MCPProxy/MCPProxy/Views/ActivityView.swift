// ActivityView.swift
// MCPProxy
//
// Shows the activity log with summary stats, filter dropdowns for Type/Server/Status,
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
    @State private var filterType = "all"
    @State private var filterServer = "all"
    @State private var filterStatus = "all"
    @State private var filterText = ""

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

    private let statusOptions: [(label: String, value: String)] = [
        ("All Statuses", "all"),
        ("Success", "success"),
        ("Error", "error"),
        ("Blocked", "blocked"),
        ("Description Changed", "tool_description_changed"),
    ]

    /// Unique server names from activity list + appState servers.
    private var serverOptions: [(label: String, value: String)] {
        var names = Set<String>()
        for entry in activities {
            if let name = entry.serverName, !name.isEmpty { names.insert(name) }
        }
        for server in appState.servers { names.insert(server.name) }
        var options: [(label: String, value: String)] = [("All Servers", "all")]
        for name in names.sorted() { options.append((name, name)) }
        return options
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

    /// Build query string from current filter state.
    private var filterQueryString: String {
        var parts: [String] = ["limit=100"]
        if filterType != "all" { parts.append("type=\(filterType)") }
        if filterServer != "all" { parts.append("server=\(filterServer)") }
        if filterStatus != "all" { parts.append("status=\(filterStatus)") }
        return parts.joined(separator: "&")
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

                if isLoading && activities.isEmpty {
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
                                ForEach(filteredActivities) { entry in
                                    ActivityTableRow(
                                        entry: entry,
                                        currentDate: context.date,
                                        isSelected: entry.id == selectedActivityID,
                                        colTime: colTime,
                                        colType: colType,
                                        colServer: colServer,
                                        colIntent: colIntent,
                                        colStatus: colStatus,
                                        colDuration: colDuration,
                                        fontScale: fontScale
                                    )
                                    .contentShape(Rectangle())
                                    .onTapGesture {
                                        selectedActivityID = entry.id
                                    }

                                    Divider().padding(.leading, 8)
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
            if let selectedID = selectedActivityID,
               let selected = activities.first(where: { $0.id == selectedID }) {
                Divider()
                ActivityDetailView(
                    entry: selected,
                    recentSessions: appState.recentSessions,
                    onDismiss: { selectedActivityID = nil }
                )
                .frame(minWidth: 300, idealWidth: 380, maxWidth: 520, maxHeight: .infinity)
                .layoutPriority(1)
            }
        }
        .task {
            await loadSummary()
            await loadActivities()
        }
        // SSE live update: reload when activityVersion is bumped
        .onChange(of: appState.activityVersion) { _ in
            Task {
                await loadSummary()
                await loadActivities()
            }
        }
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

            // Export menu
            Menu {
                Button("Export JSON...") { exportActivity(format: "json") }
                Button("Export CSV...") { exportActivity(format: "csv") }
            } label: {
                Image(systemName: "square.and.arrow.up")
            }
            .menuStyle(.borderlessButton)
            .frame(width: 28)
            .help("Export activity log")
            .accessibilityIdentifier("activity-export-button")

            Button {
                Task {
                    await loadSummary()
                    await loadActivities()
                }
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
            HStack(spacing: 12) {
                Picker("Type", selection: $filterType) {
                    ForEach(typeOptions, id: \.value) { option in
                        Text(option.label).tag(option.value)
                    }
                }
                .frame(maxWidth: 180)
                .accessibilityIdentifier("activity-filter-type")
                .onChange(of: filterType) { _ in
                    Task { await loadActivities() }
                }

                Picker("Server", selection: $filterServer) {
                    ForEach(serverOptions, id: \.value) { option in
                        Text(option.label).tag(option.value)
                    }
                }
                .frame(maxWidth: 180)
                .accessibilityIdentifier("activity-filter-server")
                .onChange(of: filterServer) { _ in
                    Task { await loadActivities() }
                }

                Picker("Status", selection: $filterStatus) {
                    ForEach(statusOptions, id: \.value) { option in
                        Text(option.label).tag(option.value)
                    }
                }
                .frame(maxWidth: 180)
                .accessibilityIdentifier("activity-filter-status")
                .onChange(of: filterStatus) { _ in
                    Task { await loadActivities() }
                }
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
        .padding(.horizontal)
        .padding(.bottom, 8)
    }

    // MARK: - Empty State

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

    private func loadActivities() async {
        isLoading = true
        defer { isLoading = false }
        guard let client = apiClient else {
            activities = appState.recentActivity
            return
        }

        do {
            let data = try await client.fetchRaw(path: "/api/v1/activity?\(filterQueryString)")
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
                    // Build export query with current filters
                    var exportQuery = "format=\(format)"
                    if filterType != "all" { exportQuery += "&type=\(filterType)" }
                    if filterServer != "all" { exportQuery += "&server=\(filterServer)" }
                    if filterStatus != "all" { exportQuery += "&status=\(filterStatus)" }
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
    @Environment(\.fontScale) var fontScale
    @State private var copiedField: String?

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

                // Intent Declaration
                if entry.intent != nil {
                    Divider()
                    intentSection
                }

                // Request Arguments
                if let args = entry.arguments, !args.isEmpty {
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
