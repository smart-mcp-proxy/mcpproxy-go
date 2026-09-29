import AppKit
import SwiftUI

/// The native Clients hub keeps connection, endpoint terminology, and agent
/// tokens together. ConnectClientView remains the single author of the
/// preview-before-write workflow.
struct ClientsView: View {
    @ObservedObject var appState: AppState
    @State private var tab = 0
    @State private var clients: [ClientPresenceRecord] = []
    @State private var routing: RoutingInfo?
    @State private var expandedClientID: String?
    @State private var isLoading = false
    @State private var isSavingMode = false
    @State private var errorMessage: String?
    @State private var showConnect = false
    @State private var defaultMCPEndpoint = "http://127.0.0.1:8080/mcp"
    @State private var snippetCopied = false

    var body: some View {
        VStack(spacing: 0) {
            Picker("Clients section", selection: $tab) {
                Text("Clients").tag(0)
                Text("Endpoint & Mode").tag(1)
                Text("Agent Tokens").tag(2)
            }
            .pickerStyle(.segmented)
            .padding()
            Divider()
            if tab == 0 {
                clientsPane
            } else if tab == 1 {
                endpointAndModePane
            } else {
                TokensView(appState: appState)
            }
        }
        .accessibilityIdentifier("clients-view")
        .task { await load() }
        .sheet(isPresented: $showConnect) {
            let state = appState
            ConnectClientView(model: ConnectClientModel(source: DeferredConnectSource {
                await MainActor.run { state.apiClient }
            }))
                .frame(minWidth: 780, minHeight: 560)
        }
    }

    // MARK: - Clients

    @ViewBuilder
    private var clientsPane: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("Clients").font(.title2.bold())
                    Text("Connect AI clients and check their recent MCP activity.")
                        .font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer()
                if isLoading { ProgressView().controlSize(.small) }
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .buttonStyle(.borderless).help("Refresh clients")
                Button("Connect client") { showConnect = true }
                    .buttonStyle(.borderedProminent)
            }
            .padding()

            if let errorMessage {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(errorMessage).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button("Dismiss") { self.errorMessage = nil }.buttonStyle(.borderless)
                }
                .padding(.horizontal).padding(.vertical, 8)
                .background(Color.orange.opacity(0.1))
            }

            if clients.isEmpty && !isLoading && errorMessage == nil {
                emptyState(
                    title: "No clients found",
                    message: "Connect an AI client to add MCPProxy to its configuration.",
                    symbol: "person.2"
                )
            } else {
                List(clients) { client in
                    clientRow(client)
                }
                .listStyle(.inset)
            }

            VStack(alignment: .leading, spacing: 8) {
                Text("Other client?").font(.headline)
                Text("Add MCPProxy to its MCP configuration with this endpoint. This example contains no admin key.")
                    .font(.caption).foregroundStyle(.secondary)
                Text(otherClientSnippet)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.secondary.opacity(0.08))
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                Button(snippetCopied ? "Copied" : "Copy config") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(otherClientSnippet, forType: .string)
                    snippetCopied = true
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("copy-other-client-snippet")
            }
            .padding()
        }
    }

    private var otherClientSnippet: String {
        let object: [String: Any] = ["mcpServers": ["mcpproxy": ["url": defaultMCPEndpoint]]]
        guard let data = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys]),
              let text = String(data: data, encoding: .utf8) else { return "" }
        return text
    }

    @ViewBuilder
    private func clientRow(_ client: ClientPresenceRecord) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                Task { await toggle(client) }
            } label: {
                HStack(spacing: 10) {
                    Image(systemName: client.symbolName).frame(width: 20)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(client.displayName).font(.headline)
                        Text(client.stateLabel).font(.caption).foregroundStyle(client.connected ? .green : .secondary)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 2) {
                        Text("\(client.activeSessions) active session\(client.activeSessions == 1 ? "" : "s")")
                            .font(.caption)
                        Text("\(client.calls24h) calls in 24h").font(.caption2).foregroundStyle(.secondary)
                    }
                    Image(systemName: expandedClientID == client.id ? "chevron.up" : "chevron.down")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("client-row-\(client.id)")

            if expandedClientID == client.id {
                clientDetail(client)
            }
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private func clientDetail(_ client: ClientPresenceRecord) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if let path = client.effectiveDisplayPath, !path.isEmpty {
                Label(path, systemImage: "doc.text").font(.caption).textSelection(.enabled)
            }
            Text("Last seen: \(formattedTimestamp(client.lastSeen))")
                .font(.caption).foregroundStyle(.secondary)
            if let reloadHint = client.reloadHint, !reloadHint.isEmpty {
                Label(reloadHint, systemImage: "arrow.clockwise").font(.caption)
            }
            let sessions = client.sessions ?? []
            if sessions.isEmpty {
                Text("No recent sessions recorded.").font(.caption).foregroundStyle(.secondary)
            } else {
                Text("Recent sessions").font(.caption.weight(.semibold))
                ForEach(sessions.prefix(5)) { session in
                    HStack {
                        Image(systemName: "rectangle.connected.to.line.below")
                        Text(session.displayID).font(.caption.monospaced())
                        Spacer()
                        Text(formattedTimestamp(session.lastActivity)).font(.caption2).foregroundStyle(.secondary)
                    }
                }
            }
            if appState.scopeFiltersAvailable {
                Button("Show activity") { appState.openActivity(with: .forClient(client.id)) }
                    .buttonStyle(.link).font(.caption)
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    // MARK: - Endpoint & mode

    @ViewBuilder
    private var endpointAndModePane: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("Endpoint & mode").font(.title2.bold())
                    Text("Choose the MCP surface this instance serves. A mode change applies after restart.")
                        .font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer()
                if isLoading || isSavingMode { ProgressView().controlSize(.small) }
                Button { Task { await loadRouting() } } label: { Image(systemName: "arrow.clockwise") }
                    .buttonStyle(.borderless).help("Refresh endpoint and mode")
            }

            if let routing {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Routing mode").font(.headline)
                    Picker("Routing mode", selection: modeBinding(for: routing)) {
                        ForEach(routing.availableModes, id: \.self) { mode in
                            Text(RoutingInfo.modeLabel(mode)).tag(mode)
                                .disabled(mode == "code_execution" && routing.codeExecutionEnabled == false)
                        }
                    }
                    .pickerStyle(.radioGroup)
                    .disabled(isSavingMode)
                    Text(routing.description).font(.caption).foregroundStyle(.secondary)
                    if routing.restartRequired == true, let pending = routing.pendingRoutingMode, !pending.isEmpty {
                        Label("Restart MCPProxy to apply \(RoutingInfo.modeLabel(pending)) mode.", systemImage: "arrow.clockwise")
                            .font(.caption).foregroundStyle(.orange)
                    }
                }

                Divider()
                Text("MCP endpoints").font(.headline)
                Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 8) {
                    ForEach(routing.endpoints.rows, id: \.name) { endpoint in
                        GridRow {
                            Text(endpoint.name).font(.caption.weight(.medium))
                            Text(endpoint.path).font(.caption.monospaced()).textSelection(.enabled)
                        }
                    }
                }
            } else if !isLoading {
                emptyState(
                    title: "Endpoint information unavailable",
                    message: "Connect to MCPProxy core, then refresh this page.",
                    symbol: "point.3.connected.trianglepath"
                )
            }
            Spacer()
        }
        .padding()
    }

    private func modeBinding(for routing: RoutingInfo) -> Binding<String> {
        Binding(
            get: { routing.pendingRoutingMode?.isEmpty == false ? routing.pendingRoutingMode! : routing.routingMode },
            set: { proposed in Task { await applyRoutingMode(proposed) } }
        )
    }

    @ViewBuilder
    private func emptyState(title: String, message: String, symbol: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: symbol).font(.system(size: 36)).foregroundStyle(.tertiary)
            Text(title).font(.headline)
            Text(message).font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding()
    }

    // MARK: - Loading and actions

    private func load() async {
        guard let apiClient = appState.apiClient else { return }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            async let loadedClients = apiClient.clients()
            async let loadedRouting = apiClient.routing()
            clients = try await loadedClients
            let loaded = try await loadedRouting
            routing = loaded
            defaultMCPEndpoint = APIClient.endpointURL(
                loaded.endpoints.default,
                baseURL: appState.webUIBaseURL
            )
        } catch {
            errorMessage = "Unable to load clients: \(error.localizedDescription)"
        }
    }

    private func loadRouting() async {
        guard let apiClient = appState.apiClient else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            routing = try await apiClient.routing()
        } catch {
            errorMessage = "Unable to load endpoint and mode: \(error.localizedDescription)"
        }
    }

    private func toggle(_ client: ClientPresenceRecord) async {
        guard expandedClientID != client.id else {
            expandedClientID = nil
            return
        }
        expandedClientID = client.id
        guard let apiClient = appState.apiClient else { return }
        do {
            let detail = try await apiClient.clientPresence(client.id)
            guard let index = clients.firstIndex(where: { $0.id == detail.id }) else { return }
            clients[index] = detail
        } catch {
            errorMessage = "Unable to load \(client.displayName): \(error.localizedDescription)"
        }
    }

    private func applyRoutingMode(_ mode: String) async {
        let desiredMode = routing?.pendingRoutingMode?.isEmpty == false
            ? routing?.pendingRoutingMode
            : routing?.routingMode
        guard let apiClient = appState.apiClient, desiredMode != mode else { return }
        isSavingMode = true
        errorMessage = nil
        defer { isSavingMode = false }
        do {
            _ = try await apiClient.patchConfig(["routing_mode": mode])
            routing = try await apiClient.routing()
        } catch {
            errorMessage = "Unable to change routing mode: \(error.localizedDescription)"
        }
    }

    private func formattedTimestamp(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "Never" }
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: value) else { return value }
        return date.formatted(date: .abbreviated, time: .shortened)
    }
}
