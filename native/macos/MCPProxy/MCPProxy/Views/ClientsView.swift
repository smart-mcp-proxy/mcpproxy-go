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
    /// Set by the toolbar "+ -> Token" hand-off; TokensView opens its create
    /// sheet and resets it.
    @State private var tokenCreateRequested = false

    // Spec 108-k
    /// Instance-level binding warnings from `GET /clients`.
    @State private var warnings: [ClientWarning] = []
    /// Scope filters of the Clients and Agent Tokens tabs (profile chips).
    @State private var clientsFilter = ScopeFilter()
    @State private var tokensFilter = ScopeFilter()
    @StateObject private var bindingModel: ClientBindingModel
    @State private var connectPreselect: String?
    @State private var showBulkMove = false
    @State private var showUpgrade = false
    @State private var showOtherClient = false
    @State private var rotating: ClientPresenceRecord?
    @State private var forgetting: ClientPresenceRecord?
    @State private var focusedClientID: String?
    /// A `.clientDetail` route that arrived before the row was in `clients`
    /// (Clients not yet loaded when Home or the tray routed here). Expanded as
    /// soon as the row appears so the profile picker is visible (109-l F4.1).
    @State private var pendingExpandID: String?

    init(appState: AppState) {
        self.appState = appState
        _bindingModel = StateObject(wrappedValue: ClientBindingModel(source: appState.deferredClientSource))
    }

    var body: some View {
        VStack(spacing: 0) {
            Picker("Clients section", selection: $tab) {
                Text("Clients").tag(0)
                Text("Endpoint & Mode").tag(1)
                Text("Agent Tokens").tag(2)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding()
            Divider()
            if tab == 0 {
                clientsPane
            } else if tab == 1 {
                endpointAndModePane
            } else {
                TokensView(appState: appState, requestCreate: $tokenCreateRequested, filter: $tokensFilter)
            }
        }
        .accessibilityIdentifier("clients-view")
        .task { await load() }
        // Spec 109-i FR-052: toolbar "+ -> Client / Token" hand-off (see
        // AppState.pendingAddAction). Only Client and Token are this view's.
        .onAppear { consumePendingAddAction(); consumeRoute() }
        .onChange(of: appState.pendingAddAction) { _ in consumePendingAddAction() }
        .onChange(of: appState.pendingRoute) { _ in consumeRoute() }
        .onChange(of: appState.clients) { fresh in adopt(fresh) }
        .onChange(of: clientsFilter) { _ in Task { await load() } }
        .sheet(isPresented: $showConnect, onDismiss: {
            Task { await load() }
            connectPreselect = nil
        }) {
            ConnectClientSheetHost(
                appState: appState,
                preselect: connectPreselect,
                presetProfile: connectPreselect.flatMap { id in
                    (clients.first { $0.id == id } ?? appState.clients.first { $0.id == id })?.boundProfile
                },
                onClose: { showConnect = false },
                onRoute: { route in
                    showConnect = false
                    appState.navigate(route)
                }
            )
                .frame(minWidth: 780, minHeight: 560)
        }
        .sheet(isPresented: $showBulkMove) {
            BulkMoveSheet(appState: appState) { Task { await load() } }
        }
        .sheet(isPresented: $showUpgrade) {
            AdminKeyUpgradeSheet(appState: appState) { Task { await load() } }
        }
        .sheet(isPresented: $showOtherClient) {
            OtherClientSheet(appState: appState) { Task { await load() } }
        }
        .sheet(item: $rotating) { client in
            RotateClientSheet(appState: appState, client: client) { Task { await load() } }
        }
        .sheet(item: $forgetting) { client in
            ForgetClientSheet(client: client, model: bindingModel) { Task { await load() } }
        }
    }

    /// Routes this hub owns (Spec 108-k): a tab with its filter, a row's detail,
    /// the connect sheet on a client, and the admin-key upgrade sheet.
    private func consumeRoute() {
        enum Landing { case tab(ClientsTab, ScopeFilter?), detail(String), connect(String?), upgrade }
        let landing: Landing? = appState.consumeRoute { route in
            switch route {
            case .clients(let tab, let filter): return .tab(tab, filter)
            case .clientDetail(let id): return .detail(id)
            case .connectSheet(let id): return .connect(id)
            case .upgradeAdminKeys: return .upgrade
            default: return nil
            }
        }
        switch landing {
        case .tab(let landedTab, let filter)?:
            tab = landedTab.rawValue
            if landedTab == .tokens { tokensFilter = filter ?? ScopeFilter() }
            else { clientsFilter = filter ?? ScopeFilter() }
        case .detail(let id)?:
            tab = 0
            focusedClientID = id
            pendingExpandID = id
            expandPendingClient(dropIfAbsent: false)
        case .connect(let id)?:
            tab = 0
            connectPreselect = id
            showConnect = true
        case .upgrade?:
            tab = 0
            showUpgrade = true
        case nil:
            break
        }
    }

    /// Expand the row a `.clientDetail` route asked for, once it is loaded.
    /// After a full load the request is dropped when the client is gone.
    private func expandPendingClient(dropIfAbsent: Bool) {
        guard let id = pendingExpandID else { return }
        if expandedClientID == id {
            pendingExpandID = nil
        } else if let row = clients.first(where: { $0.id == id }) {
            pendingExpandID = nil
            Task { await toggle(row) }
        } else if dropIfAbsent {
            pendingExpandID = nil
        }
    }

    /// Follow the app-wide client list (SSE `client.binding_changed`) without
    /// losing the sessions a row's detail loaded. A filtered view refetches.
    private func adopt(_ fresh: [ClientPresenceRecord]) {
        guard clientsFilter.profile == nil, clientsFilter.client == nil else {
            Task { await load() }
            return
        }
        let sessions = Dictionary(uniqueKeysWithValues: clients.compactMap { row in
            row.sessions.map { (row.id, $0) }
        })
        clients = fresh.map { row in
            var merged = row
            if merged.sessions == nil, let kept = sessions[row.id] { merged.sessions = kept }
            return merged
        }
        warnings = appState.clientWarnings
        expandPendingClient(dropIfAbsent: false)
    }

    private func consumePendingAddAction() {
        switch appState.consumePendingAddAction(for: [.client, .token]) {
        case .client:
            tab = 0
            showConnect = true
        case .token:
            tab = 2
            tokenCreateRequested = true
        default:
            break
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
                Menu("More") {
                    Button("Move Clients…") { showBulkMove = true }
                        .accessibilityIdentifier("clients-bulk-move")
                    if warnings.contains(where: { $0.code == ClientWarning.holdsAdminKey }) {
                        Button("Upgrade Admin-Key Clients…") { showUpgrade = true }
                            .accessibilityIdentifier("clients-upgrade-admin-key")
                    }
                    Button("Other Client…") { showOtherClient = true }
                        .accessibilityIdentifier("clients-other-client")
                }
                .menuStyle(.borderlessButton).frame(width: 70)
                .accessibilityIdentifier("clients-more-menu")
                Button("Connect client") { connectPreselect = nil; showConnect = true }
                    .buttonStyle(.borderedProminent)
            }
            .padding()

            ClientWarningsBanner(warnings: warnings) { appState.navigate($0) }
            clientsFilterChips

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
                        ClientBindingRowLabel(client: client)
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
            Divider()
            ClientBindingDetail(
                appState: appState, model: bindingModel, client: client,
                onUpdate: { updated in replace(updated) },
                onUpgrade: { connectPreselect = client.id; showConnect = true },
                onRotate: { rotating = client },
                onForget: { forgetting = client },
                onExplain: { appState.navigate(.explain(subject: .client(client.id), tool: nil)) })
        }
        .padding(10)
        .background(Color.secondary.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .overlay(
            RoundedRectangle(cornerRadius: 6)
                .stroke(Color.accentColor, lineWidth: focusedClientID == client.id ? 2 : 0))
    }

    /// Profile / client chips with a clear (✕) button: the filter a Profiles
    /// card link (or a Clients row link) set.
    @ViewBuilder
    private var clientsFilterChips: some View {
        if clientsFilter.profile != nil || clientsFilter.client != nil {
            HStack(spacing: 8) {
                if let profile = clientsFilter.profile {
                    chip("Profile: \(profile)") { clientsFilter.profile = nil }
                }
                if let client = clientsFilter.client {
                    chip("Client: \(client)") { clientsFilter.client = nil }
                }
                Spacer()
            }
            .padding(.horizontal).padding(.bottom, 6)
            .accessibilityIdentifier("clients-filter-chips")
        }
    }

    private func chip(_ text: String, clear: @escaping () -> Void) -> some View {
        HStack(spacing: 4) {
            Text(text).font(.caption)
            Button(action: clear) { Image(systemName: "xmark.circle.fill") }
                .buttonStyle(.borderless)
                .accessibilityLabel("Clear filter \(text)")
        }
        .padding(.horizontal, 8).padding(.vertical, 3)
        .background(Color.accentColor.opacity(0.15)).clipShape(Capsule())
    }

    private func replace(_ updated: ClientPresenceRecord) {
        guard let index = clients.firstIndex(where: { $0.id == updated.id }) else { return }
        var merged = updated
        if merged.sessions == nil { merged.sessions = clients[index].sessions }
        clients[index] = merged
        if let shared = appState.clients.firstIndex(where: { $0.id == updated.id }) {
            appState.clients[shared] = updated
        }
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
            // The scope parameters ride only when the core advertises them.
            let request = clientsFilter.restRequest(for: .clients, scopeFiltersAvailable: appState.scopeFiltersAvailable)
            let profile = request?.query.first { $0.name == "profile" }?.value
            let client = request?.query.first { $0.name == "client" }?.value
            async let loadedClients = apiClient.clientsV3(profile: profile, client: client)
            async let loadedRouting = apiClient.routing()
            let response = try await loadedClients
            clients = response.clients
            warnings = response.warnings ?? []
            expandPendingClient(dropIfAbsent: true)
            if profile == nil && client == nil {
                appState.clients = response.clients
                appState.clientWarnings = warnings
            }
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

/// Owns the Connect sheet's model for the life of the sheet.
///
/// The model used to be built inline in the `.sheet` closure, which runs again
/// whenever `ClientsView` re-renders, so every `profiles.changed` /
/// `client.binding_changed` event handed the sheet a fresh model stuck on
/// "Loading clients…" (its `.task` runs once). `@StateObject` keeps one.
private struct ConnectClientSheetHost: View {
    @StateObject private var model: ConnectClientModel
    let preselect: String?
    let presetProfile: String?
    let onClose: () -> Void
    let onRoute: (AppRoute) -> Void

    init(appState: AppState, preselect: String?, presetProfile: String?,
         onClose: @escaping () -> Void, onRoute: @escaping (AppRoute) -> Void) {
        _model = StateObject(wrappedValue: ConnectClientModel(source: DeferredConnectSource {
            await MainActor.run { appState.apiClient }
        }))
        self.preselect = preselect
        self.presetProfile = presetProfile
        self.onClose = onClose
        self.onRoute = onRoute
    }

    var body: some View {
        ConnectClientView(model: model, onClose: onClose, preselect: preselect, presetProfile: presetProfile, onRoute: onRoute)
    }
}
