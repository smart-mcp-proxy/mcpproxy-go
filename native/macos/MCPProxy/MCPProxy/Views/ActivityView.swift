// ActivityView.swift
// MCPProxy
//
// Shows the activity log with a list on the left and detail panel on the right.
// Supports filtering and refresh. Detail view shows formatted JSON for the
// selected activity entry.

import SwiftUI

// MARK: - Activity View

struct ActivityView: View {
    @ObservedObject var appState: AppState
    let apiClient: APIClient?
    @State private var activities: [ActivityEntry] = []
    @State private var selectedActivityID: String?
    @State private var isLoading = false
    @State private var filterText = ""

    var body: some View {
        HSplitView {
            // Left: activity list
            VStack(alignment: .leading, spacing: 0) {
                activityListHeader
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
                }
            }
            .frame(minWidth: 350)

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
        .task { await loadActivities() }
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
                Task { await loadActivities() }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.borderless)
            .help("Refresh activity log")
        }
        .padding()

        // Search filter
        HStack {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(.secondary)
            TextField("Filter by server, tool, or type...", text: $filterText)
                .textFieldStyle(.plain)
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

    // MARK: - Filtering

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

    // MARK: - Data Loading

    private func loadActivities() async {
        isLoading = true
        defer { isLoading = false }
        guard let client = apiClient else {
            // Fall back to appState's cached activity
            activities = appState.recentActivity
            return
        }
        do {
            activities = try await client.recentActivity(limit: 100)
        } catch {
            // Fall back to cached data on error
            activities = appState.recentActivity
        }
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
                    Text(entry.type)
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
            return entry.type
        }
        return parts.joined(separator: ":")
    }

    private var statusIcon: String {
        if entry.hasSensitiveData == true {
            return "exclamationmark.triangle.fill"
        }
        switch entry.status {
        case "error": return "xmark.circle.fill"
        case "blocked": return "hand.raised.fill"
        case "success": return "checkmark.circle.fill"
        default: return "circle.fill"
        }
    }

    private var statusColor: Color {
        switch entry.status {
        case "error": return .red
        case "blocked": return .orange
        case "success": return .green
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

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
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
                        Text("Error")
                            .font(.headline)
                            .foregroundStyle(.red)
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

                // Sensitive data detections
                if entry.hasSensitiveData == true {
                    Divider()
                    VStack(alignment: .leading, spacing: 4) {
                        Label("Sensitive Data Detected", systemImage: "exclamationmark.triangle.fill")
                            .font(.headline)
                            .foregroundStyle(.red)

                        if let severity = entry.maxSeverity {
                            Text("Max severity: \(severity)")
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }

                        if let types = entry.detectionTypes, !types.isEmpty {
                            Text("Detection types: \(types.joined(separator: ", "))")
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .padding()
        }
    }

    // MARK: - Helpers

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
        default: return "circle.fill"
        }
    }

    private var detailStatusColor: Color {
        switch entry.status {
        case "error": return .red
        case "blocked": return .orange
        case "success": return .green
        default: return .gray
        }
    }
}
