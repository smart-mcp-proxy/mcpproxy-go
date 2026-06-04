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
            if let err = loadError {
                banner(icon: "exclamationmark.triangle.fill", tint: .orange, text: err)
            }

            // Browse + add servers across one or more registries (R1 parity).
            // The configured-registries list lives in the browse multiselect and
            // the per-result registry badge → info popup (MCP-1050); there is no
            // longer a bottom "Configured registries" description panel.
            ServerBrowseView(appState: appState, registries: registries)
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
