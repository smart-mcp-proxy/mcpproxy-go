// ClientSheets.swift
// MCPProxy
//
// Spec 108-k (K10, K11): the sheets opened from the Clients page: move clients
// between profiles in bulk, upgrade clients that hold the admin key, add an
// "other" client, rotate a credential and forget a client. Every sheet states
// the change BEFORE its button.
//
// A credential the core returns (a custom client's, a custom rotation's) is
// held in the sheet's model only and wiped when the sheet goes away.

import AppKit
import SwiftUI

private func bindingSource(_ appState: AppState) -> ClientBindingSource {
    appState.deferredClientSource
}

/// Resolves the app's API client at call time, so a model built before the
/// core is up (the Clients page stays alive across restarts of the core) still
/// works once it answers.
struct DeferredClientSource: ClientBindingSource {
    let resolve: @Sendable () async -> APIClient?

    private func api() async throws -> APIClient {
        guard let client = await resolve() else { throw APIClientError.notReady }
        return client
    }

    func setBinding(_ clientId: String, profile: String, mode: BindingMode?) async throws -> ClientBindingResponse {
        try await api().setBinding(clientId, profile: profile, mode: mode)
    }
    func bulkAssign(from: String, to: String, mode: BindingMode?) async throws -> BulkAssignResponse {
        try await api().bulkAssign(from: from, to: to, mode: mode)
    }
    func addCustomClient(id: String, displayName: String?, profile: String?, mode: BindingMode?, expiresIn: String?) async throws -> CustomClientResponse {
        try await api().addCustomClient(id: id, displayName: displayName, profile: profile, mode: mode, expiresIn: expiresIn)
    }
    func rotateClient(_ id: String, preconditionToken: String?) async throws -> RotateResponse {
        try await api().rotateClient(id, preconditionToken: preconditionToken)
    }
    func finalizeRotation(_ id: String) async throws -> FinalizeRotationResponse {
        try await api().finalizeRotation(id)
    }
    func forgetClient(_ id: String, disconnect: Bool) async throws -> ForgetClientResponse {
        try await api().forgetClient(id, disconnect: disconnect)
    }
    func previewAdminKeyUpgrade(profile: String?, mode: BindingMode?) async throws -> UpgradePreview {
        try await api().previewAdminKeyUpgrade(profile: profile, mode: mode)
    }
    func applyAdminKeyUpgrade(profile: String?, mode: BindingMode?, preconditionToken: String?) async throws -> UpgradeResult {
        try await api().applyAdminKeyUpgrade(profile: profile, mode: mode, preconditionToken: preconditionToken)
    }
    func connectPreview(_ clientId: String, serverName: String, binding: ConnectBinding) async throws -> ConnectPreviewModel {
        try await api().connectPreview(clientId, serverName: serverName, binding: binding)
    }
}

extension AppState {
    /// A binding source that follows `apiClient` as it changes.
    var deferredClientSource: ClientBindingSource {
        DeferredClientSource { [weak self] in
            await MainActor.run { self?.apiClient }
        }
    }
}

/// Stands in when no core connection exists yet: every call fails politely.
struct NoCoreClientSource: ClientBindingSource {
    func setBinding(_ clientId: String, profile: String, mode: BindingMode?) async throws -> ClientBindingResponse { throw APIClientError.notReady }
    func bulkAssign(from: String, to: String, mode: BindingMode?) async throws -> BulkAssignResponse { throw APIClientError.notReady }
    func addCustomClient(id: String, displayName: String?, profile: String?, mode: BindingMode?, expiresIn: String?) async throws -> CustomClientResponse { throw APIClientError.notReady }
    func rotateClient(_ id: String, preconditionToken: String?) async throws -> RotateResponse { throw APIClientError.notReady }
    func finalizeRotation(_ id: String) async throws -> FinalizeRotationResponse { throw APIClientError.notReady }
    func forgetClient(_ id: String, disconnect: Bool) async throws -> ForgetClientResponse { throw APIClientError.notReady }
    func previewAdminKeyUpgrade(profile: String?, mode: BindingMode?) async throws -> UpgradePreview { throw APIClientError.notReady }
    func applyAdminKeyUpgrade(profile: String?, mode: BindingMode?, preconditionToken: String?) async throws -> UpgradeResult { throw APIClientError.notReady }
    func connectPreview(_ clientId: String, serverName: String, binding: ConnectBinding) async throws -> ConnectPreviewModel { throw APIClientError.notReady }
}

// MARK: - Bulk move

struct BulkMoveSheet: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: BulkMoveModel

    init(appState: AppState, onDone: @escaping () -> Void) {
        self.appState = appState
        self.onDone = onDone
        _model = StateObject(wrappedValue: BulkMoveModel(source: bindingSource(appState)))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Move clients").font(.title3.bold())
            Text("Every client credential bound to the first profile moves to the second. Clients without a client credential are left where they are.")
                .font(.caption).foregroundStyle(.secondary)
            Picker("From", selection: $model.from) { profileChoices }
                .accessibilityIdentifier("bulk-move-from")
            Picker("To", selection: $model.to) { profileChoices }
                .accessibilityIdentifier("bulk-move-to")
            Picker("Mode", selection: Binding(get: { model.mode }, set: { model.mode = $0 })) {
                Text("Keep each client’s mode").tag(BindingMode?.none)
                Text("Locked").tag(BindingMode?.some(.locked))
                Text("Switchable").tag(BindingMode?.some(.switchable))
            }
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            if let summary = model.summary, let result = model.result {
                VStack(alignment: .leading, spacing: 4) {
                    Label(summary, systemImage: "checkmark.circle.fill").font(.callout).foregroundStyle(.green)
                    ForEach(result.skipped) { skipped in
                        Text("• \(skipped.clientId): \(skipped.reason)").font(.caption).foregroundStyle(.orange)
                    }
                }
                .accessibilityIdentifier("bulk-move-result")
            }
            HStack {
                Spacer()
                Button(model.result == nil ? "Cancel" : "Done") { dismiss(); if model.result != nil { onDone() } }
                    .keyboardShortcut(.cancelAction)
                Button("Move") { Task { await model.run() } }
                    .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                    .disabled(!model.canRun)
                    .accessibilityIdentifier("bulk-move-confirm")
            }
        }
        .padding(20).frame(width: 480)
    }

    @ViewBuilder
    private var profileChoices: some View {
        Text("All servers").tag("")
        ForEach(appState.profiles) { Text($0.displayTitle).tag($0.name) }
    }
}

// MARK: - Admin-key upgrade

struct AdminKeyUpgradeSheet: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: AdminKeyUpgradeModel
    @State private var chosenProfile = ""

    init(appState: AppState, onDone: @escaping () -> Void) {
        self.appState = appState
        self.onDone = onDone
        _model = StateObject(wrappedValue: AdminKeyUpgradeModel(source: bindingSource(appState)))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Upgrade admin-key clients").font(.title3.bold())
            Text("These clients hold the admin API key, which no profile can limit. Upgrading gives each a client credential. Nothing is written until you Apply.")
                .font(.caption).foregroundStyle(.secondary)

            HStack {
                Picker("Profile", selection: $chosenProfile) {
                    Text("All servers").tag("")
                    ForEach(appState.profiles) { Text($0.displayTitle).tag($0.name) }
                }
                .frame(maxWidth: 320)
                .accessibilityIdentifier("upgrade-profile")
                Button("Preview") { Task { await preview() } }
                    .accessibilityIdentifier("upgrade-preview")
            }

            content
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Close") { dismiss(); onDone() }.keyboardShortcut(.cancelAction)
                Button("Apply") { Task { await model.apply() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(!model.canApply)
                    .accessibilityIdentifier("upgrade-apply")
            }
        }
        .padding(20).frame(minWidth: 620, minHeight: 420)
        .task { await preview() }
    }

    private func preview() async {
        model.profile = chosenProfile.isEmpty ? nil : chosenProfile
        await model.loadPreview()
    }

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .idle:
            EmptyView()
        case .loading, .applying:
            ProgressView().controlSize(.small)
        case .failed(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(.red)
        case .nothingToUpgrade:
            Text("No client holds the admin API key.").font(.callout)
            rotatePanel
        case .preview(let preview):
            if let refusal = preview.guardRefusal {
                GuardRefusalView(error: "Applying would let a client escape its profile while authentication is off.",
                                 bindings: refusal.bindings, fixes: refusal.fixes) { route in
                    dismiss(); appState.navigate(route)
                }
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(preview.preview) { row in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(row.displayName).font(.callout.weight(.semibold))
                                Spacer()
                                Text(row.credential).font(.caption.monospaced())
                            }
                            Text(row.displayPath ?? "").font(.caption).foregroundStyle(.secondary)
                            Text("\(row.profile.isEmpty ? "All servers" : row.profile) · \(row.mode == .locked ? "locked" : "switchable")")
                                .font(.caption)
                            if let error = row.error { Text(error).font(.caption).foregroundStyle(.red) }
                            if let diff = row.diff {
                                DisclosureGroup("Change") {
                                    Text(diffText(diff)).font(.caption.monospaced()).textSelection(.enabled)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .font(.caption)
                            }
                        }
                        .padding(8).background(Color.secondary.opacity(0.08))
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                    }
                }
            }
        case .applied(let result):
            VStack(alignment: .leading, spacing: 6) {
                if !result.upgraded.isEmpty {
                    Label("Upgraded: \(result.upgraded.joined(separator: ", "))", systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green).font(.callout)
                }
                ForEach(result.failed) { failure in
                    Label("\(failure.clientId): \(failure.error)", systemImage: "xmark.octagon.fill")
                        .foregroundStyle(.red).font(.callout)
                }
            }
            .accessibilityIdentifier("upgrade-result")
            if model.showsRotatePanel { rotatePanel }
        }
    }

    /// "Rotate the admin API key": the two steps of the documented procedure.
    /// The sheet never rotates the key itself.
    private var rotatePanel: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Rotate the admin API key").font(.subheadline.weight(.semibold))
            Text("Clients you upgraded no longer need it, but every copy that was not upgraded still works until the key changes.")
                .font(.caption).foregroundStyle(.secondary)
            ForEach(Array(AdminKeyUpgradeModel.rotateSteps.enumerated()), id: \.offset) { index, step in
                Text("\(index + 1). \(step)").font(.caption)
            }
            if let url = URL(string: AdminKeyUpgradeModel.rotateDocsURL) {
                Link("How to rotate the admin API key", destination: url).font(.caption)
            }
        }
        .padding(10).background(Color.blue.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .accessibilityIdentifier("upgrade-rotate-panel")
    }

    private func diffText(_ value: JSONValue) -> String {
        guard let data = try? JSONEncoder().encode(value),
              let object = try? JSONSerialization.jsonObject(with: data),
              let pretty = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys]),
              let text = String(data: pretty, encoding: .utf8) else { return "" }
        return text
    }
}

// MARK: - Other client + credential once

struct OtherClientSheet: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: CustomClientModel

    init(appState: AppState, onDone: @escaping () -> Void) {
        self.appState = appState
        self.onDone = onDone
        _model = StateObject(wrappedValue: CustomClientModel(source: bindingSource(appState)))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Add another client").font(.title3.bold())
            if let credential = model.credential {
                CredentialOnceView(credential: credential, snippet: model.snippet)
            } else {
                form
            }
            HStack {
                Spacer()
                Button(model.credential == nil ? "Cancel" : "Done") { finish() }
                    .keyboardShortcut(model.credential == nil ? .cancelAction : .defaultAction)
                    .accessibilityIdentifier("other-client-done")
                if model.credential == nil {
                    Button("Create") { Task { await model.create() } }
                        .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                        .disabled(!model.canCreate)
                        .accessibilityIdentifier("other-client-create")
                }
            }
        }
        .padding(20).frame(width: 500)
        .onDisappear { model.dismiss() }
    }

    private func finish() {
        model.dismiss()
        dismiss()
        onDone()
    }

    @ViewBuilder
    private var form: some View {
        Text("For a client MCPProxy cannot configure itself. It gets its own credential, bound to a profile.")
            .font(.caption).foregroundStyle(.secondary)
        VStack(alignment: .leading, spacing: 2) {
            TextField("Client id, e.g. dev-laptop", text: $model.id).textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("other-client-id")
            Text(CustomClientModel.idHelp).font(.caption2).foregroundStyle(.secondary)
            if let error = model.idError {
                Label(error, systemImage: "exclamationmark.circle.fill").font(.caption).foregroundStyle(.red)
                    .accessibilityIdentifier("other-client-id-error")
            }
        }
        TextField("Display name (optional)", text: $model.displayName).textFieldStyle(.roundedBorder)
        Picker("Profile", selection: $model.profile) {
            Text("All servers").tag("")
            ForEach(appState.profiles) { Text($0.displayTitle).tag($0.name) }
        }
        .accessibilityIdentifier("other-client-profile")
        Toggle("Locked — the client cannot switch to another profile", isOn: $model.locked)
            .disabled(model.profile.isEmpty)
        Picker("Expires", selection: $model.expiresIn) {
            ForEach(CustomClientModel.expiryChoices, id: \.value) { Text($0.label).tag($0.value) }
        }
        if let refusal = model.guardRefusal {
            GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
        }
        if let message = model.errorMessage {
            Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
        }
    }
}

/// The credential, once: selectable, copyable, never stored.
struct CredentialOnceView: View {
    let credential: String
    let snippet: String?
    /// "Client created" on first issue, "New credential issued" on a rotation.
    var title: String = "Client created"
    @State private var copiedCredential = false
    @State private var copiedSnippet = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(title, systemImage: "checkmark.circle.fill").foregroundStyle(.green)
            Text("Shown once. MCPProxy stores only a hash.").font(.callout.weight(.semibold))
            HStack {
                Text(credential)
                    .font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                    .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.secondary.opacity(0.12)).clipShape(RoundedRectangle(cornerRadius: 6))
                    .accessibilityLabel("Client credential")
                Button(copiedCredential ? "Copied" : "Copy") {
                    copy(credential); copiedCredential = true
                }
                .accessibilityIdentifier("credential-once-copy")
            }
            if let snippet, !snippet.isEmpty {
                Text("Generic HTTP client configuration").font(.caption).foregroundStyle(.secondary)
                HStack(alignment: .top) {
                    Text(snippet)
                        .font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.secondary.opacity(0.08)).clipShape(RoundedRectangle(cornerRadius: 6))
                    Button(copiedSnippet ? "Copied" : "Copy") { copy(snippet); copiedSnippet = true }
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("credential-once")
    }

    private func copy(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

// MARK: - Rotate

struct RotateClientSheet: View {
    @ObservedObject var appState: AppState
    let client: ClientPresenceRecord
    let onDone: () -> Void
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: RotationModel

    init(appState: AppState, client: ClientPresenceRecord, onDone: @escaping () -> Void) {
        self.appState = appState
        self.client = client
        self.onDone = onDone
        _model = StateObject(wrappedValue: RotationModel(source: bindingSource(appState), client: client))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Rotate \(client.displayName)’s credential").font(.title3.bold())
            if model.isSupportedClient {
                Text("MCPProxy writes a new credential into \(client.displayName)’s configuration, keeps its profile and mode, and removes the old one once the write succeeds. If the write fails, the old credential stays.")
                    .font(.caption).foregroundStyle(.secondary)
                if let preview = model.preview {
                    Text("Config file: \(preview.effectiveDisplayPath)").font(.caption.monospaced())
                }
            } else {
                Text("A new credential is issued. The old one keeps working until you finalize the rotation, or for 24 hours.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let credential = model.credential {
                CredentialOnceView(credential: credential, snippet: model.snippet, title: "New credential issued")
            }
            outcomeView
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            HStack {
                Spacer()
                Button(model.outcome == nil ? "Cancel" : "Done") { model.dismiss(); dismiss(); onDone() }
                    .keyboardShortcut(.cancelAction)
                if model.outcome == .pending {
                    Button("Finalize rotation") { Task { await model.finalize() } }
                        .accessibilityIdentifier("rotate-finalize")
                }
                if model.outcome == nil {
                    Button("Rotate") { Task { await model.rotate() } }
                        .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                        .disabled(model.isBusy)
                        .accessibilityIdentifier("rotate-confirm")
                }
            }
        }
        .padding(20).frame(width: 500)
        .task { await model.loadPreview() }
        .onDisappear { model.dismiss() }
    }

    @ViewBuilder
    private var outcomeView: some View {
        switch model.outcome {
        case .some(.finalized):
            Label("Rotated. The old credential no longer works.", systemImage: "checkmark.circle.fill")
                .font(.callout).foregroundStyle(.green)
        case .some(.rolledBack):
            Label("The write failed, so the old credential was kept.", systemImage: "arrow.uturn.backward.circle.fill")
                .font(.callout).foregroundStyle(.orange)
        case .some(.pending):
            Label("Rotation pending — update the client, then finalize.", systemImage: "clock.fill")
                .font(.callout).foregroundStyle(.orange)
        case .none:
            EmptyView()
        }
    }
}

// MARK: - Forget

struct ForgetClientSheet: View {
    let client: ClientPresenceRecord
    let onConfirm: (_ disconnect: Bool) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var disconnect = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Forget \(client.displayName)?").font(.title3.bold())
            Text("Its client credential is revoked, so it can no longer use MCPProxy.")
                .font(.callout)
            if client.kind == "supported" {
                Toggle("Also remove MCPProxy from its configuration file", isOn: $disconnect)
                    .accessibilityIdentifier("forget-disconnect")
                Text(disconnect
                     ? "The entry is removed from its config first; the credential is revoked either way."
                     : "Its config file keeps the entry; it stops working once the credential is revoked.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Forget", role: .destructive) { onConfirm(disconnect); dismiss() }
                    .keyboardShortcut(.defaultAction)
                    .accessibilityIdentifier("forget-confirm")
            }
        }
        .padding(20).frame(width: 440)
    }
}
