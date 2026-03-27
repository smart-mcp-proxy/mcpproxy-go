// DashboardView.swift
// MCPProxy
//
// Dashboard overview matching the web UI layout:
// Stats cards, servers needing attention, token savings,
// token distribution, recent sessions, recent tool calls.

import SwiftUI

// MARK: - Dashboard View

struct DashboardView: View {
    @ObservedObject var appState: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                // Error banner
                if case .error(let coreError) = appState.coreState {
                    errorBanner(coreError)
                }

                // Stats cards
                statsSection

                // Servers needing attention
                if !appState.serversNeedingAttention.isEmpty {
                    attentionSection
                }

                // Token savings
                tokenSavingsSection

                // Token distribution
                tokenDistributionSection

                // Recent sessions (derived from activity)
                recentSessionsSection

                // Recent tool calls table
                recentToolCallsSection
            }
            .padding(20)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    // MARK: - Stats Cards

    @ViewBuilder
    private var statsSection: some View {
        HStack(spacing: 16) {
            let enabledCount = appState.servers.filter { $0.enabled }.count
            StatCard(
                title: "Total Servers",
                value: "\(appState.totalServers)",
                subtitle: "\(enabledCount) enabled",
                icon: "server.rack",
                color: .blue
            )

            let connPct = appState.totalServers > 0
                ? Int(Double(appState.connectedCount) / Double(appState.totalServers) * 100)
                : 0
            StatCard(
                title: "Connected",
                value: "\(appState.connectedCount)",
                subtitle: "\(connPct)%",
                icon: "link",
                color: .green
            )

            StatCard(
                title: "Total Tools",
                value: "\(appState.totalTools)",
                subtitle: "across all servers",
                icon: "wrench.and.screwdriver",
                color: .indigo
            )

            let quarantined = appState.servers.filter { $0.quarantined }.count
            StatCard(
                title: "Quarantined",
                value: "\(quarantined)",
                subtitle: quarantined == 0 ? "all clear" : "needs review",
                icon: "shield.lefthalf.filled",
                color: quarantined > 0 ? .red : .gray
            )
        }
    }

    // MARK: - Servers Needing Attention

    @ViewBuilder
    private var attentionSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Servers Needing Attention", systemImage: "exclamationmark.triangle.fill")
                .font(.headline)
                .foregroundStyle(.orange)

            VStack(spacing: 1) {
                ForEach(appState.serversNeedingAttention) { server in
                    AttentionRow(server: server, appState: appState)
                }
            }
            .background(Color(nsColor: .controlBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
    }

    // MARK: - Token Savings

    @ViewBuilder
    private var tokenSavingsSection: some View {
        if let stats = appState.tokenMetrics {
            VStack(alignment: .leading, spacing: 12) {
                Label("Token Savings", systemImage: "bolt.fill")
                    .font(.headline)

                HStack(spacing: 16) {
                    // Tokens Saved — prominent green card
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Image(systemName: "arrow.down.circle.fill")
                                .font(.subheadline)
                                .foregroundStyle(.green)
                            Spacer()
                        }
                        Text(formatTokenCount(stats.savedTokens))
                            .font(.title)
                            .fontWeight(.bold)
                            .fontDesign(.rounded)
                            .foregroundStyle(.green)
                        Text("Tokens Saved")
                            .font(.subheadline.weight(.medium))
                            .foregroundStyle(.primary)
                        Text("\(Int(stats.savedTokensPercentage))% reduction")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .clipShape(RoundedRectangle(cornerRadius: 8))

                    // Full tool list size
                    StatCard(
                        title: "Full Tool List Size",
                        value: formatTokenCount(stats.totalServerToolListSize),
                        subtitle: "total tokens",
                        icon: "list.bullet",
                        color: .gray
                    )

                    // Typical query result
                    StatCard(
                        title: "Typical Query Result",
                        value: formatTokenCount(stats.averageQueryResultSize),
                        subtitle: "tokens per query",
                        icon: "magnifyingglass",
                        color: .purple
                    )
                }
            }
        }
    }

    // MARK: - Token Distribution

    @ViewBuilder
    private var tokenDistributionSection: some View {
        if let stats = appState.tokenMetrics,
           let perServer = stats.perServerToolListSizes,
           !perServer.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Label("Token Distribution", systemImage: "chart.bar.fill")
                    .font(.headline)

                let sorted = perServer.sorted { $0.value > $1.value }
                let top = Array(sorted.prefix(6))
                let maxValue = top.first?.value ?? 1

                VStack(spacing: 6) {
                    ForEach(top, id: \.key) { server, size in
                        TokenDistributionBar(
                            serverName: server,
                            tokenSize: size,
                            maxSize: maxValue,
                            totalSize: stats.totalServerToolListSize
                        )
                    }
                }
                .padding(16)
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    // MARK: - Recent Sessions

    @ViewBuilder
    private var recentSessionsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Label("Recent Sessions", systemImage: "person.2.fill")
                    .font(.headline)
                Text("MCP client connections")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            let sessions = deriveSessions()
            if sessions.isEmpty {
                HStack {
                    Spacer()
                    Text("No sessions recorded")
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 20)
                    Spacer()
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                VStack(spacing: 0) {
                    // Header
                    HStack(spacing: 0) {
                        Text("Client")
                            .frame(width: 180, alignment: .leading)
                        Text("Status")
                            .frame(width: 80, alignment: .leading)
                        Text("Tool Calls")
                            .frame(width: 80, alignment: .trailing)
                        Text("Started")
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    }
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color(nsColor: .controlBackgroundColor))

                    Divider()

                    ForEach(sessions) { session in
                        HStack(spacing: 0) {
                            Text(session.displayId)
                                .font(.system(.caption, design: .monospaced))
                                .lineLimit(1)
                                .frame(width: 180, alignment: .leading)

                            DashboardStatusBadge(
                                label: session.hasErrors ? "Error" : "Active",
                                color: session.hasErrors ? .red : .green
                            )
                            .frame(width: 80, alignment: .leading)

                            Text("\(session.toolCallCount)")
                                .font(.caption.monospacedDigit())
                                .frame(width: 80, alignment: .trailing)

                            Text(session.relativeTime)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                        .padding(.horizontal, 12)
                        .padding(.vertical, 5)

                        if session.id != sessions.last?.id {
                            Divider().padding(.leading, 12)
                        }
                    }
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    // MARK: - Recent Tool Calls

    @ViewBuilder
    private var recentToolCallsSection: some View {
        let toolCalls = appState.recentActivity.filter { $0.type == "tool_call" || $0.type == "internal_tool_call" }
        let recent = Array(toolCalls.prefix(10))

        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label("Recent Tool Calls", systemImage: "wrench.and.screwdriver")
                    .font(.headline)
                Spacer()
            }

            if recent.isEmpty {
                HStack {
                    Spacer()
                    Text("No tool calls recorded")
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 20)
                    Spacer()
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                VStack(spacing: 0) {
                    // Header row
                    HStack(spacing: 0) {
                        Text("Time")
                            .frame(width: 70, alignment: .leading)
                        Text("Server")
                            .frame(width: 120, alignment: .leading)
                        Text("Tool")
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Text("Status")
                            .frame(width: 80, alignment: .center)
                        Text("Duration")
                            .frame(width: 70, alignment: .trailing)
                        Text("Intent")
                            .frame(width: 80, alignment: .center)
                    }
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color(nsColor: .controlBackgroundColor))

                    Divider()

                    ForEach(recent) { entry in
                        ToolCallRow(entry: entry)
                        if entry.id != recent.last?.id {
                            Divider().padding(.leading, 12)
                        }
                    }
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    // MARK: - Error Banner

    @ViewBuilder
    private func errorBanner(_ error: CoreError) -> some View {
        HStack(spacing: 12) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.title2)
                .foregroundStyle(.red)

            VStack(alignment: .leading, spacing: 2) {
                Text(error.userMessage)
                    .font(.subheadline.bold())
                    .foregroundStyle(.primary)
                Text(error.remediationHint)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            Spacer()
        }
        .padding(16)
        .background(Color.red.opacity(0.15))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityLabel("Error: \(error.userMessage)")
    }

    // MARK: - Helpers

    private func formatTokenCount(_ count: Int) -> String {
        if count >= 1_000_000 {
            return String(format: "%.1fM", Double(count) / 1_000_000)
        } else if count >= 1_000 {
            return String(format: "%.1fK", Double(count) / 1_000)
        }
        return "\(count)"
    }

    /// Derive session summaries from recent activity entries grouped by sessionId.
    private func deriveSessions() -> [SessionSummary] {
        var grouped: [String: [ActivityEntry]] = [:]
        for entry in appState.recentActivity {
            let key = entry.sessionId ?? "unknown"
            grouped[key, default: []].append(entry)
        }

        var sessions: [SessionSummary] = []
        for (sessionId, entries) in grouped {
            let toolCalls = entries.filter { $0.type == "tool_call" || $0.type == "internal_tool_call" }
            let hasErrors = entries.contains { $0.status == "error" }
            let earliest = entries.compactMap { parseISO8601($0.timestamp) }.min() ?? Date.distantPast
            sessions.append(SessionSummary(
                sessionId: sessionId,
                toolCallCount: toolCalls.count,
                hasErrors: hasErrors,
                startedAt: earliest
            ))
        }

        // Sort by most recent first
        sessions.sort { $0.startedAt > $1.startedAt }
        return Array(sessions.prefix(6))
    }

    private func parseISO8601(_ string: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: string) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: string)
    }
}

// MARK: - Session Summary Model

private struct SessionSummary: Identifiable {
    let sessionId: String
    let toolCallCount: Int
    let hasErrors: Bool
    let startedAt: Date

    var id: String { sessionId }

    var displayId: String {
        if sessionId == "unknown" { return "unknown" }
        if sessionId.count > 16 {
            return String(sessionId.prefix(16)) + "..."
        }
        return sessionId
    }

    var relativeTime: String {
        let interval = Date().timeIntervalSince(startedAt)
        if interval < 60 { return "just now" }
        if interval < 3600 { return "\(Int(interval / 60))m ago" }
        if interval < 86400 { return "\(Int(interval / 3600))h ago" }
        return "\(Int(interval / 86400))d ago"
    }
}

// MARK: - Stat Card

private struct StatCard: View {
    let title: String
    let value: String
    let subtitle: String
    let icon: String
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: icon)
                    .font(.subheadline)
                    .foregroundStyle(color)
                Spacer()
            }
            Text(value)
                .font(.title)
                .fontWeight(.bold)
                .fontDesign(.rounded)
                .foregroundStyle(.primary)
            Text(title)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.primary)
            Text(subtitle)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(title): \(value), \(subtitle)")
    }
}

// MARK: - Token Distribution Bar

private struct TokenDistributionBar: View {
    let serverName: String
    let tokenSize: Int
    let maxSize: Int
    let totalSize: Int

    var body: some View {
        HStack(spacing: 8) {
            Text(serverName)
                .font(.caption)
                .lineLimit(1)
                .frame(width: 120, alignment: .trailing)

            GeometryReader { geometry in
                let fraction = maxSize > 0 ? CGFloat(tokenSize) / CGFloat(maxSize) : 0
                RoundedRectangle(cornerRadius: 3)
                    .fill(Color.blue.opacity(0.7))
                    .frame(width: max(4, geometry.size.width * fraction), height: 16)
            }
            .frame(height: 16)

            let pct = totalSize > 0 ? Double(tokenSize) / Double(totalSize) * 100 : 0
            Text(String(format: "%.0f%%", pct))
                .font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: 40, alignment: .trailing)

            Text(formatTokenSize(tokenSize))
                .font(.caption.monospacedDigit())
                .foregroundStyle(.tertiary)
                .frame(width: 50, alignment: .trailing)
        }
    }

    private func formatTokenSize(_ count: Int) -> String {
        if count >= 1_000_000 {
            return String(format: "%.1fM", Double(count) / 1_000_000)
        } else if count >= 1_000 {
            return String(format: "%.1fK", Double(count) / 1_000)
        }
        return "\(count)"
    }
}

// MARK: - Dashboard Status Badge

private struct DashboardStatusBadge: View {
    let label: String
    let color: Color

    var body: some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(color.opacity(0.15))
            .foregroundStyle(color)
            .clipShape(Capsule())
            .accessibilityLabel("Status: \(label)")
    }
}

// MARK: - Tool Call Row

private struct ToolCallRow: View {
    let entry: ActivityEntry

    var body: some View {
        HStack(spacing: 0) {
            Text(relativeTime)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(width: 70, alignment: .leading)

            Text(entry.serverName ?? "-")
                .font(.caption)
                .lineLimit(1)
                .frame(width: 120, alignment: .leading)

            Text(entry.toolName ?? entry.type)
                .font(.caption)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)

            DashboardStatusBadge(
                label: statusLabel,
                color: statusColor
            )
            .frame(width: 80, alignment: .center)

            if let duration = entry.durationMs {
                Text("\(duration)ms")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(width: 70, alignment: .trailing)
            } else {
                Text("-")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .frame(width: 70, alignment: .trailing)
            }

            if let op = entry.intentOperationType {
                ToolCallIntentBadge(operationType: op)
                    .frame(width: 80, alignment: .center)
            } else {
                Text("-")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .frame(width: 80, alignment: .center)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    private var statusLabel: String {
        switch entry.status {
        case "success": return "Success"
        case "error": return "Error"
        case "blocked": return "Blocked"
        default: return entry.status
        }
    }

    private var statusColor: Color {
        switch entry.status {
        case "success": return .green
        case "error": return .red
        case "blocked": return .orange
        default: return .gray
        }
    }

    private var relativeTime: String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = formatter.date(from: entry.timestamp)
        if date == nil {
            formatter.formatOptions = [.withInternetDateTime]
            date = formatter.date(from: entry.timestamp)
        }
        guard let d = date else { return "-" }

        let interval = Date().timeIntervalSince(d)
        if interval < 60 { return "now" }
        if interval < 3600 { return "\(Int(interval / 60))m" }
        if interval < 86400 { return "\(Int(interval / 3600))h" }
        return "\(Int(interval / 86400))d"
    }
}

// MARK: - Tool Call Intent Badge

private struct ToolCallIntentBadge: View {
    let operationType: String

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: iconName)
                .font(.system(size: 8))
            Text(operationType)
                .font(.caption2.weight(.semibold))
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

// MARK: - Attention Row

private struct AttentionRow: View {
    let server: ServerStatus
    let appState: AppState

    var body: some View {
        HStack {
            Image(systemName: server.health?.healthLevel.sfSymbolName ?? "questionmark.circle")
                .foregroundStyle(server.statusColor)
                .accessibilityLabel("Health: \(server.health?.level ?? "unknown")")

            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.subheadline.weight(.medium))
                if let detail = server.health?.summary {
                    Text(detail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            Spacer()

            if let action = server.health?.healthAction {
                Button(action.label) {
                    Task { await performAction(action, for: server) }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .tint(actionColor(action))
                .accessibilityLabel("\(action.label) \(server.name)")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    private func actionColor(_ action: HealthAction) -> Color {
        switch action {
        case .login: return .blue
        case .restart: return .orange
        case .approve: return .green
        default: return .accentColor
        }
    }

    private func performAction(_ action: HealthAction, for server: ServerStatus) async {
        guard let client = appState.apiClient else { return }
        do {
            switch action {
            case .login:
                try await client.loginServer(server.id)
            case .restart:
                try await client.restartServer(server.id)
            case .enable:
                try await client.enableServer(server.id)
            case .approve:
                try await client.approveTools(server.id)
            default:
                break
            }
        } catch {
            // Action errors are visible via server health refresh
        }
    }
}
