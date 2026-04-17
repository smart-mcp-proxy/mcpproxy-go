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
    @Environment(\.fontScale) var fontScale
    @State private var showConnectClients = false
    @State private var mcpSessions: [APIClient.MCPSession] = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                // Error banner
                if case .error(let coreError) = appState.coreState {
                    errorBanner(coreError)
                }

                // Hub visualization
                hubSection

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
                recentActivitySection
            }
            .padding(20)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .task {
            do {
                mcpSessions = try await appState.apiClient?.sessions(limit: 20) ?? []
            } catch {
                // Non-fatal; sessions will be empty
            }
        }
        .sheet(isPresented: $showConnectClients) {
            ConnectClientsSheet(appState: appState, isPresented: $showConnectClients)
        }
    }

    // MARK: - Hub Visualization

    @ViewBuilder
    private var hubSection: some View {
        VStack(spacing: 12) {
            // Token savings badge — top center
            if let stats = appState.tokenMetrics {
                HStack(spacing: 4) {
                    Image(systemName: "arrow.down.right")
                        .font(.system(size: 10 * fontScale))
                    Text("\(stats.savedTokensPercentage >= 99.995 ? "99.99" : String(format: "%.1f", stats.savedTokensPercentage))%")
                        .font(.scaled(.title2, scale: fontScale))
                        .fontWeight(.bold)
                    Text("tokens saved")
                        .font(.scaled(.caption, scale: fontScale))
                }
                .foregroundStyle(.green)
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(Color.green.opacity(0.1))
                .clipShape(Capsule())
            }

            // Three-column hub: agents — line — shield — line — servers
            HStack(alignment: .top, spacing: 0) {
                // === LEFT COLUMN: AI Agents ===
                VStack(alignment: .leading, spacing: 8) {
                    // Single box with label inside
                    VStack(alignment: .leading, spacing: 6) {
                        Text("AI AGENTS")
                            .font(.scaled(.caption2, scale: fontScale))
                            .fontWeight(.bold)
                            .tracking(1)
                            .foregroundStyle(.secondary)

                        let connectedNames = connectedClientNames(from: mcpSessions)

                        if !connectedNames.isEmpty {
                            HStack(spacing: 6) {
                                Circle().fill(.green).frame(width: 8, height: 8)
                                Text("CONNECTED")
                                    .font(.scaled(.caption2, scale: fontScale))
                                    .fontWeight(.bold)
                                    .foregroundStyle(.secondary)
                            }
                            Text(connectedNames.joined(separator: ", "))
                                .font(.scaled(.caption, scale: fontScale))
                                .fontWeight(.medium)
                        }

                        let available = ["Claude Code", "Cursor", "VS Code", "Codex", "Gemini"]
                            .filter { name in !connectedNames.contains(name) }
                        if !available.isEmpty {
                            Text("Available: " + available.joined(separator: ", "))
                                .font(.scaled(.caption2, scale: fontScale))
                                .foregroundStyle(.tertiary)
                        }

                        if connectedNames.isEmpty {
                            Text("No agents detected")
                                .font(.scaled(.caption, scale: fontScale))
                                .foregroundStyle(.secondary)
                        }
                    }
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .clipShape(RoundedRectangle(cornerRadius: 8))

                    // Left column buttons
                    VStack(spacing: 6) {
                        Button {
                            showConnectClients = true
                        } label: {
                            Label("Connect Clients", systemImage: "link")
                                .font(.scaled(.caption, scale: fontScale))
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .accessibilityLabel("Connect AI clients to MCPProxy")

                        Button {
                            NotificationCenter.default.post(name: .switchToServers, object: nil)
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                                NotificationCenter.default.post(name: .showAddServer, object: AddServerTab.importConfig)
                            }
                        } label: {
                            Label("Import from client configs", systemImage: "square.and.arrow.down")
                                .font(.scaled(.caption, scale: fontScale))
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .accessibilityLabel("Import servers from AI client configuration files")

                        // Recent Sessions link
                        Button {
                            NotificationCenter.default.post(name: .switchToActivity, object: nil)
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: "clock.arrow.circlepath")
                                    .font(.system(size: 10 * fontScale))
                                Text("Recent Sessions")
                                    .font(.scaled(.caption, scale: fontScale))
                            }
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                        .padding(.top, 2)
                        .onHover { hovering in
                            if hovering {
                                NSCursor.pointingHand.push()
                            } else {
                                NSCursor.pop()
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity)

                // Left connection line
                HubConnectionLine(fontScale: fontScale)
                    .frame(minWidth: 60, maxWidth: .infinity)
                    .padding(.top, 55)

                // === CENTER COLUMN: MCPProxy logo + status ===
                VStack(spacing: 10) {
                    // App logo (MCPProxy shield with M C P circles)
                    if let appIcon = NSApp.applicationIconImage {
                        Image(nsImage: appIcon)
                            .resizable()
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 80 * fontScale, height: 80 * fontScale)
                            .opacity(appState.coreState == .connected ? 1.0 : 0.4)
                    } else {
                        // Fallback to SF Symbol if icon not available
                        Image(systemName: "shield.fill")
                            .font(.system(size: 60 * fontScale))
                            .foregroundStyle(appState.coreState == .connected ? Color.accentColor : .gray)
                    }

                    // Status text
                    VStack(spacing: 2) {
                        Text("MCPPROXY")
                            .font(.scaled(.caption2, scale: fontScale))
                            .fontWeight(.bold)
                            .tracking(1)
                            .foregroundStyle(.primary)
                        Text(appState.coreState == .connected ? "active" : "stopped")
                            .font(.scaled(.caption, scale: fontScale))
                            .fontWeight(.medium)
                            .foregroundStyle(appState.coreState == .connected ? .green : .red)
                    }

                    // Security status badges
                    VStack(alignment: .leading, spacing: 6) {
                        // Docker isolation
                        let dockerAvailable = appState.dockerAvailable
                        HStack(spacing: 6) {
                            Image(systemName: dockerAvailable ? "checkmark.shield.fill" : "exclamationmark.triangle.fill")
                                .font(.system(size: 10 * fontScale))
                                .foregroundStyle(dockerAvailable ? .green : .orange)
                            Text(dockerAvailable ? "Docker isolation active" : "Docker isolation disabled")
                                .font(.scaled(.caption2, scale: fontScale))
                                .foregroundStyle(dockerAvailable ? .green : .orange)
                        }

                        // Quarantine protection
                        let quarantineActive = appState.quarantineEnabled
                        HStack(spacing: 6) {
                            Image(systemName: quarantineActive ? "checkmark.shield.fill" : "exclamationmark.triangle.fill")
                                .font(.system(size: 10 * fontScale))
                                .foregroundStyle(quarantineActive ? .green : .orange)
                            Text(quarantineActive ? "Quarantine protection active" : "Quarantine protection disabled")
                                .font(.scaled(.caption2, scale: fontScale))
                                .foregroundStyle(quarantineActive ? .green : .orange)
                        }

                        // Activity Log link
                        Button {
                            NotificationCenter.default.post(name: .switchToActivity, object: nil)
                        } label: {
                            HStack(spacing: 6) {
                                Image(systemName: "eye")
                                    .font(.system(size: 10 * fontScale))
                                Text("Activity Log")
                                    .font(.scaled(.caption2, scale: fontScale))
                            }
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                        .onHover { hovering in
                            if hovering {
                                NSCursor.pointingHand.push()
                            } else {
                                NSCursor.pop()
                            }
                        }
                    }
                    .padding(.top, 4)
                }
                .frame(width: 200)

                // Right connection line
                HubConnectionLine(fontScale: fontScale)
                    .frame(minWidth: 60, maxWidth: .infinity)
                    .padding(.top, 55)

                // === RIGHT COLUMN: Upstream Servers ===
                VStack(alignment: .leading, spacing: 8) {
                    // Single box with label inside
                    VStack(alignment: .leading, spacing: 6) {
                        Text("UPSTREAM SERVERS")
                            .font(.scaled(.caption2, scale: fontScale))
                            .fontWeight(.bold)
                            .tracking(1)
                            .foregroundStyle(.secondary)

                        let disabled = appState.servers.filter({ !$0.enabled }).count
                        let quarantined = appState.servers.filter { $0.quarantined }.count

                        // "13 connected / 10 disabled" on one line
                        HStack(spacing: 4) {
                            Circle().fill(.green).frame(width: 8, height: 8)
                            Text("\(appState.connectedCount)")
                                .font(.scaled(.title, scale: fontScale))
                                .fontWeight(.bold)
                            Text("connected")
                                .font(.scaled(.caption, scale: fontScale))
                                .foregroundStyle(.secondary)
                            if disabled > 0 {
                                Text("/")
                                    .font(.scaled(.caption, scale: fontScale))
                                    .foregroundStyle(.tertiary)
                                Text("\(disabled) disabled")
                                    .font(.scaled(.caption, scale: fontScale))
                                    .foregroundStyle(.secondary)
                            }
                        }

                        // "197 tools"
                        Text("\(appState.totalTools) tools")
                            .font(.scaled(.caption, scale: fontScale))

                        if quarantined > 0 {
                            HStack(spacing: 4) {
                                Image(systemName: "lock.shield")
                                    .font(.system(size: 10 * fontScale))
                                    .foregroundStyle(.orange)
                                Text("\(quarantined) in quarantine")
                                    .font(.scaled(.caption2, scale: fontScale))
                                    .foregroundStyle(.orange)
                            }
                        }
                    }
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .clipShape(RoundedRectangle(cornerRadius: 8))

                    // Right column buttons
                    VStack(spacing: 6) {
                        Button {
                            NotificationCenter.default.post(name: .switchToServers, object: nil)
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                                NotificationCenter.default.post(name: .showAddServer, object: nil)
                            }
                        } label: {
                            Label("Add Server", systemImage: "plus")
                                .font(.scaled(.caption, scale: fontScale))
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .accessibilityLabel("Add a new upstream MCP server")
                    }
                }
                .frame(maxWidth: .infinity)
            }
        }
        .padding(.vertical, 8)
    }

    // MARK: - Servers Needing Attention

    @ViewBuilder
    private var attentionSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Servers Needing Attention", systemImage: "exclamationmark.triangle.fill")
                .font(.scaled(.headline, scale: fontScale))
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
                    .font(.scaled(.headline, scale: fontScale))

                HStack(spacing: 16) {
                    // Tokens Saved — prominent green card
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Image(systemName: "arrow.down.circle.fill")
                                .font(.scaled(.subheadline, scale: fontScale))
                                .foregroundStyle(.green)
                            Spacer()
                        }
                        Text(formatTokenCount(stats.savedTokens))
                            .font(.scaled(.title, scale: fontScale))
                            .fontWeight(.bold)
                            .fontDesign(.rounded)
                            .foregroundStyle(.green)
                        Text("Tokens Saved")
                            .font(.scaled(.subheadline, scale: fontScale).weight(.medium))
                            .foregroundStyle(.primary)
                        Text("\(Int(stats.savedTokensPercentage))% reduction")
                            .font(.scaled(.caption, scale: fontScale))
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
                    .font(.scaled(.headline, scale: fontScale))

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
                    .font(.scaled(.headline, scale: fontScale))
                Text("MCP client connections")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            }

            // Deduplicate by client name: prefer session with most tool calls, then most recent
            let rawSessions = mcpSessions.isEmpty ? appState.recentSessions : mcpSessions
            let sessions: [APIClient.MCPSession] = {
                var byClient: [String: APIClient.MCPSession] = [:]
                for s in rawSessions {
                    let key = s.clientName ?? "unknown"
                    if let existing = byClient[key] {
                        let existingCalls = existing.toolCallCount ?? 0
                        let newCalls = s.toolCallCount ?? 0
                        // Prefer active sessions, then most tool calls, then most recent
                        if s.status == "active" && existing.status != "active" {
                            byClient[key] = s
                        } else if newCalls > existingCalls {
                            byClient[key] = s
                        } else if newCalls == existingCalls {
                            let existingTime = existing.lastActive ?? existing.startTime ?? ""
                            let newTime = s.lastActive ?? s.startTime ?? ""
                            if newTime > existingTime { byClient[key] = s }
                        }
                    } else {
                        byClient[key] = s
                    }
                }
                // Sort most-recently-active first, break ties by tool call count,
                // then by session ID so the order is deterministic when timestamps
                // and call counts match. `.values` iteration is random, so an
                // explicit sort key is required to keep the UI stable.
                return byClient.values.sorted { lhs, rhs in
                    let lTime = lhs.lastActive ?? lhs.startTime ?? ""
                    let rTime = rhs.lastActive ?? rhs.startTime ?? ""
                    if lTime != rTime { return lTime > rTime }
                    let lCalls = lhs.toolCallCount ?? 0
                    let rCalls = rhs.toolCallCount ?? 0
                    if lCalls != rCalls { return lCalls > rCalls }
                    return lhs.id < rhs.id
                }
            }()
            if sessions.isEmpty {
                HStack {
                    Spacer()
                    Text("No sessions recorded")
                        .font(.scaled(.body, scale: fontScale))
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
                        Text("Last Active")
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    }
                    .font(.scaled(.caption, scale: fontScale).weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color(nsColor: .controlBackgroundColor))

                    Divider()

                    ForEach(sessions) { session in
                        HStack(spacing: 0) {
                            Text(sessionDisplayName(session))
                                .font(.scaled(.caption, scale: fontScale))
                                .lineLimit(1)
                                .frame(width: 180, alignment: .leading)

                            DashboardStatusBadge(
                                label: session.status == "active" ? "Active" : (session.status == "closed" ? "Closed" : session.status.capitalized),
                                color: session.status == "active" ? .green : .gray,
                                fontScale: fontScale
                            )
                            .frame(width: 80, alignment: .leading)

                            Text("\(session.toolCallCount ?? 0)")
                                .font(.scaledMonospacedDigit(.caption, scale: fontScale))
                                .frame(width: 80, alignment: .trailing)

                            Text(sessionRelativeTime(session.lastActive ?? session.startTime))
                                .font(.scaled(.caption, scale: fontScale))
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

    // MARK: - Recent Activity
    //
    // Shows the full activity log on the dashboard (not just tool calls) so
    // users with a quiet proxy still see something useful — security scans,
    // tool quarantine changes, OAuth events, etc. System-level events
    // (`system_start`, `system_stop`) are filtered out because they're noise
    // on the dashboard.

    private static let activityDashboardHiddenTypes: Set<String> = [
        "system_start", "system_stop",
    ]

    @ViewBuilder
    private var recentActivitySection: some View {
        let entries = appState.recentActivity.filter { entry in
            !Self.activityDashboardHiddenTypes.contains(entry.type)
        }
        let recent = Array(entries.prefix(10))

        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label("Recent Activity", systemImage: "clock.arrow.circlepath")
                    .font(.scaled(.headline, scale: fontScale))
                Spacer()
            }

            if recent.isEmpty {
                HStack {
                    Spacer()
                    Text("No activity recorded")
                        .font(.scaled(.body, scale: fontScale))
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
                        Text("Event")
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Text("Status")
                            .frame(width: 80, alignment: .center)
                        Text("Duration")
                            .frame(width: 70, alignment: .trailing)
                        Text("Intent")
                            .frame(width: 80, alignment: .center)
                    }
                    .font(.scaled(.caption, scale: fontScale).weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color(nsColor: .controlBackgroundColor))

                    Divider()

                    ForEach(recent) { entry in
                        ToolCallRow(entry: entry, fontScale: fontScale)
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
                .font(.scaled(.title2, scale: fontScale))
                .foregroundStyle(.red)

            VStack(alignment: .leading, spacing: 2) {
                Text(error.userMessage)
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                    .foregroundStyle(.primary)
                Text(error.remediationHint)
                    .font(.scaled(.caption, scale: fontScale))
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

    /// Extract human-readable client names from real MCP sessions.
    /// Uses client_name field when available, falls back to session ID heuristics.
    private func connectedClientNames(from sessions: [APIClient.MCPSession]) -> [String] {
        let activeSessions = sessions.filter { $0.status == "active" }
        if activeSessions.isEmpty { return [] }
        var names: Set<String> = []
        for session in activeSessions {
            if let clientName = session.clientName, !clientName.isEmpty {
                // Use known display names for common clients
                let lower = clientName.lowercased()
                if lower.contains("claude") { names.insert("Claude Code") }
                else if lower.contains("cursor") { names.insert("Cursor") }
                else if lower.contains("vscode") || lower.contains("copilot") { names.insert("VS Code") }
                else if lower.contains("codex") { names.insert("Codex") }
                else if lower.contains("gemini") { names.insert("Gemini") }
                else if lower.contains("antigravity") { names.insert("Antigravity") }
                else if lower.contains("windsurf") { names.insert("Windsurf") }
                else { names.insert(clientName) }
            } else {
                // Fallback to session ID heuristics
                let id = session.id.lowercased()
                if id.contains("claude") { names.insert("Claude Code") }
                else if id.contains("cursor") { names.insert("Cursor") }
                else if id.contains("vscode") || id.contains("copilot") { names.insert("VS Code") }
                else { names.insert("MCP Client") }
            }
        }
        return names.sorted()
    }

    /// Display name for a session, preferring client_name with version.
    private func sessionDisplayName(_ session: APIClient.MCPSession) -> String {
        if let name = session.clientName, !name.isEmpty {
            if let version = session.clientVersion, !version.isEmpty {
                return "\(name) \(version)"
            }
            return name
        }
        // Fallback to truncated session ID
        if session.id.count > 20 {
            return String(session.id.prefix(20)) + "..."
        }
        return session.id
    }

    /// Relative time string from an ISO 8601 timestamp.
    private func sessionRelativeTime(_ timestamp: String?) -> String {
        guard let timestamp else { return "-" }
        guard let date = parseISO8601(timestamp) else { return "-" }
        let interval = Date().timeIntervalSince(date)
        if interval < 60 { return "just now" }
        if interval < 3600 { return "\(Int(interval / 60))m ago" }
        if interval < 86400 { return "\(Int(interval / 3600))h ago" }
        return "\(Int(interval / 86400))d ago"
    }

    private func parseISO8601(_ string: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: string) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: string)
    }
}

// MARK: - Stat Card

private struct StatCard: View {
    let title: String
    let value: String
    let subtitle: String
    let icon: String
    let color: Color
    @Environment(\.fontScale) var fontScale

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: icon)
                    .font(.scaled(.subheadline, scale: fontScale))
                    .foregroundStyle(color)
                Spacer()
            }
            Text(value)
                .font(.scaled(.title, scale: fontScale))
                .fontWeight(.bold)
                .fontDesign(.rounded)
                .foregroundStyle(.primary)
            Text(title)
                .font(.scaled(.subheadline, scale: fontScale).weight(.medium))
                .foregroundStyle(.primary)
            Text(subtitle)
                .font(.scaled(.caption, scale: fontScale))
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
    @Environment(\.fontScale) var fontScale

    var body: some View {
        HStack(spacing: 8) {
            Text(serverName)
                .font(.scaled(.caption, scale: fontScale))
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
                .font(.scaledMonospacedDigit(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(width: 40, alignment: .trailing)

            Text(formatTokenSize(tokenSize))
                .font(.scaledMonospacedDigit(.caption, scale: fontScale))
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

// MARK: - Hub Connection Line (animated green dots on a fat line)

private struct HubConnectionLine: View {
    var fontScale: CGFloat = 1.0
    @State private var dotOffset: CGFloat = 4
    @State private var lineWidth: CGFloat = 100

    var body: some View {
        GeometryReader { geometry in
            let w = geometry.size.width
            let h = geometry.size.height
            let midY = h / 2

            ZStack {
                // Fat green line
                Rectangle()
                    .fill(Color.green.opacity(0.25))
                    .frame(height: 4)
                    .position(x: w / 2, y: midY)

                // Animated green dot
                Circle()
                    .fill(Color.green)
                    .frame(width: 8, height: 8)
                    .position(x: dotOffset, y: midY)
                    .opacity(0.9)

                // Static endpoint dots
                Circle()
                    .fill(Color.green.opacity(0.7))
                    .frame(width: 6, height: 6)
                    .position(x: 4, y: midY)
                Circle()
                    .fill(Color.green.opacity(0.7))
                    .frame(width: 6, height: 6)
                    .position(x: w - 4, y: midY)
            }
            .onAppear {
                lineWidth = w
                // Trigger once immediately
                dotOffset = 4
                withAnimation(.linear(duration: 2.0)) {
                    dotOffset = w - 4
                }
                // Then repeat every 20 seconds
                Timer.scheduledTimer(withTimeInterval: 20.0, repeats: true) { _ in
                    dotOffset = 4
                    withAnimation(.linear(duration: 2.0)) {
                        dotOffset = w - 4
                    }
                }
            }
            .onChange(of: geometry.size.width) { newWidth in
                lineWidth = newWidth
            }
        }
        .frame(height: 20)
    }
}

// MARK: - Dashboard Status Badge

private struct DashboardStatusBadge: View {
    let label: String
    let color: Color
    var fontScale: CGFloat = 1.0

    var body: some View {
        Text(label)
            .font(.scaled(.caption2, scale: fontScale).weight(.semibold))
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
    var fontScale: CGFloat = 1.0

    var body: some View {
        HStack(spacing: 0) {
            Text(relativeTime)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(width: 70, alignment: .leading)

            Text(entry.serverName ?? "-")
                .font(.scaled(.caption, scale: fontScale))
                .lineLimit(1)
                .frame(width: 120, alignment: .leading)

            Text(entry.toolName ?? entry.type)
                .font(.scaled(.caption, scale: fontScale))
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)

            DashboardStatusBadge(
                label: statusLabel,
                color: statusColor,
                fontScale: fontScale
            )
            .frame(width: 80, alignment: .center)

            if let duration = entry.durationMs {
                Text("\(duration)ms")
                    .font(.scaledMonospacedDigit(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .frame(width: 70, alignment: .trailing)
            } else {
                Text("-")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .frame(width: 70, alignment: .trailing)
            }

            if let op = entry.intentOperationType {
                ToolCallIntentBadge(operationType: op, fontScale: fontScale)
                    .frame(width: 80, alignment: .center)
            } else {
                Text("-")
                    .font(.scaled(.caption, scale: fontScale))
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

// MARK: - Attention Row

private struct AttentionRow: View {
    let server: ServerStatus
    let appState: AppState
    @Environment(\.fontScale) var fontScale

    var body: some View {
        HStack {
            Image(systemName: server.health?.healthLevel.sfSymbolName ?? "questionmark.circle")
                .foregroundStyle(server.statusColor)
                .accessibilityLabel("Health: \(server.health?.level ?? "unknown")")

            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.scaled(.subheadline, scale: fontScale).weight(.medium))
                if let detail = server.health?.summary {
                    Text(detail)
                        .font(.scaled(.caption, scale: fontScale))
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

// MARK: - Connect Clients Sheet

private struct ConnectClientsSheet: View {
    @ObservedObject var appState: AppState
    @Binding var isPresented: Bool
    @State private var clients: [APIClient.ClientStatus] = []
    @State private var loading = true
    @State private var errorMessage: String?
    @State private var successMessage: String?
    @State private var actionInProgress: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            // Header
            VStack(alignment: .leading, spacing: 4) {
                Text("Connect MCPProxy to AI Agents")
                    .font(.headline)
                Text("Register MCPProxy in your AI tools' MCP config files so they can discover and use your upstream servers.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Divider()

            if loading {
                HStack {
                    Spacer()
                    ProgressView("Loading clients...")
                    Spacer()
                }
                .padding(.vertical, 20)
            } else if clients.isEmpty {
                HStack {
                    Spacer()
                    VStack(spacing: 8) {
                        Image(systemName: "questionmark.circle")
                            .font(.title2)
                            .foregroundStyle(.secondary)
                        Text("No AI clients detected")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                        Text("The connect API may not be available in this version.")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    Spacer()
                }
                .padding(.vertical, 20)
            } else {
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(clients) { client in
                            clientRow(client)
                            if client.id != clients.last?.id {
                                Divider().padding(.leading, 8)
                            }
                        }
                    }
                }
                .frame(maxHeight: 300)
            }

            // Status messages
            if let msg = successMessage {
                HStack(spacing: 6) {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                    Text(msg)
                        .font(.caption)
                        .foregroundStyle(.green)
                }
            }

            if let err = errorMessage {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                    Text(err)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }

            Divider()

            // Footer
            HStack {
                Button("Refresh") {
                    loadClients()
                }
                .controlSize(.small)

                Spacer()

                Button("Close") {
                    isPresented = false
                }
                .keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 500)
        .onAppear { loadClients() }
    }

    @ViewBuilder
    private func clientRow(_ client: APIClient.ClientStatus) -> some View {
        HStack(spacing: 12) {
            // Client icon
            Image(systemName: clientIcon(for: client.clientId))
                .font(.title3)
                .foregroundStyle(client.connected ? .green : .secondary)
                .frame(width: 24)

            // Client info
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(client.name)
                        .font(.subheadline)
                        .fontWeight(.medium)
                    if client.connected {
                        Text("Connected")
                            .font(.caption2)
                            .fontWeight(.semibold)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.green.opacity(0.15))
                            .foregroundStyle(.green)
                            .clipShape(Capsule())
                    }
                }
                Text(client.configPath)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            Spacer()

            // Action button
            if !client.supported {
                Text(client.reason ?? "Not supported")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            } else if client.connected {
                Button {
                    disconnect(client.clientId)
                } label: {
                    if actionInProgress == client.clientId {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Text("Disconnect")
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(actionInProgress != nil)
            } else {
                Button {
                    connect(client.clientId)
                } label: {
                    if actionInProgress == client.clientId {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Text("Connect")
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(actionInProgress != nil)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 8)
    }

    private func clientIcon(for clientId: String) -> String {
        switch clientId {
        case "claude-code", "claude-desktop":
            return "brain"
        case "cursor":
            return "cursorarrow.rays"
        case "vscode", "copilot":
            return "chevron.left.forwardslash.chevron.right"
        case "windsurf":
            return "wind"
        default:
            return "app.connected.to.app.below.fill"
        }
    }

    private func loadClients() {
        loading = true
        errorMessage = nil
        successMessage = nil
        Task {
            guard let client = appState.apiClient else {
                await MainActor.run {
                    loading = false
                    errorMessage = "Core is not connected"
                }
                return
            }
            do {
                let result = try await client.connectClients()
                await MainActor.run {
                    clients = result
                    loading = false
                }
            } catch {
                await MainActor.run {
                    loading = false
                    errorMessage = error.localizedDescription
                }
            }
        }
    }

    private func connect(_ clientId: String) {
        errorMessage = nil
        successMessage = nil
        actionInProgress = clientId
        Task {
            guard let client = appState.apiClient else { return }
            do {
                let result = try await client.connectToClient(clientId)
                await MainActor.run {
                    actionInProgress = nil
                    if result.success {
                        successMessage = result.message ?? "Connected to \(clientId)"
                    } else {
                        errorMessage = result.message ?? "Failed to connect"
                    }
                }
                // Reload the list to reflect changes
                loadClients()
            } catch {
                await MainActor.run {
                    actionInProgress = nil
                    errorMessage = error.localizedDescription
                }
            }
        }
    }

    private func disconnect(_ clientId: String) {
        errorMessage = nil
        successMessage = nil
        actionInProgress = clientId
        Task {
            guard let client = appState.apiClient else { return }
            do {
                let result = try await client.disconnectFromClient(clientId)
                await MainActor.run {
                    actionInProgress = nil
                    if result.success {
                        successMessage = result.message ?? "Disconnected from \(clientId)"
                    } else {
                        errorMessage = result.message ?? "Failed to disconnect"
                    }
                }
                // Reload the list to reflect changes
                loadClients()
            } catch {
                await MainActor.run {
                    actionInProgress = nil
                    errorMessage = error.localizedDescription
                }
            }
        }
    }
}
