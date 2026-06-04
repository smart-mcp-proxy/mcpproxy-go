// ServerBrowseView.swift
// MCPProxy
//
// macOS-tray mirror of the Web UI R1 feature: a MULTISELECT registry filter
// plus cross-registry server search. The user ticks one or more registries,
// searches, and sees a merged, registry-attributed result list. Registries
// that need an API key / are unreachable are surfaced as a non-fatal notice so
// the registries that DID return still render. Each result can be added
// (servers from custom registries land quarantined server-side).

import SwiftUI

struct ServerBrowseView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    /// Registries to offer in the multiselect (loaded by the parent view).
    let registries: [Registry]

    @State private var selectedIDs: Set<String> = []
    @State private var query: String = ""
    @State private var results: [RepositoryServer] = []
    @State private var unavailable: [String] = []
    @State private var isSearching = false
    @State private var searchError: String?
    @State private var addingID: String?
    @State private var addNote: String?
    @State private var registryInfo: RegistryInfoContext?

    private var apiClient: APIClient? { appState.apiClient }

    /// Identifiable context for the registry-info popup (`.sheet(item:)`). Holds
    /// the looked-up `Registry` (nil when the result's label matched nothing in
    /// the loaded list) plus the raw `label` so the popup always has a title.
    struct RegistryInfoContext: Identifiable {
        let id = UUID()
        let registry: Registry?
        let label: String
    }

    private func registryName(_ id: String) -> String {
        registries.first { $0.id == id }?.name ?? id
    }

    private var selectionLabel: String {
        let n = selectedIDs.count
        if n == 0 { return "Choose registries…" }
        if n == 1 { return registryName(selectedIDs.first!) }
        if !registries.isEmpty, n == registries.count { return "All registries (\(n))" }
        return "\(n) registries"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            filterBar

            if !unavailable.isEmpty {
                Label("Some registries returned no results: \(unavailable.joined(separator: "; "))",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.orange)
                    .padding(.horizontal)
                    .accessibilityIdentifier("browse-unavailable-notice")
            }

            if let note = addNote {
                Text(note)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal)
                    .accessibilityIdentifier("browse-add-note")
            }

            resultsArea
        }
        .accessibilityIdentifier("server-browse")
        .sheet(item: $registryInfo) { ctx in
            registryInfoSheet(ctx)
        }
    }

    // MARK: Registry-info popup (MCP-1050)

    @ViewBuilder
    private func registryInfoSheet(_ ctx: RegistryInfoContext) -> some View {
        let displayName = ctx.registry.map { $0.name.isEmpty ? $0.id : $0.name } ?? ctx.label
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Text(displayName)
                    .font(.scaled(.title3, scale: fontScale).bold())
                if let reg = ctx.registry { provenanceBadge(reg) }
                Spacer()
                Button {
                    registryInfo = nil
                } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("registry-info-close")
            }

            if let reg = ctx.registry {
                if let desc = reg.description, !desc.isEmpty {
                    Text(desc)
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                if let url = reg.serversURL ?? reg.url, !url.isEmpty {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("URL")
                            .font(.scaled(.caption2, scale: fontScale))
                            .foregroundStyle(.secondary)
                        Text(url)
                            .font(.scaledMonospaced(.caption, scale: fontScale))
                            .foregroundStyle(.tertiary)
                            .textSelection(.enabled)
                            .lineLimit(2)
                            .truncationMode(.middle)
                    }
                }

                if reg.isCustom {
                    Text("Servers added from this third-party registry are always quarantined and cannot skip security review.")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("registry-info-quarantine-note")
                }
            } else {
                Text("No additional details are available for this registry.")
                    .font(.scaled(.subheadline, scale: fontScale))
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)
        }
        .padding()
        .frame(width: 420, height: 220)
        .accessibilityIdentifier("registry-info-popup")
    }

    @ViewBuilder
    private func provenanceBadge(_ registry: Registry) -> some View {
        if registry.isCustom {
            badge(text: "Third-party \u{00B7} unverified", tint: .orange)
                .accessibilityIdentifier("registry-info-badge-custom")
        } else {
            badge(text: "Official \u{00B7} trusted", tint: .green)
                .accessibilityIdentifier("registry-info-badge-official")
        }
    }

    @ViewBuilder
    private func badge(text: String, tint: Color) -> some View {
        Text(text)
            .font(.scaled(.caption2, scale: fontScale).weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(tint.opacity(0.18))
            .foregroundStyle(tint)
            .clipShape(Capsule())
    }

    // MARK: Filter bar (multiselect + search)

    @ViewBuilder
    private var filterBar: some View {
        HStack(spacing: 8) {
            Menu {
                if registries.count > 1 {
                    Button("Select all") { selectedIDs = Set(registries.map(\.id)) }
                    Button("Clear") { selectedIDs.removeAll() }
                    Divider()
                }
                ForEach(registries) { registry in
                    Button {
                        toggle(registry.id)
                    } label: {
                        Label(
                            registry.name + (registry.isCustom ? " — unverified" : ""),
                            systemImage: selectedIDs.contains(registry.id) ? "checkmark.square.fill" : "square"
                        )
                    }
                    .accessibilityIdentifier("browse-registry-option-\(registry.id)")
                }
            } label: {
                Label(selectionLabel, systemImage: "line.3.horizontal.decrease.circle")
            }
            .frame(minWidth: 160)
            .accessibilityIdentifier("browse-registry-multiselect")

            TextField("Search servers by name or description…", text: $query)
                .textFieldStyle(.roundedBorder)
                .onSubmit { Task { await search() } }
                .accessibilityIdentifier("browse-search-input")

            Button {
                Task { await search() }
            } label: {
                if isSearching { ProgressView().controlSize(.small) } else { Text("Search") }
            }
            .keyboardShortcut(.defaultAction)
            .disabled(selectedIDs.isEmpty || isSearching || apiClient == nil)
            .accessibilityIdentifier("browse-search-button")
        }
        .padding(.horizontal)
        .padding(.top, 8)
    }

    private func toggle(_ id: String) {
        if selectedIDs.contains(id) { selectedIDs.remove(id) } else { selectedIDs.insert(id) }
    }

    // MARK: Results

    @ViewBuilder
    private var resultsArea: some View {
        if isSearching {
            VStack { Spacer(); ProgressView("Searching…"); Spacer() }
                .frame(maxWidth: .infinity, minHeight: 120)
        } else if let err = searchError {
            Label(err, systemImage: "xmark.octagon.fill")
                .foregroundStyle(.red)
                .font(.scaled(.callout, scale: fontScale))
                .padding()
                .accessibilityIdentifier("browse-error")
        } else if results.isEmpty {
            Text(selectedIDs.isEmpty ? "Pick one or more registries, then search."
                                     : "No servers found. Try a different search.")
                .foregroundStyle(.secondary)
                .font(.scaled(.callout, scale: fontScale))
                .padding(.horizontal)
                .accessibilityIdentifier("browse-empty")
        } else {
            HStack {
                Text("Found \(results.count) server(s)" + (selectedIDs.count > 1 ? " across \(selectedIDs.count) registries" : ""))
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal)
            .accessibilityIdentifier("browse-results-count")

            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(results) { server in
                        serverCard(server)
                    }
                }
                .padding(.horizontal)
                .padding(.bottom, 8)
            }
        }
    }

    @ViewBuilder
    private func serverCard(_ server: RepositoryServer) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .top) {
                Text(server.name)
                    .font(.scaled(.headline, scale: fontScale))
                    .fixedSize(horizontal: false, vertical: true)
                Spacer()
                if let reg = server.registry, !reg.isEmpty {
                    // Tappable: opens an info popup for the originating registry
                    // (name/url/description/provenance) — MCP-1050.
                    Button {
                        registryInfo = RegistryInfoContext(
                            registry: Registry.lookup(reg, in: registries), label: reg)
                    } label: {
                        HStack(spacing: 3) {
                            Text(reg)
                            Image(systemName: "info.circle")
                                .font(.system(size: 9 * fontScale))
                        }
                        .font(.scaled(.caption2, scale: fontScale))
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Color.secondary.opacity(0.15))
                        .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .help("Show registry info")
                    .accessibilityIdentifier("browse-source-\(server.id)")
                }
            }
            if let desc = server.description, !desc.isEmpty {
                Text(desc).font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary).lineLimit(3)
            }
            HStack(spacing: 6) {
                Text(server.transport)
                    .font(.system(.caption2, design: .monospaced))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .overlay(Capsule().stroke(Color.secondary.opacity(0.4)))
                    .accessibilityIdentifier("browse-transport-\(server.id)")
                if let inputs = server.requiredInputs, !inputs.isEmpty {
                    Text("requires input")
                        .font(.caption2)
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .overlay(Capsule().stroke(Color.secondary.opacity(0.4)))
                }
                Spacer()
                Button {
                    Task { await add(server) }
                } label: {
                    if addingID == server.id { ProgressView().controlSize(.small) } else { Text("Add to MCP") }
                }
                .controlSize(.small)
                .disabled(addingID != nil || server.registry == nil)
                .accessibilityIdentifier("browse-add-\(server.id)")
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityIdentifier("browse-server-\(server.id)")
    }

    // MARK: Actions

    /// Cross-registry search: fan out to every selected registry (sequentially —
    /// a handful of registries, and it keeps us clear of Sendable concerns),
    /// merge + dedupe results, and collect per-registry failures as a non-fatal
    /// notice. A hard error is raised only when every selected registry fails.
    private func search() async {
        let ids = Array(selectedIDs)
        guard !ids.isEmpty, let client = apiClient else { return }
        let q = query
        isSearching = true
        searchError = nil
        unavailable = []
        addNote = nil

        var merged: [RepositoryServer] = []
        var seen = Set<String>()
        var failures: [String] = []

        for id in ids {
            do {
                let resp = try await client.searchRegistryServers(registryID: id, query: q)
                if let u = resp.unavailable {
                    failures.append("\(registryName(id)): \(u.reason ?? "unavailable")")
                }
                for s in resp.servers ?? [] {
                    let key = "\(s.registry ?? id)::\(s.id)"
                    if seen.contains(key) { continue }
                    seen.insert(key)
                    merged.append(s)
                }
            } catch {
                failures.append("\(registryName(id)): \(error.localizedDescription)")
            }
        }

        results = merged
        unavailable = failures
        if merged.isEmpty, !failures.isEmpty, failures.count == ids.count {
            searchError = "No results — " + failures.joined(separator: "; ")
        }
        isSearching = false
    }

    private func add(_ server: RepositoryServer) async {
        guard let client = apiClient, let reg = server.registry else { return }
        addingID = server.id
        addNote = nil
        let result = await client.addServerFromRegistry(registryID: reg, serverID: server.id)
        if result.success {
            addNote = "Added “\(server.name)”. New servers start quarantined — review under Servers."
        } else if let missing = result.missingInputs, !missing.isEmpty {
            addNote = "“\(server.name)” needs input: \(missing.joined(separator: ", ")). Add it from the Web UI to supply those values."
        } else {
            addNote = "Couldn’t add “\(server.name)”: \(result.message ?? "unknown error")"
        }
        addingID = nil
    }
}
