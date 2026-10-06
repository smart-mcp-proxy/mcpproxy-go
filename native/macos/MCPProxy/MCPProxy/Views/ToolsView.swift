// ToolsView.swift
// MCPProxy
//
// F16 (2026-08 tray UX audit): BM25 tool discovery is the product's headline
// feature and the tray had no native equivalent of the Web UI's /search and
// /tools — per-server tools were visible only inside Server Detail, so a
// tray-first user could not answer "which of my 942 tools does X?" without
// opening a browser.
//
// Empty query  -> the whole catalogue (GET /api/v1/tools), grouped by server.
// Typed query  -> BM25 ranking (GET /api/v1/index/search), best match first.
//
// Both REST surfaces return the BARE tool name with `server_name` alongside
// (#871); `server:tool` is assembled here, never taken from `name`.

import SwiftUI

struct ToolsView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    @State private var query = ""
    @State private var rows: [ToolRow] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    /// Only the newest in-flight load may publish, so a slow catalogue fetch
    /// cannot overwrite the search results the user just asked for.
    @State private var loadGeneration = 0
    @State private var selectedID: String?
    /// Spec 108-k K18: "View as" — the catalogue as a client or a profile sees it.
    @State private var viewAs: ToolsViewAs = .everything
    @State private var viewAsCounts: ViewAsCounts?

    private var apiClient: APIClient? { appState.apiClient }

    /// Whose view of the catalogue is shown. Everything (admin) is the default.
    enum ToolsViewAs: Equatable {
        case everything
        case client(String)
        case profile(String)

        var isScoped: Bool { self != .everything }

        /// The explainer subject for a "Why?" on a row.
        var explainerSubject: ExplainerSubject? {
            switch self {
            case .everything: return nil
            case .client(let id): return .client(id)
            case .profile(let name): return .profile(name)
            }
        }
    }

    /// One row of the list: a tool, its server, and (for a search) its rank.
    struct ToolRow: Identifiable, Equatable {
        let server: String
        let name: String
        let description: String
        let score: Double?
        /// Spec 109 FR-028: server-computed tier (`read`|`write`|`destructive`
        /// |`unannotated`), never derived locally. `nil` for an older core
        /// that does not yet send it.
        let tier: String?
        /// View-as only (Spec 108 FR-032): the verdict for the viewed subject and
        /// the tool's tier under its profile.
        var access: ToolAccess?
        var profileTier: String?

        // Explicit initializer (rather than relying on the synthesized
        // memberwise one) so every existing call site that predates `tier`
        // keeps compiling unchanged.
        init(server: String, name: String, description: String, score: Double?, tier: String? = nil,
             access: ToolAccess? = nil, profileTier: String? = nil) {
            self.server = server
            self.name = name
            self.description = description
            self.score = score
            self.tier = tier
            self.access = access
            self.profileTier = profileTier
        }

        /// A row the viewed subject cannot use: greyed, with the reason beside it.
        var isBlockedForSubject: Bool { access.map { !$0.callable } ?? false }

        /// The canonical MCP identity — what an agent would actually call.
        var qualified: String { server.isEmpty ? name : "\(server):\(name)" }
        var id: String { qualified }

        /// `tier`, defaulting to "unannotated" (review round 1): a `nil` tier
        /// from an older core must still render a badge, same as the Web UI's
        /// `tool.tier || 'unannotated'` — never no badge at all.
        var displayTier: String { tier ?? "unannotated" }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            searchBar
            Divider()
            content
        }
        .task { await load() }
        .onAppear { consumeRoute() }
        .onChange(of: appState.pendingRoute) { _ in consumeRoute() }
        .onChange(of: viewAs) { _ in Task { await load() } }
        .onChange(of: appState.totalTools) { _ in
            // The index rebuilt (a server connected, tools re-discovered).
            if query.isEmpty { Task { await load() } }
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack {
            Text("Tools")
                .font(.scaled(.title2, scale: fontScale).bold())
            Spacer()
            if isLoading { ProgressView().controlSize(.small) }
            if appState.scopeFiltersAvailable { viewAsControls }
            Text(countLabel)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal)
        .padding(.top, 12)
        .padding(.bottom, 6)
    }

    /// "View as: Everything (admin) / Client… / Profile…" and its value picker.
    @ViewBuilder
    private var viewAsControls: some View {
        Picker("View as", selection: Binding(
            get: { kind(of: viewAs) },
            set: { newKind in
                switch newKind {
                case "client": viewAs = .client(appState.clients.first?.id ?? "")
                case "profile": viewAs = .profile(appState.profiles.first?.name ?? "")
                default: viewAs = .everything
                }
            })) {
            Text("View as: Everything (admin)").tag("everything")
            Text("View as: Client…").tag("client")
            Text("View as: Profile…").tag("profile")
        }
        .frame(maxWidth: 230)
        .accessibilityIdentifier("tools-view-as")

        switch viewAs {
        case .client(let current):
            Picker("Client", selection: Binding(get: { current }, set: { viewAs = .client($0) })) {
                ForEach(appState.clients, id: \.id) { Text($0.displayName).tag($0.id) }
                if !appState.clients.contains(where: { $0.id == current }) { Text(current).tag(current) }
            }
            .labelsHidden().frame(maxWidth: 160)
            .accessibilityIdentifier("tools-view-as-client")
        case .profile(let current):
            Picker("Profile", selection: Binding(get: { current }, set: { viewAs = .profile($0) })) {
                ForEach(appState.profiles) { Text($0.pickerTitle(in: appState.profiles)).tag($0.name) }
                if !appState.profiles.contains(where: { $0.name == current }) { Text(current).tag(current) }
            }
            .labelsHidden().frame(maxWidth: 160)
            .accessibilityIdentifier("tools-view-as-profile")
        case .everything:
            EmptyView()
        }
    }

    private func kind(of viewAs: ToolsViewAs) -> String {
        switch viewAs {
        case .everything: return "everything"
        case .client: return "client"
        case .profile: return "profile"
        }
    }

    /// A Profiles-card or Clients link lands here with a profile or client filter.
    private func consumeRoute() {
        let filter: ScopeFilter? = appState.consumeRoute { route in
            if case .tools(let filter) = route { return filter ?? ScopeFilter() }
            return nil
        }
        guard let filter else { return }
        if let client = filter.client, !client.isEmpty {
            viewAs = .client(client)
        } else if let profile = filter.profile, !profile.isEmpty {
            viewAs = .profile(profile)
        }
    }

    private var countLabel: String {
        if rows.isEmpty { return "" }
        let noun = rows.count == 1 ? "tool" : "tools"
        return query.isEmpty
            ? "\(rows.count) \(noun)"
            : "\(rows.count) \(noun) matched"
    }

    private var searchBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Search all tools — the same BM25 ranking agents get", text: $query)
                .textFieldStyle(.plain)
                .accessibilityIdentifier("tools-search-field")
                .onSubmit { Task { await load() } }
                .onChange(of: query) { _ in Task { await load() } }
            if !query.isEmpty {
                Button {
                    query = ""
                } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("Clear search")
            }
        }
        .padding(.horizontal)
        .padding(.bottom, 10)
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        if let errorMessage {
            VStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle").font(.title2).foregroundStyle(.orange)
                Text("Couldn’t load tools").font(.scaled(.headline, scale: fontScale))
                Text(errorMessage).font(.scaled(.caption, scale: fontScale)).foregroundStyle(.secondary)
                Button("Retry") { Task { await load() } }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if rows.isEmpty && !isLoading {
            VStack(spacing: 8) {
                Image(systemName: "wrench.and.screwdriver").font(.title2).foregroundStyle(.secondary)
                Text(query.isEmpty ? "No tools indexed yet" : "No tool matches “\(query)”")
                    .font(.scaled(.headline, scale: fontScale))
                Text(query.isEmpty
                     ? "Connect a server and its tools appear here."
                     : "Try fewer or more general words — this is the same search agents run.")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            List(rows, selection: $selectedID) { row in
                toolRow(row)
                    .contextMenu {
                        Button("Copy Tool Name") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(row.qualified, forType: .string)
                        }
                        Button("Open \(row.server)") { openServer(row.server) }
                            .disabled(row.server.isEmpty)
                    }
            }
            .listStyle(.inset)
            .accessibilityIdentifier("tools-list")
        }
    }

    private func toolRow(_ row: ToolRow) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                Text(row.name)
                    .font(.scaled(.body, scale: fontScale).weight(.medium))
                    .textSelection(.enabled)
                Text(row.server)
                    .font(.scaled(.caption2, scale: fontScale))
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(Color.accentColor.opacity(0.15))
                    .clipShape(Capsule())
                Text(ToolLabels.tierLabel(row.displayTier))
                    .font(.scaled(.caption2, scale: fontScale))
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(tierColor(row.displayTier).opacity(0.15))
                    .foregroundStyle(tierColor(row.displayTier))
                    .clipShape(Capsule())
                if let profileTier = row.profileTier, profileTier != row.tier {
                    Text("as \(ToolLabels.tierLabel(profileTier))")
                        .font(.scaled(.caption2, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let access = row.access {
                    // Text beside the greying: colour is never the only signal.
                    Text(access.callable ? "Callable" : AccessReasonText.label(access.reason))
                        .font(.scaled(.caption2, scale: fontScale))
                        .foregroundStyle(access.callable ? Color.green : Color.orange)
                    if let subject = viewAs.explainerSubject {
                        Button("Why?") {
                            appState.navigate(.explain(subject: subject, tool: row.qualified))
                        }
                        .buttonStyle(.link).font(.scaled(.caption2, scale: fontScale))
                        .accessibilityLabel("Why is \(row.qualified) \(access.callable ? "allowed" : "blocked")?")
                        .accessibilityIdentifier("tools-why-\(row.qualified)")
                    }
                }
                if let score = row.score {
                    Text(String(format: "%.2f", score))
                        .font(.scaled(.caption2, scale: fontScale).monospacedDigit())
                        .foregroundStyle(.secondary)
                        .help("BM25 relevance score")
                }
            }
            if !row.description.isEmpty {
                Text(row.description)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 3)
        .opacity(row.isBlockedForSubject ? 0.55 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(
            "\(row.qualified). \(row.description)"
            + (row.access.map { $0.callable ? ". Callable" : ". \(AccessReasonText.label($0.reason))" } ?? ""))
    }

    private func tierColor(_ tier: String) -> Color {
        switch tier {
        case "destructive": return .red
        case "write": return .orange
        case "read": return .green
        default: return .secondary
        }
    }

    private func openServer(_ server: String) {
        guard !server.isEmpty else { return }
        NotificationCenter.default.post(name: .switchToServers, object: nil)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            NotificationCenter.default.post(name: .showServerDetail, object: server)
        }
    }

    // MARK: - Loading

    private func load() async {
        guard let client = apiClient else {
            rows = []
            errorMessage = "MCPProxy is not connected to a running core."
            return
        }
        loadGeneration += 1
        let generation = loadGeneration
        let term = query.trimmingCharacters(in: .whitespacesAndNewlines)
        isLoading = true
        defer { if generation == loadGeneration { isLoading = false } }

        do {
            let fetched: [ToolRow]
            if viewAs.isScoped {
                // View-as: the catalogue with each row's verdict for the viewed
                // subject. The search index has no view-as, so a typed query
                // filters these rows by name instead of BM25 ranking.
                let response: SearchToolsResponse
                switch viewAs {
                case .client(let id): response = try await client.viewAsTools(client: id)
                case .profile(let name): response = try await client.viewAsTools(profile: name)
                case .everything: response = SearchToolsResponse(query: nil, results: nil, tools: [], total: nil)
                }
                let typed = term.lowercased()
                fetched = (response.tools ?? []).map {
                    ToolRow(server: $0.serverName ?? "", name: $0.name,
                            description: $0.description ?? "", score: nil, tier: $0.tier,
                            access: $0.access, profileTier: $0.profileTier)
                }
                .filter { typed.isEmpty || $0.qualified.lowercased().contains(typed) || $0.description.lowercased().contains(typed) }
                guard generation == loadGeneration else { return }
                viewAsCounts = response.counts
            } else if term.isEmpty {
                viewAsCounts = nil
                fetched = try await client.allTools().map {
                    ToolRow(server: $0.serverName ?? "", name: $0.name,
                            description: $0.description ?? "", score: nil, tier: $0.tier)
                }
            } else {
                viewAsCounts = nil
                fetched = try await client.searchTools(query: term).map {
                    ToolRow(server: $0.tool.serverName ?? "", name: $0.tool.name,
                            description: $0.tool.description ?? "", score: $0.score, tier: $0.tool.tier)
                }
            }
            guard generation == loadGeneration else { return }
            rows = fetched
            errorMessage = nil
        } catch {
            guard generation == loadGeneration else { return }
            rows = []
            errorMessage = error.localizedDescription
        }
    }
}
