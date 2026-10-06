// ProfileSheets.swift
// MCPProxy
//
// Spec 108-k (K6, K8): the rename, delete and assign sheets of a profile. Rename
// and delete LIST what they move BEFORE acting: the clients bound to the
// profile (with their mode), the tokens pinned to it, the profiles whose
// "Agent may switch to" names it, and the anonymous callers.

import AppKit
import SwiftUI

/// Posts a VoiceOver announcement (macOS 13 compatible).
enum AccessibilityAnnouncer {
    static func post(_ text: String) {
        guard let app = NSApp else { return }
        NSAccessibility.post(
            element: app, notification: .announcementRequested,
            userInfo: [
                .announcement: text,
                .priority: NSAccessibilityPriorityLevel.high.rawValue,
            ])
    }
}

// MARK: - Rename

struct RenameProfileSheet: View {
    @ObservedObject var appState: AppState
    @ObservedObject var model: ProfileEditorModel
    @Environment(\.dismiss) private var dismiss
    @State private var newName = ""

    var body: some View {
        let impact = model.impact(in: appState.profiles)
        VStack(alignment: .leading, spacing: 12) {
            Text("Rename profile").font(.title3.bold())
            Text("These follow the new name automatically:").font(.callout)
            impactList(impact)
            TextField("New name (slug)", text: $newName)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("profile-rename-name")
            if let error = model.fieldError, error.field == "new_name" || error.field == "name" {
                Label(error.message, systemImage: "exclamationmark.circle.fill")
                    .font(.caption).foregroundStyle(.red)
            }
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename") {
                    Task { if await model.rename(to: newName.trimmingCharacters(in: .whitespaces)) { dismiss() } }
                }
                .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                .disabled(newName.trimmingCharacters(in: .whitespaces).isEmpty || model.isSaving)
                .accessibilityIdentifier("profile-rename-confirm")
            }
        }
        .padding(20).frame(width: 440)
        .onAppear { newName = model.original?.name ?? "" }
    }
}

@ViewBuilder
private func impactList(_ impact: ProfileImpact) -> some View {
    if impact.isEmpty {
        Text("Nothing points at this profile yet.").font(.caption).foregroundStyle(.secondary)
    } else {
        VStack(alignment: .leading, spacing: 2) {
            ForEach(impact.lines, id: \.self) { Text("• \($0)").font(.caption) }
        }
        .accessibilityIdentifier("profile-impact-list")
    }
}

// MARK: - Delete

struct DeleteProfileSheet: View {
    @ObservedObject var appState: AppState
    let profile: ProfileView
    let onDone: () -> Void

    @Environment(\.dismiss) private var dismiss
    @StateObject private var ownModel: ProfileEditorModel
    private let sharedModel: ProfileEditorModel?
    @State private var target = ""
    @State private var force = false

    init(appState: AppState, profile: ProfileView, model: ProfileEditorModel? = nil, onDone: @escaping () -> Void) {
        self.appState = appState
        self.profile = profile
        self.onDone = onDone
        self.sharedModel = model
        let source: ProfileEditorSource = appState.apiClient ?? UnavailableProfileSource()
        _ownModel = StateObject(wrappedValue: ProfileEditorModel(source: source, profile: profile))
    }

    private var model: ProfileEditorModel { sharedModel ?? ownModel }

    private var impact: ProfileImpact {
        var impact = ProfileImpact(profile: profile, all: appState.profiles)
        if let conflict = model.conflictUsedBy { impact.merge(conflict) }
        return impact
    }

    private var targets: [ProfileView] {
        ProfileEditorModel.reassignTargets(excluding: profile.name, in: appState.profiles)
    }

    var body: some View {
        let impact = impact
        VStack(alignment: .leading, spacing: 12) {
            Text("Delete “\(profile.displayTitle)”?").font(.title3.bold())
            if impact.isEmpty {
                Text("Nothing points at this profile.").font(.callout)
            } else {
                Text("These will be affected:").font(.callout)
                impactList(impact)
            }
            if impact.requiresTarget {
                Picker("Move them to", selection: $target) {
                    Text("Choose a profile…").tag("")
                    ForEach(targets) { Text($0.displayTitle).tag($0.name) }
                }
                .accessibilityIdentifier("profile-delete-target")
                if targets.isEmpty {
                    Text("There is no other profile to move them to. Create one first.")
                        .font(.caption).foregroundStyle(.orange)
                }
                if impact.canForce {
                    Toggle("Leave them without a profile (denied all tools)", isOn: $force)
                        .accessibilityIdentifier("profile-delete-force")
                    if force {
                        Text("Clients and tokens that pointed at it will be denied every tool until you choose a profile for them.")
                            .font(.caption).foregroundStyle(.orange)
                    }
                }
            }
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Delete", role: .destructive) {
                    Task {
                        let moved = impact.requiresTarget && !force ? target : nil
                        if await model.delete(reassignTo: moved?.isEmpty == true ? nil : moved, force: force) {
                            dismiss()
                            onDone()
                        }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.isSaving || (impact.requiresTarget && !force && target.isEmpty))
                .accessibilityIdentifier("profile-delete-confirm")
            }
        }
        .padding(20).frame(width: 460)
    }
}

// MARK: - Assign

/// "Assign to client…": bind one client to this profile, locked or switchable.
struct AssignProfileSheet: View {
    @ObservedObject var appState: AppState
    let profile: ProfileView

    @Environment(\.dismiss) private var dismiss
    @State private var clientId = ""
    @State private var locked = true
    @State private var model: ClientBindingModel?
    @State private var done: String?

    private var eligible: [ClientPresenceRecord] { appState.clients.filter(\.hasClientCredential) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Assign “\(profile.displayTitle)” to a client").font(.title3.bold())
            if eligible.isEmpty {
                Text("No client has a client credential yet. Connect a client with this profile from Clients, or upgrade one that holds the admin key.")
                    .font(.callout).foregroundStyle(.secondary)
            } else {
                Picker("Client", selection: $clientId) {
                    Text("Choose a client…").tag("")
                    ForEach(eligible, id: \.id) { Text($0.displayName).tag($0.id) }
                }
                .accessibilityIdentifier("profile-assign-client")
                Toggle("Locked — the client cannot switch to another profile", isOn: $locked)
                    .accessibilityIdentifier("profile-assign-lock")
            }
            if let refusal = model?.guardRefusal {
                GuardRefusalView(body: refusal) { route in dismiss(); appState.navigate(route) }
            }
            if let message = model?.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            if let done { Label(done, systemImage: "checkmark.circle.fill").font(.caption).foregroundStyle(.green) }
            HStack {
                Spacer()
                Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Assign") { Task { await assign() } }
                    .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                    .disabled(clientId.isEmpty)
                    .accessibilityIdentifier("profile-assign-confirm")
            }
        }
        .padding(20).frame(width: 460)
        .onAppear { model = ClientBindingModel(source: appState.deferredClientSource) }
    }

    private func assign() async {
        guard let model, let client = eligible.first(where: { $0.id == clientId }) else { return }
        done = nil
        // One binding change carries the profile and the wanted lock, so a
        // client already on this profile can have just its lock changed.
        guard let outcome = await model.assign(client, to: profile.name, locked: locked) else { return }
        if case .changed(let row) = outcome,
           let index = appState.clients.firstIndex(where: { $0.id == row.id }) {
            appState.clients[index] = row
        }
        done = outcome.note(displayName: client.displayName, title: profile.displayTitle, locked: locked)
    }
}

/// Stands in when no core connection exists yet: every call fails politely.
struct UnavailableProfileSource: ProfileEditorSource {
    func profile(_ name: String) async throws -> ProfileView { throw APIClientError.notReady }
    func createProfile(_ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse { throw APIClientError.notReady }
    func updateProfile(_ name: String, _ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse { throw APIClientError.notReady }
    func renameProfile(_ name: String, newName: String) async throws -> ProfileRenameResponse { throw APIClientError.notReady }
    func deleteProfile(_ name: String, reassignTo: String?, force: Bool) async throws -> ProfileDeleteResponse { throw APIClientError.notReady }
    func effectiveTools(_ name: String, client: String?, server: String?, reason: String?) async throws -> EffectiveToolsResponse { throw APIClientError.notReady }
    func tryProfile(draft: ProfileConfigPayload, query: String, limit: Int?) async throws -> TryResponse { throw APIClientError.notReady }
}
