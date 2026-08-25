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

    private var apiClient: APIClient? { appState.apiClient }

    /// One row of the list: a tool, its server, and (for a search) its rank.
    struct ToolRow: Identifiable, Equatable {
        let server: String
        let name: String
        let description: String
        let score: Double?

        /// The canonical MCP identity — what an agent would actually call.
        var qualified: String { server.isEmpty ? name : "\(server):\(name)" }
        var id: String { qualified }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            searchBar
            Divider()
            content
        }
        .task { await load() }
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
            Text(countLabel)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal)
        .padding(.top, 12)
        .padding(.bottom, 6)
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
                Spacer()
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
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(row.qualified). \(row.description)")
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
            if term.isEmpty {
                fetched = try await client.allTools().map {
                    ToolRow(server: $0.serverName ?? "", name: $0.name,
                            description: $0.description ?? "", score: nil)
                }
            } else {
                fetched = try await client.searchTools(query: term).map {
                    ToolRow(server: $0.tool.serverName ?? "", name: $0.tool.name,
                            description: $0.tool.description ?? "", score: $0.score)
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
