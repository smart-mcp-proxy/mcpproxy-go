// RegistriesView.swift
// MCPProxy
//
// macOS-tray registry management surface (MCP-902 / MCP-1074):
//   - lists configured registries with a neutral Official / Custom badge
//   - an "Add Registry" affordance (POST /api/v1/registries)
//   - per-custom-registry Edit (PUT /api/v1/registries/{id}) and
//     Remove (DELETE /api/v1/registries/{id}) via an ellipsis + context menu
//   - browse + add servers across registries (ServerBrowseView)
//
// Provenance is informational only (MCP-1072): servers added from any registry
// follow the global quarantine default, so there is no third-party warning.

import SwiftUI

// MARK: - Neutral provenance badge (reused by the browse info popup, MCP-1074)

/// A small, neutral 2-value provenance badge: "Official" (built-in) or
/// "Custom" (user-added). Replaces the old alarming "third-party · unverified"
/// styling now that provenance is informational only (MCP-1072).
struct RegistryProvenanceBadge: View {
    let isCustom: Bool
    @Environment(\.fontScale) var fontScale

    var body: some View {
        Text(isCustom ? "Custom" : "Official")
            .font(.scaled(.caption2, scale: fontScale).weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background((isCustom ? Color.secondary : Color.accentColor).opacity(0.16))
            .foregroundStyle(isCustom ? Color.secondary : Color.accentColor)
            .clipShape(Capsule())
            .accessibilityIdentifier(isCustom ? "registry-badge-custom" : "registry-badge-official")
    }
}

// MARK: - Registries View

struct RegistriesView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    @State private var registries: [Registry] = []
    @State private var isLoading = false
    @State private var loadError: String?
    @State private var successMessage: String?

    /// Drives the add/edit sheet. `editing == nil` => add a new registry;
    /// otherwise edit the carried (custom) registry. `.sheet(item:)` keeps the
    /// pre-filled state fresh per presentation.
    @State private var activeSheet: RegistrySheet?
    /// The custom registry awaiting a Remove confirmation, if any.
    @State private var pendingRemoval: Registry?

    private var apiClient: APIClient? { appState.apiClient }

    private var customRegistries: [Registry] { registries.filter(\.isCustom) }

    /// Custom (editable) registries first, then official (read-only) ones —
    /// a management view leads with the rows the user can act on.
    private var sortedRegistries: [Registry] {
        registries.filter(\.isCustom) + registries.filter { !$0.isCustom }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header

            // Prominent "Add Registry" button bar (mirrors ServersView).
            HStack {
                Button {
                    activeSheet = RegistrySheet(editing: nil)
                } label: {
                    Label("Add Registry", systemImage: "plus.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .accessibilityIdentifier("registry-add-source-button")
            }
            .padding(.horizontal)
            .padding(.vertical, 8)

            Divider()

            if let success = successMessage {
                banner(icon: "checkmark.circle.fill", tint: .green, text: success)
            }
            if let err = loadError {
                banner(icon: "exclamationmark.triangle.fill", tint: .orange, text: err)
            }

            configuredList

            Divider()

            // Browse + add servers across one or more registries (R1 parity).
            ServerBrowseView(appState: appState, registries: registries)
        }
        .sheet(item: $activeSheet) { sheet in
            AddRegistryView(appState: appState, editing: sheet.editing) { result in
                let verb = sheet.editing == nil ? "Added" : "Updated"
                let label = result.name.isEmpty ? result.id : result.name
                successMessage = "\(verb) registry \u{201C}\(label)\u{201D}."
                loadError = nil
                Task { await load() }
            }
        }
        .confirmationDialog(
            "Remove \u{201C}\(pendingRemoval?.name ?? "")\u{201D}?",
            isPresented: Binding(
                get: { pendingRemoval != nil },
                set: { if !$0 { pendingRemoval = nil } }
            ),
            titleVisibility: .visible
        ) {
            if let reg = pendingRemoval {
                Button("Remove", role: .destructive) {
                    Task { await remove(reg) }
                }
                .accessibilityIdentifier("registry-remove-confirm")
            }
            Button("Cancel", role: .cancel) { pendingRemoval = nil }
        } message: {
            Text("This removes the registry source from MCPProxy. Servers you have already added stay installed.")
        }
        .task { await load() }
    }

    // MARK: Header

    @ViewBuilder
    private var header: some View {
        HStack {
            Text("Registries")
                .font(.scaled(.title2, scale: fontScale).bold())
            Spacer()
            Text("\(registries.count) configured")
                .foregroundStyle(.secondary)

            if isLoading {
                ProgressView().controlSize(.small)
            } else {
                Button {
                    Task { await load() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("registries-refresh")
            }
        }
        .padding()
        .accessibilityIdentifier("registries-header")
    }

    // MARK: Configured registries list

    @ViewBuilder
    private var configuredList: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Configured registries")
                    .font(.scaled(.subheadline, scale: fontScale).weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal)
            .padding(.top, 8)
            .padding(.bottom, 4)

            if registries.isEmpty {
                Text(isLoading ? "Loading…" : "No registries configured.")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal)
                    .padding(.bottom, 8)
                    .accessibilityIdentifier("registries-list-empty")
            } else {
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(sortedRegistries) { reg in
                            registryRow(reg)
                            if reg.id != sortedRegistries.last?.id { Divider() }
                        }
                    }
                }
                .frame(maxHeight: 360)
                .accessibilityIdentifier("registries-list")
            }
        }
    }

    @ViewBuilder
    private func registryRow(_ reg: Registry) -> some View {
        HStack(spacing: 10) {
            // Built-in indicator for official registries (MCP-1074).
            Image(systemName: reg.isCustom ? "globe" : "checkmark.seal.fill")
                .foregroundStyle(reg.isCustom ? Color.secondary : Color.accentColor)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(reg.name.isEmpty ? reg.id : reg.name)
                        .font(.scaled(.body, scale: fontScale).weight(.medium))
                    RegistryProvenanceBadge(isCustom: reg.isCustom)
                }
                if let url = reg.serversURL ?? reg.url, !url.isEmpty {
                    Text(url)
                        .font(.scaledMonospaced(.caption2, scale: fontScale))
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }

            Spacer()

            // Custom registries are editable/removable; official ones are
            // read-only (no menu).
            if reg.isCustom {
                Menu {
                    rowActions(reg)
                } label: {
                    Image(systemName: "ellipsis.circle")
                        .foregroundStyle(.secondary)
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Edit or remove this registry")
                .accessibilityIdentifier("registry-row-menu-\(reg.id)")
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .contentShape(Rectangle())
        // Right-click parity with the ellipsis menu (custom registries only).
        .contextMenu {
            if reg.isCustom { rowActions(reg) }
        }
        .accessibilityIdentifier("registry-row-\(reg.id)")
    }

    /// Edit / Remove actions shared by the ellipsis menu and the context menu.
    @ViewBuilder
    private func rowActions(_ reg: Registry) -> some View {
        Button {
            activeSheet = RegistrySheet(editing: reg)
        } label: {
            Label("Edit Registry\u{2026}", systemImage: "pencil")
        }
        .accessibilityIdentifier("registry-edit-\(reg.id)")

        Button(role: .destructive) {
            pendingRemoval = reg
        } label: {
            Label("Remove Registry\u{2026}", systemImage: "trash")
        }
        .accessibilityIdentifier("registry-remove-\(reg.id)")
    }

    // MARK: Content

    @ViewBuilder
    private func banner(icon: String, tint: Color, text: String) -> some View {
        HStack {
            Image(systemName: icon).foregroundStyle(tint)
            Text(text)
                .font(.scaled(.caption, scale: fontScale))
            Spacer()
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(tint.opacity(0.1))
    }

    // MARK: Data

    private func load() async {
        guard let client = apiClient else {
            loadError = "Not connected to MCPProxy core"
            return
        }
        isLoading = true
        loadError = nil
        defer { isLoading = false }
        do {
            registries = try await client.registries()
        } catch {
            loadError = error.localizedDescription
        }
    }

    private func remove(_ reg: Registry) async {
        guard let client = apiClient else {
            loadError = "Not connected to MCPProxy core"
            return
        }
        pendingRemoval = nil
        let result = await client.removeRegistrySource(id: reg.id)
        if result.success {
            let label = reg.name.isEmpty ? reg.id : reg.name
            successMessage = "Removed registry \u{201C}\(label)\u{201D}."
            loadError = nil
            await load()
        } else {
            loadError = result.userMessage
        }
    }
}

/// Identifiable wrapper for the add/edit sheet (`.sheet(item:)`).
struct RegistrySheet: Identifiable {
    let id = UUID()
    /// nil => add a new registry; otherwise the custom registry being edited.
    let editing: Registry?
}

// MARK: - Add / Edit Registry Sheet

struct AddRegistryView: View {
    @ObservedObject var appState: AppState
    /// nil => add mode; non-nil => edit the carried custom registry (id immutable).
    let editing: Registry?
    /// Called with the added/updated registry summary on success.
    let onCompleted: (RegistrySummary) -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.fontScale) var fontScale

    @State private var url: String
    @State private var name: String
    @State private var submitError: String?
    @State private var isSubmitting = false

    /// Only the official protocol is offered (mirrors the Web UI's single
    /// option); the field exists so the contract stays explicit.
    private let registryProtocol = "modelcontextprotocol/registry"

    init(appState: AppState, editing: Registry? = nil, onCompleted: @escaping (RegistrySummary) -> Void) {
        self.appState = appState
        self.editing = editing
        self.onCompleted = onCompleted
        _url = State(initialValue: editing?.serversURL ?? editing?.url ?? "")
        _name = State(initialValue: editing?.name ?? "")
    }

    private var apiClient: APIClient? { appState.apiClient }
    private var isEditing: Bool { editing != nil }
    private var title: String { isEditing ? "Edit Registry" : "Add a Registry" }
    private var submitTitle: String { isEditing ? "Save Changes" : "Add Registry" }

    private var isValid: Bool {
        !url.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(title)
                    .font(.scaled(.title2, scale: fontScale).bold())
                Spacer()
                Button {
                    if !isSubmitting { dismiss() }
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
            }
            .padding()

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text(isEditing
                         ? "Update this custom **modelcontextprotocol/registry** source. The registry id is fixed; you can change its name or URL."
                         : "Add a custom **modelcontextprotocol/registry** v0.1 source by its HTTPS URL.")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)

                    if let editing {
                        formField(label: "Registry ID") {
                            Text(editing.id)
                                .font(.scaledMonospaced(.body, scale: fontScale))
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                                .accessibilityIdentifier("registry-edit-id")
                        }
                    }

                    formField(label: "Registry URL (required)") {
                        TextField("https://registry.example.com/", text: $url)
                            .textFieldStyle(.roundedBorder)
                            .accessibilityIdentifier("registry-add-url-input")
                    }

                    formField(label: "Protocol") {
                        Text(registryProtocol)
                            .font(.scaledMonospaced(.body, scale: fontScale))
                            .foregroundStyle(.secondary)
                    }

                    formField(label: "Name (optional)") {
                        TextField("Derived from the URL host when empty", text: $name)
                            .textFieldStyle(.roundedBorder)
                            .accessibilityIdentifier("registry-add-name-input")
                    }

                    if let err = submitError {
                        HStack(alignment: .top, spacing: 8) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(.red)
                            Text(err)
                                .font(.scaled(.caption, scale: fontScale))
                                .foregroundStyle(.red)
                                .textSelection(.enabled)
                            Spacer()
                        }
                        .padding(8)
                        .background(Color.red.opacity(0.1))
                        .cornerRadius(6)
                        .accessibilityIdentifier("registry-add-error")
                    }
                }
                .padding()
            }

            Divider()

            HStack {
                Spacer()
                Button("Cancel") {
                    if !isSubmitting { dismiss() }
                }
                .buttonStyle(.bordered)

                Button {
                    Task { await submit() }
                } label: {
                    if isSubmitting {
                        ProgressView().controlSize(.small)
                    } else {
                        Text(submitTitle)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!isValid || isSubmitting)
                .accessibilityIdentifier("registry-add-submit")
            }
            .padding()
        }
        .frame(width: 520, height: 440)
    }

    @ViewBuilder
    private func formField(label: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)
            content()
        }
    }

    /// Submit the add (POST) or edit (PUT) directly — provenance is
    /// informational only now, so there is no third-party warning to gate on.
    private func submit() async {
        guard isValid, !isSubmitting else { return }
        guard let client = apiClient else {
            submitError = "Not connected to MCPProxy core"
            return
        }
        isSubmitting = true
        submitError = nil
        defer { isSubmitting = false }

        let trimmedURL = url.trimmingCharacters(in: .whitespaces)
        let trimmedName = name.trimmingCharacters(in: .whitespaces)
        let nameOrNil = trimmedName.isEmpty ? nil : trimmedName

        let result: AddRegistrySourceResult
        if let editing {
            result = await client.editRegistrySource(id: editing.id, url: trimmedURL, name: nameOrNil)
        } else {
            result = await client.addRegistrySource(url: trimmedURL, protocol: registryProtocol, name: nameOrNil)
        }

        if result.success, let registry = result.registry {
            onCompleted(registry)
            dismiss()
            return
        }
        submitError = result.userMessage
    }
}
