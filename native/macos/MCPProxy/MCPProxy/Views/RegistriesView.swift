// RegistriesView.swift
// MCPProxy
//
// macOS-tray mirror of the MCP-867 Web UI registry surface (MCP-902):
//   - lists configured registries with provenance/trust badges
//   - an "Add Registry" affordance (POST /api/v1/registries)
//   - a one-time third-party warning gating the first custom add
//
// Servers discovered through a custom (third-party) registry are always
// quarantined and can never skip security review; the UI surfaces that.

import SwiftUI

// MARK: - Registries View

struct RegistriesView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    @State private var registries: [Registry] = []
    @State private var isLoading = false
    @State private var loadError: String?
    @State private var showAddRegistry = false
    @State private var successMessage: String?

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header

            // Prominent "Add Registry" button bar (mirrors ServersView).
            HStack {
                Button {
                    showAddRegistry = true
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

            content
        }
        .sheet(isPresented: $showAddRegistry) {
            AddRegistryView(appState: appState, isPresented: $showAddRegistry) { added in
                successMessage = "Added registry \u{201C}\(added.name.isEmpty ? added.id : added.name)\u{201D} — third-party \u{00B7} unverified."
                Task { await load() }
            }
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

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if let err = loadError, registries.isEmpty {
            VStack(spacing: 12) {
                Spacer()
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 40 * fontScale))
                    .foregroundStyle(.orange)
                Text("Couldn't load registries")
                    .font(.scaled(.title3, scale: fontScale))
                    .foregroundStyle(.secondary)
                Text(err)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .multilineTextAlignment(.center)
                Spacer()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .padding()
        } else if registries.isEmpty && !isLoading {
            VStack(spacing: 12) {
                Spacer()
                Image(systemName: "books.vertical")
                    .font(.system(size: 40 * fontScale))
                    .foregroundStyle(.tertiary)
                Text("No registries")
                    .font(.scaled(.title3, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(registries) { registry in
                        registryRow(registry)
                    }
                }
                .padding()
            }
        }
    }

    @ViewBuilder
    private func registryRow(_ registry: Registry) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text(registry.name.isEmpty ? registry.id : registry.name)
                    .font(.scaled(.headline, scale: fontScale))
                provenanceBadge(registry)
                Spacer()
            }

            if let desc = registry.description, !desc.isEmpty {
                Text(desc)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            }

            if let url = registry.serversURL ?? registry.url, !url.isEmpty {
                Text(url)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            if registry.isCustom {
                Text("Servers added from this third-party registry are always quarantined and cannot skip security review.")
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.orange)
                    .accessibilityIdentifier("registry-custom-quarantine-note")
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(8)
        .accessibilityIdentifier("registry-row-\(registry.id)")
    }

    @ViewBuilder
    private func provenanceBadge(_ registry: Registry) -> some View {
        if registry.isCustom {
            badge(text: "Third-party \u{00B7} unverified", tint: .orange)
                .accessibilityIdentifier("registry-provenance-badge-custom")
        } else {
            badge(text: "Official \u{00B7} trusted", tint: .green)
                .accessibilityIdentifier("registry-provenance-badge-official")
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
}

// MARK: - Add Registry Sheet

struct AddRegistryView: View {
    @ObservedObject var appState: AppState
    @Binding var isPresented: Bool
    let onAdded: (RegistrySummary) -> Void
    @Environment(\.fontScale) var fontScale

    @State private var url = ""
    @State private var name = ""
    /// Only the official protocol is offered (mirrors the Web UI's single
    /// option); the field exists so the contract stays explicit.
    private let registryProtocol = "modelcontextprotocol/registry"

    @State private var addError: String?
    @State private var isAdding = false
    @State private var showThirdPartyWarning = false

    private var apiClient: APIClient? { appState.apiClient }
    private let ack = ThirdPartyRegistryAck()

    private var isValid: Bool {
        !url.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Add a Registry")
                    .font(.scaled(.title2, scale: fontScale).bold())
                Spacer()
                Button {
                    if !isAdding { isPresented = false }
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
                    Text("Add a custom **modelcontextprotocol/registry** v0.1 source by its HTTPS URL. Added registries are marked **third-party \u{00B7} unverified**; their servers are always quarantined.")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)

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

                    if let err = addError {
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
                    if !isAdding { isPresented = false }
                }
                .buttonStyle(.bordered)

                Button {
                    submit()
                } label: {
                    if isAdding {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("Add Registry")
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!isValid || isAdding)
                .accessibilityIdentifier("registry-add-submit")
            }
            .padding()
        }
        .frame(width: 520, height: 420)
        // One-time third-party warning (MCP-867 parity). Gates the first custom
        // add until the user acknowledges it once.
        .alert("Adding a third-party registry", isPresented: $showThirdPartyWarning) {
            Button("Cancel", role: .cancel) {}
            Button("I understand, continue") {
                ack.acknowledge()
                Task { await doAdd() }
            }
        } message: {
            Text("You're about to add a registry that is not shipped with MCPProxy. Custom registries are unverified — MCPProxy cannot vouch for the servers they list.\n\nFor your safety, every server you add from a custom registry is always quarantined and can never skip security review. Only add registries operated by parties you trust.")
        }
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

    /// Gate the first-ever custom add behind the one-time warning; every
    /// user-added source is custom/unverified server-side, so the warning
    /// applies to all adds until acknowledged once.
    private func submit() {
        guard isValid, !isAdding else { return }
        addError = nil
        if ack.hasAcknowledged {
            Task { await doAdd() }
        } else {
            showThirdPartyWarning = true
        }
    }

    private func doAdd() async {
        guard let client = apiClient else {
            addError = "Not connected to MCPProxy core"
            return
        }
        isAdding = true
        addError = nil
        defer { isAdding = false }

        let result = await client.addRegistrySource(
            url: url.trimmingCharacters(in: .whitespaces),
            protocol: registryProtocol,
            name: name.trimmingCharacters(in: .whitespaces).isEmpty ? nil : name.trimmingCharacters(in: .whitespaces)
        )

        if result.success, let registry = result.registry {
            isPresented = false
            onAdded(registry)
            return
        }
        addError = result.userMessage
    }
}
