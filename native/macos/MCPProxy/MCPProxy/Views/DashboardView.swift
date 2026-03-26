// DashboardView.swift
// MCPProxy
//
// Dashboard overview showing server stats, servers needing attention,
// recent activity, and token savings.

import SwiftUI

// MARK: - Dashboard View

struct DashboardView: View {
    @ObservedObject var appState: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                // Stats cards
                statsSection

                // Servers needing attention
                if !appState.serversNeedingAttention.isEmpty {
                    attentionSection
                }

                // Recent activity
                recentActivitySection

                // Token savings
                tokenSavingsSection
            }
            .padding(20)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    // MARK: - Stats Cards

    @ViewBuilder
    private var statsSection: some View {
        HStack(spacing: 12) {
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

    // MARK: - Recent Activity

    @ViewBuilder
    private var recentActivitySection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Recent Activity", systemImage: "clock.arrow.circlepath")
                .font(.headline)

            if appState.recentActivity.isEmpty {
                HStack {
                    Spacer()
                    Text("No recent activity")
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 20)
                    Spacer()
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                VStack(spacing: 1) {
                    ForEach(appState.recentActivity.prefix(10)) { entry in
                        DashboardActivityRow(entry: entry)
                    }
                }
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    // MARK: - Token Savings

    @ViewBuilder
    private var tokenSavingsSection: some View {
        if let stats = appState.tokenMetrics {
            VStack(alignment: .leading, spacing: 8) {
                Label("Token Savings", systemImage: "bolt.fill")
                    .font(.headline)

                HStack(spacing: 12) {
                    StatCard(
                        title: "Saved",
                        value: "\(Int(stats.savedTokensPercentage))%",
                        subtitle: "\(formatTokenCount(stats.savedTokens)) tokens",
                        icon: "arrow.down.circle.fill",
                        color: .green
                    )

                    StatCard(
                        title: "Full Tool List",
                        value: formatTokenCount(stats.totalServerToolListSize),
                        subtitle: "tokens",
                        icon: "list.bullet",
                        color: .gray
                    )

                    StatCard(
                        title: "Avg Query Size",
                        value: formatTokenCount(stats.averageQueryResultSize),
                        subtitle: "tokens per query",
                        icon: "magnifyingglass",
                        color: .purple
                    )
                }
            }
        }
    }

    private func formatTokenCount(_ count: Int) -> String {
        if count >= 1_000_000 {
            return String(format: "%.1fM", Double(count) / 1_000_000)
        } else if count >= 1_000 {
            return String(format: "%.1fK", Double(count) / 1_000)
        }
        return "\(count)"
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
                    .font(.system(size: 14))
                    .foregroundStyle(color)
                Spacer()
            }
            Text(value)
                .font(.system(size: 28, weight: .bold, design: .rounded))
                .foregroundStyle(.primary)
            Text(title)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.primary)
            Text(subtitle)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }
}

// MARK: - Attention Row

private struct AttentionRow: View {
    let server: ServerStatus
    let appState: AppState

    var body: some View {
        HStack {
            Image(systemName: server.health?.healthLevel.sfSymbolName ?? "questionmark.circle")
                .foregroundStyle(healthColor)

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
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var healthColor: Color {
        switch server.health?.healthLevel {
        case .unhealthy: return .red
        case .degraded: return .orange
        default: return .gray
        }
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

// MARK: - Activity Row

private struct DashboardActivityRow: View {
    let entry: ActivityEntry

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: typeIcon)
                .font(.system(size: 12))
                .foregroundStyle(statusColor)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 4) {
                    if let server = entry.serverName {
                        Text(server)
                            .font(.caption.weight(.medium))
                        if let tool = entry.toolName {
                            Text(":")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Text(tool)
                                .font(.caption)
                        }
                    } else {
                        Text(entry.type)
                            .font(.caption.weight(.medium))
                    }
                }
                Text(relativeTime)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }

            Spacer()

            if entry.status == "error" {
                Image(systemName: "exclamationmark.circle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(.red)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
    }

    private var typeIcon: String {
        switch entry.type {
        case "tool_call": return "wrench.fill"
        case "connection": return "link"
        case "security": return "shield.fill"
        case "config": return "gearshape.fill"
        default: return "circle.fill"
        }
    }

    private var statusColor: Color {
        switch entry.status {
        case "success": return .green
        case "error": return .red
        case "blocked": return .orange
        default: return .secondary
        }
    }

    private var relativeTime: String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = formatter.date(from: entry.timestamp) else {
            // Try without fractional seconds
            formatter.formatOptions = [.withInternetDateTime]
            guard let date = formatter.date(from: entry.timestamp) else {
                return entry.timestamp
            }
            return formatRelative(date)
        }
        return formatRelative(date)
    }

    private func formatRelative(_ date: Date) -> String {
        let interval = Date().timeIntervalSince(date)
        if interval < 60 { return "just now" }
        if interval < 3600 { return "\(Int(interval / 60))m ago" }
        if interval < 86400 { return "\(Int(interval / 3600))h ago" }
        return "\(Int(interval / 86400))d ago"
    }
}
