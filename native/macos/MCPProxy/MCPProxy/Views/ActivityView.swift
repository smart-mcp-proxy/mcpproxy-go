// ActivityView.swift
// MCPProxy
//
// Shows the activity log with summary stats, filter dropdowns for Type/Server/Status,
// a list on the left, and a detail panel on the right.
// Reads apiClient from appState instead of taking it as a parameter.

import SwiftUI

// MARK: - Activity View

struct ActivityView: View {
    @ObservedObject var appState: AppState
    @State private var activities: [ActivityEntry] = []
    @State private var selectedActivityID: String?
    @State private var isLoading = false
    @State private var totalCount: Int = 0

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

    /// Unique server names extracted from the current activity list,
    /// plus the servers from appState for a more complete list.
    private var serverOptions: [(label: String, value: String)] {
        var names = Set<String>()
        for entry in activities {
            if let name = entry.serverName, !name.isEmpty {
                names.insert(name)
            }
        }
        for server in appState.servers {
            names.insert(server.name)
        }
        var options: [(label: String, value: String)] = [("All Servers", "all")]
        for name in names.sorted() {
            options.append((name, name))
        }
        return options
    }

    /// Activities filtered by the text search (applied client-side on top of API filters).
    private var filteredActivities: [ActivityEntry] {
        guard !filterText.isEmpty else { return activities }
        let query = filterText.lowercased()
        return activities.filter { entry in
            (entry.serverName?.lowercased().contains(query) ?? false) ||
            (entry.toolName?.lowercased().contains(query) ?? false) ||
            entry.type.lowercased().contains(query) ||
            entry.status.lowercased().contains(query)
        }
    }

    var body: some View {
        HSplitView {
            // Left: activity list with filters
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
                    List(filteredActivities, selection: $selectedActivityID) { entry in
                        ActivityRow(entry: entry)
                            .tag(entry.id)
                    }
                    .accessibilityIdentifier("activity-list")
                }
            }
            .frame(minWidth: 400)

            // Right: detail panel
            if let selectedID = selectedActivityID,
               let selected = activities.first(where: { $0.id == selectedID }) {
                ActivityDetailView(entry: selected)
            } else {
                Text("Select an activity entry")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task {
            await loadSummary()
            await loadActivities()
        }
    }

    // MARK: - Header

    @ViewBuilder
    private var activityListHeader: some View {
        HStack {
            Text("Activity Log")
                .font(.title2.bold())
            Spacer()
            if isLoading {
                ProgressView()
                    .controlSize(.small)
            }
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
                SummaryStatPill(label: "Total 24h", value: "\(s.totalCount)", color: .blue)
                SummaryStatPill(label: "Success", value: "\(s.successCount)", color: .green)
                SummaryStatPill(label: "Errors", value: "\(s.errorCount)", color: .red)
                SummaryStatPill(label: "Blocked", value: "\(s.blockedCount)", color: .orange)
            } else if isSummaryLoading {
                ProgressView()
                    .controlSize(.small)
                Text("Loading summary...")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text("Summary unavailable")
                    .font(.caption)
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
        VStack(spacing: 12) {
            Image(systemName: "clock.arrow.circlepath")
                .font(.system(size: 48))
                .foregroundStyle(.tertiary)
            Text("No activity recorded")
                .font(.title3)
                .foregroundStyle(.secondary)
            Text("Tool calls and server events will appear here")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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
            // Fall back to appState's cached activity
            activities = appState.recentActivity
            return
        }

        // Build query string with active filters
        var queryParts: [String] = ["limit=100"]
        if filterType != "all" {
            queryParts.append("type=\(filterType)")
        }
        if filterServer != "all" {
            queryParts.append("server=\(filterServer)")
        }
        if filterStatus != "all" {
            queryParts.append("status=\(filterStatus)")
        }
        let queryString = queryParts.joined(separator: "&")

        do {
            let data = try await client.fetchRaw(path: "/api/v1/activity?\(queryString)")
            let decoder = JSONDecoder()
            // Try APIResponse wrapper
            if let wrapper = try? decoder.decode(APIResponse<ActivityListResponse>.self, from: data),
               let payload = wrapper.data {
                activities = payload.activities
                totalCount = payload.total
            } else if let direct = try? decoder.decode(ActivityListResponse.self, from: data) {
                activities = direct.activities
                totalCount = direct.total
            }
        } catch {
            // Fall back to cached data on error
            activities = appState.recentActivity
        }
    }
}

// MARK: - Summary Stat Pill

struct SummaryStatPill: View {
    let label: String
    let value: String
    let color: Color

    var body: some View {
        HStack(spacing: 4) {
            Text(value)
                .font(.subheadline.bold().monospacedDigit())
                .foregroundStyle(color)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .background(.quaternary)
        .cornerRadius(6)
    }
}

// MARK: - Activity Row

struct ActivityRow: View {
    let entry: ActivityEntry

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: statusIcon)
                .foregroundStyle(statusColor)
                .frame(width: 16)

            VStack(alignment: .leading, spacing: 2) {
                // Primary line: server:tool or type
                Text(summaryText)
                    .font(.system(.body, design: .default))
                    .lineLimit(1)

                // Secondary line: type and timestamp
                HStack(spacing: 6) {
                    Text(displayType)
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    if let duration = entry.durationMs {
                        Text("\(duration)ms")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }

                    Spacer()

                    Text(relativeTime(entry.timestamp))
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }

            // Sensitive data indicator
            if entry.hasSensitiveData == true {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                    .font(.caption)
                    .help("Contains sensitive data detections")
            }
        }
        .padding(.vertical, 2)
    }

    // MARK: - Helpers

    private var summaryText: String {
        var parts: [String] = []
        if let server = entry.serverName, !server.isEmpty {
            parts.append(server)
        }
        if let tool = entry.toolName, !tool.isEmpty {
            parts.append(tool)
        }
        if parts.isEmpty {
            return displayType
        }
        return parts.joined(separator: ":")
    }

    /// Human-friendly display name for the activity type.
    private var displayType: String {
        switch entry.type {
        case "tool_call": return "Tool Call"
        case "tool_quarantine_change": return "Quarantine Change"
        case "system_start": return "System Start"
        case "system_stop": return "System Stop"
        case "config_change": return "Config Change"
        case "policy_decision": return "Policy Decision"
        case "server_change": return "Server Change"
        default: return entry.type
        }
    }

    private var statusIcon: String {
        if entry.hasSensitiveData == true {
            return "exclamationmark.triangle.fill"
        }
        switch entry.status {
        case "error": return "xmark.circle.fill"
        case "blocked": return "hand.raised.fill"
        case "success": return "checkmark.circle.fill"
        case "tool_description_changed": return "pencil.circle.fill"
        default: return "circle.fill"
        }
    }

    private var statusColor: Color {
        switch entry.status {
        case "error": return .red
        case "blocked": return .orange
        case "success": return .green
        case "tool_description_changed": return .yellow
        default: return .gray
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

        let elapsed = -d.timeIntervalSinceNow
        if elapsed < 60 { return "just now" }
        if elapsed < 3600 { return "\(Int(elapsed / 60))m ago" }
        if elapsed < 86400 { return "\(Int(elapsed / 3600))h ago" }
        return "\(Int(elapsed / 86400))d ago"
    }
}

// MARK: - Activity Detail View

struct ActivityDetailView: View {
    let entry: ActivityEntry
    @State private var copiedField: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // Sensitive data warning banner
                if entry.hasSensitiveData == true {
                    HStack(spacing: 8) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .font(.title3)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Sensitive Data Detected")
                                .font(.headline)
                            if let severity = entry.maxSeverity {
                                Text("Max severity: \(severity)")
                                    .font(.subheadline)
                            }
                            if let types = entry.detectionTypes, !types.isEmpty {
                                Text(types.joined(separator: ", "))
                                    .font(.caption)
                            }
                        }
                        Spacer()
                    }
                    .foregroundStyle(.white)
                    .padding(12)
                    .background(Color.red)
                    .cornerRadius(8)
                }

                // Header
                HStack {
                    Image(systemName: detailStatusIcon)
                        .foregroundStyle(detailStatusColor)
                        .font(.title2)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(detailTitle)
                            .font(.title3.bold())
                        Text("Status: \(entry.status)")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                }

                Divider()

                // Metadata grid
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
                }

                // Error message
                if let errorMessage = entry.errorMessage, !errorMessage.isEmpty {
                    Divider()
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text("Error")
                                .font(.headline)
                                .foregroundStyle(.red)
                            Spacer()
                            copyButton(text: errorMessage, field: "error")
                        }
                        Text(errorMessage)
                            .font(.system(.body, design: .monospaced))
                            .foregroundStyle(.red.opacity(0.8))
                            .textSelection(.enabled)
                            .padding(8)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.red.opacity(0.05))
                            .cornerRadius(6)
                    }
                }
            }
            .padding()
        }
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
            Image(systemName: copiedField == field ? "checkmark" : "doc.on.doc")
        }
        .buttonStyle(.borderless)
        .help("Copy to clipboard")
    }

    @ViewBuilder
    private func metadataRow(label: String, value: String) -> some View {
        Text(label)
            .font(.subheadline)
            .foregroundStyle(.secondary)
        Text(value)
            .font(.system(.subheadline, design: .monospaced))
            .textSelection(.enabled)
    }

    private var detailTitle: String {
        var parts: [String] = []
        if let server = entry.serverName, !server.isEmpty {
            parts.append(server)
        }
        if let tool = entry.toolName, !tool.isEmpty {
            parts.append(tool)
        }
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
