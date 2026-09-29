// CatalogView.swift
// MCPProxy
//
// The Add Server sheet's Catalog tab (Spec 109 FR-060/061/063/065): a
// source-agnostic, ranked search over every enabled catalog source
// (`GET /api/v1/catalog/search`), superseding the old per-registry
// ServerBrowseView / RegistriesView "Discover servers" tab the same way the
// Web UI's `views/AddServer.vue` + `CatalogSearch.vue` superseded
// `views/Repositories.vue`. Registry SOURCE management (add/edit/remove a
// registry) moved to Settings → Catalog Sources.

import SwiftUI

struct CatalogView: View {
    @ObservedObject var appState: AppState
    /// Narrows results to one catalog source id (mirrors the Web UI's
    /// `?source=` query param, which narrows the Catalog tab only — it never
    /// selects a tab, FR-062). Nil searches every enabled source.
    var sourceFilter: String?

    @Environment(\.fontScale) var fontScale

    @State private var query = ""
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var results: [CatalogResult] = []
    @State private var sections: CatalogSections?
    @State private var unavailable: [CatalogSourceError] = []
    /// "<source>-<id>" -> the name the backend actually assigned (Spec 109
    /// FR-063: once added this sheet-visit, the button flips to "Added ✓ ·
    /// Open" and stays that way).
    @State private var addedNames: [String: String] = [:]
    @State private var addingKey: String?
    @State private var searchTask: Task<Void, Never>?

    // Secrets-prompt sheet state (FR-065).
    @State private var pendingResult: CatalogResult?
    @State private var pendingFields: [SecretFieldInput] = []
    @State private var addError: String?
    @State private var confirming = false
    @State private var keyringAvailable = false
    @State private var keyringReason = "Checking OS keyring availability…"
    let onOpenServer: (ServerStatus) -> Void

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            TextField("Search the catalog (e.g. 'github', 'filesystem')…", text: $query)
                .textFieldStyle(.roundedBorder)
                .padding(.horizontal)
                .padding(.top, 8)
                .padding(.bottom, 8)
                .onChange(of: query) { _ in scheduleSearch() }
                .accessibilityIdentifier("catalog-search-input")

            if !unavailable.isEmpty {
                Text(unavailable.map { "\($0.source) (\($0.reason))" }.joined(separator: ", ") + " unavailable")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.orange)
                    .padding(.horizontal)
                    .padding(.bottom, 6)
                    .accessibilityIdentifier("catalog-unavailable-notice")
            }

            // Navigation failures can happen without opening the secrets
            // sheet (for a prior-session Added/Open card), or after the user
            // dismisses it. Keep the actionable error in the catalog itself
            // whenever the sheet is not currently presenting it.
            if let addError, pendingResult == nil {
                Text(addError)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.red)
                    .padding(.horizontal)
                    .padding(.bottom, 6)
                    .accessibilityIdentifier("catalog-add-error")
            }

            content
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .accessibilityIdentifier("catalog-view")
        .task {
            await loadKeyringAvailability()
            await search()
        }
        .sheet(item: $pendingResult) { result in
            secretsSheet(result)
        }
    }

    @ViewBuilder
    private var content: some View {
        if isLoading {
            VStack { Spacer(); ProgressView("Searching the catalog…"); Spacer() }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let err = errorMessage {
            Text(err)
                .font(.scaled(.callout, scale: fontScale))
                .foregroundStyle(.red)
                .padding()
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                .accessibilityIdentifier("catalog-error")
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    if let sections {
                        sectionBlock(title: "Official", items: sections.official, testID: "catalog-section-official")
                        sectionBlock(title: "Popular", items: sections.popular, testID: "catalog-section-popular")
                        if sections.official.isEmpty && sections.popular.isEmpty {
                            Text("Nothing to browse yet.")
                                .font(.scaled(.callout, scale: fontScale))
                                .foregroundStyle(.secondary)
                        }
                    } else {
                        Text("\(results.count) result\(results.count == 1 ? "" : "s")")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                            .accessibilityIdentifier("catalog-results-count")
                        ForEach(results) { resultCard($0) }
                    }
                }
                .padding(.horizontal)
                .padding(.bottom, 8)
            }
        }
    }

    @ViewBuilder
    private func sectionBlock(title: String, items: [CatalogResult], testID: String) -> some View {
        if !items.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Text(title)
                    .font(.scaled(.subheadline, scale: fontScale).weight(.semibold))
                    .foregroundStyle(.secondary)
                ForEach(items) { resultCard($0) }
            }
            .accessibilityIdentifier(testID)
        }
    }

    @ViewBuilder
    private func resultCard(_ r: CatalogResult) -> some View {
        let key = r.id
        let addedName = addedNames[key]
        let added = addedName != nil || r.added

        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(r.title)
                        .font(.scaled(.headline, scale: fontScale))
                        .lineLimit(1)
                        .accessibilityIdentifier("catalog-result-title")
                    Text(r.catalogID)
                        .font(.scaledMonospaced(.caption2, scale: fontScale))
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer()
                HStack(spacing: 4) {
                    if r.official {
                        badge("Official", tint: .accentColor)
                    } else if r.verified {
                        badge("Verified", tint: .green)
                    }
                    badge(r.transport, tint: .secondary)
                }
            }

            if !r.description.isEmpty {
                Text(r.description)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            HStack {
                Spacer()
                if added {
                    Button("Added ✓ · Open") {
                        if let addedName {
                            Task { await openAddedServer(named: addedName) }
                        } else {
                            Task { await openPreviouslyAdded(r) }
                        }
                    }
                    .controlSize(.small)
                    .accessibilityIdentifier("catalog-added-\(key)")
                } else {
                    Button {
                        handleAddTapped(r)
                    } label: {
                        if addingKey == key { ProgressView().controlSize(.small) } else { Text("Add to MCPProxy") }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(addingKey != nil)
                    .accessibilityIdentifier("catalog-add-\(key)")
                }
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityIdentifier("catalog-result-\(key)")
    }

    private func badge(_ text: String, tint: Color) -> some View {
        Text(text)
            .font(.scaled(.caption2, scale: fontScale).weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(tint.opacity(0.15))
            .foregroundStyle(tint)
            .clipShape(Capsule())
    }

    // MARK: - Secrets sheet (FR-065)

    @ViewBuilder
    private func secretsSheet(_ result: CatalogResult) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(result.title)
                    .font(.scaled(.title3, scale: fontScale).bold())
                Spacer()
                Button {
                    pendingResult = nil
                } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
            }
            Text("This server needs a few values before it can run.")
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)

            ForEach($pendingFields) { $field in
                SecretFieldToggleView(name: field.name, value: $field.value, mode: $field.mode, keyringAvailable: keyringAvailable, keyringReason: keyringReason)
            }

            if let addError {
                Text(addError)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("catalog-add-error")
            }

            HStack {
                Spacer()
                Button("Cancel") { pendingResult = nil }
                    .buttonStyle(.bordered)
                Button {
                    Task { await confirmAdd(result) }
                } label: {
                    if confirming { ProgressView().controlSize(.small) } else { Text("Add to MCPProxy") }
                }
                .buttonStyle(.borderedProminent)
                .disabled(confirming || !allPendingValuesFilled || hasUnavailableSecret)
                .accessibilityIdentifier("catalog-secrets-confirm")
            }
        }
        .padding()
        .frame(width: 420)
    }

    private var allPendingValuesFilled: Bool {
        pendingFields.allSatisfy { !$0.value.trimmingCharacters(in: .whitespaces).isEmpty }
    }

    private var hasUnavailableSecret: Bool {
        !keyringAvailable && pendingFields.contains(where: { $0.mode == .secret })
    }

    // MARK: - Search

    private func scheduleSearch() {
        searchTask?.cancel()
        searchTask = Task {
            try? await Task.sleep(nanoseconds: 250_000_000)
            if Task.isCancelled { return }
            await search()
        }
    }

    private func search() async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            let resp = try await client.searchCatalog(query: query, source: sourceFilter)
            results = resp.results
            sections = resp.sections
            unavailable = resp.unavailable ?? []
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func loadKeyringAvailability() async {
        guard let client = apiClient else { return }
        do {
            let status = try await client.keyringAvailability()
            keyringAvailable = status.keyringAvailable
            keyringReason = status.keyringReason ?? "OS keyring unavailable"
        } catch {
            keyringAvailable = false
            keyringReason = "Could not verify OS keyring availability"
        }
    }

    // MARK: - Add

    private func handleAddTapped(_ result: CatalogResult) {
        if let inputs = result.requiredInputs, !inputs.isEmpty {
            pendingFields = inputs.map {
                SecretFieldInput(name: $0.name, kind: .env, value: "", mode: $0.secretLike ? .secret : .value)
            }
            addError = nil
            pendingResult = result
            return
        }
        Task { await addResult(result, env: [:]) }
    }

    private func confirmAdd(_ result: CatalogResult) async {
        guard let client = apiClient else { return }
        confirming = true
        addError = nil
        // Populated only once SecretFieldResolver.resolve has returned
        // successfully, so a failed addResult below rolls back exactly what
        // this attempt wrote — never double-rolls-back what resolve() itself
        // already rolled back on a write failure.
        var writtenRefs: [String] = []
        do {
            let serverName = result.title.isEmpty ? result.catalogID : result.title
            let resolved = try await SecretFieldResolver.resolve(client: client, serverName: serverName, fields: pendingFields)
            writtenRefs = resolved.writtenRefs
            let added = await addResult(result, env: resolved.env)
            if added {
                pendingResult = nil
            } else if !writtenRefs.isEmpty {
                await SecretFieldResolver.rollback(client: client, refs: writtenRefs)
            }
        } catch {
            addError = error.localizedDescription
        }
        confirming = false
    }

    /// Returns whether the add succeeded. Callers MUST check the return value
    /// rather than assume completion means success (mirrors the Web UI's
    /// `addResult`, whose doc comment explains why).
    @discardableResult
    private func addResult(_ result: CatalogResult, env: [String: String]) async -> Bool {
        guard let client = apiClient else { return false }
        let key = result.id
        addingKey = key
        defer { addingKey = nil }
        let outcome = await client.addServerFromRegistry(registryID: result.source, serverID: result.catalogID, env: env.isEmpty ? nil : env)
        if outcome.success {
            let assignedName = outcome.serverName ?? result.title
            addedNames[key] = assignedName
            // Adding succeeded independently of navigation. Do not let a
            // refresh failure make confirmAdd roll back secrets now referenced
            // by the persisted server configuration.
            await openAddedServer(named: assignedName)
            return true
        }
        addError = outcome.message ?? "Failed to add server"
        return false
    }

    /// Spec 109 FR-063: "Added ✓ · Open" opens the server it just added.
    private func openPreviouslyAdded(_ result: CatalogResult) async {
        guard let client = apiClient else { return }
        // The catalog handler performed this join over raw visible
        // configuration before GET /servers redacts credential-bearing query
        // values and argv. Use its name only when it established uniqueness.
        if let name = result.addedServerName {
            await openAddedServer(named: name)
            return
        }
        do {
            let refreshed = try await client.servers()
            let target = catalogTarget(result.install)
            let matches = refreshed.filter {
                serverTarget($0) == target && ($0.sourceRegistryID == result.source || $0.sourceRegistryID == nil)
            }
            guard matches.count == 1, let server = matches.first else {
                addError = matches.isEmpty
                    ? "This catalog entry is marked added, but MCPProxy could not identify one visible installed server. Open it from Servers."
                    : "More than one installed server matches this catalog entry. Open the intended server from Servers."
                return
            }
            appState.updateServers(refreshed)
            onOpenServer(server)
        } catch {
            addError = "Could not resolve the installed server. Refresh and try again."
        }
    }

    private func openAddedServer(named name: String) async {
        guard let client = apiClient else { return }
        do {
            let refreshed = try await client.servers()
            appState.updateServers(refreshed)
            guard let server = refreshed.first(where: { $0.name == name }) else {
                addError = "Added to MCPProxy, but it is not visible yet. Open it from Servers."
                return
            }
            onOpenServer(server)
        } catch {
            addError = "Added to MCPProxy, but it could not be opened. Open it from Servers."
        }
    }

    private func catalogTarget(_ install: CatalogInstall) -> String {
        if let url = install.url { return "url:\(url)" }
        return "stdio:\(install.command ?? "")\u{0}\((install.args ?? []).joined(separator: "\u{0}"))"
    }

    private func serverTarget(_ server: ServerStatus) -> String {
        if let url = server.url { return "url:\(url)" }
        return "stdio:\(server.command ?? "")\u{0}\((server.args ?? []).joined(separator: "\u{0}"))"
    }
}
