// AnonymousProfileSection.swift
// MCPProxy
//
// Spec 108-k (K20, FR-049, parity row 23): Settings → Security → "Anonymous
// callers". Which profile a caller that sends NO credential gets. It is a
// custom section (its options are the live profile list), not a catalogue
// field. Saving writes `anonymous_profile` through `PATCH /config`; a change
// that would let a bound client escape its profile while authentication is off
// is refused with a guard (shown by `GuardRefusalView`).

import SwiftUI

/// The narrow API surface of the section; `APIClient` conforms.
protocol AnonymousProfileSource: Sendable {
    func patchAnonymousProfile(_ value: String) async throws
}

extension APIClient: AnonymousProfileSource {
    /// `PATCH /api/v1/config {"anonymous_profile": value}`, with structured
    /// errors (a 409 carries the guard's `bindings` and `fixes`).
    func patchAnonymousProfile(_ value: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["anonymous_profile": value])
        let (data, response) = try await rawRequest(path: "/api/v1/config", method: "PATCH", body: body)
        guard (200...299).contains(response.statusCode) else {
            throw Self.serviceError(status: response.statusCode, data: data)
        }
        if let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let success = root["success"] as? Bool, !success {
            throw Self.serviceError(status: response.statusCode, data: data)
        }
    }
}

@MainActor
final class AnonymousProfileModel: ObservableObject {
    /// "" is unconfined (All servers).
    @Published var selection: String
    @Published private(set) var saved: String
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var errorMessage: String?
    @Published private(set) var isSaving = false
    @Published private(set) var savedNote: String?

    private let source: AnonymousProfileSource

    init(source: AnonymousProfileSource, current: String) {
        self.source = source
        self.saved = current
        self.selection = current
    }

    var isDirty: Bool { selection != saved }

    /// The core's value changed (another surface saved it): follow it unless the
    /// operator is mid-edit.
    func adopt(current: String) {
        if !isDirty { selection = current }
        saved = current
    }

    /// A fix button asked for this value: PRESELECT it, never save it. The
    /// operator reviews the change and presses Save.
    func preselect(_ value: String?) {
        guard let value else { return }
        selection = value
        guardRefusal = nil
        errorMessage = nil
        savedNote = nil
    }

    func revert() {
        selection = saved
        guardRefusal = nil
        errorMessage = nil
    }

    /// `PATCH /config {"anonymous_profile": selection}`; unconfined is `""`.
    func save() async {
        guard isDirty, !isSaving else { return }
        isSaving = true
        guardRefusal = nil
        errorMessage = nil
        savedNote = nil
        defer { isSaving = false }
        do {
            try await source.patchAnonymousProfile(selection)
            saved = selection
            savedNote = selection.isEmpty
                ? "Anonymous callers now reach All servers."
                : "Anonymous callers are now confined to \(selection)."
        } catch {
            if case APIClientError.service(_, let body) = error, body.isGuardRefusal {
                guardRefusal = body
            } else if case APIClientError.service(_, let body) = error {
                errorMessage = body.error
            } else {
                errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// What the setting means, which depends on whether authentication is on
    /// (the same two texts as the Web UI).
    static func explanation(requireMCPAuth: Bool) -> String {
        requireMCPAuth
            ? "Anonymous callers are refused because authentication is required. This setting applies only if you turn authentication off."
            : "Callers that send no credential get this profile. If any client is bound to a profile, anonymous callers must not reach more than it, or they are denied everything (binding guard)."
    }
}

struct AnonymousProfileSection: View {
    @ObservedObject var appState: AppState
    @ObservedObject var store: ConfigStore
    /// A preselect request from a fix button; applied, never saved.
    let preselect: String?

    @StateObject private var model: AnonymousProfileModel

    init(appState: AppState, store: ConfigStore, preselect: String?) {
        self.appState = appState
        self.store = store
        self.preselect = preselect
        let source: AnonymousProfileSource = appState.apiClient ?? UnavailableAnonymousSource()
        _model = StateObject(wrappedValue: AnonymousProfileModel(source: source, current: appState.anonymousProfile))
    }

    private var requireMCPAuth: Bool { (store.value("require_mcp_auth") as? Bool) ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Anonymous callers").font(.headline)
            HStack {
                Picker("Anonymous callers", selection: $model.selection) {
                    Text("Unconfined (all servers)").tag("")
                    ForEach(appState.profiles) { Text($0.displayTitle).tag($0.name) }
                    if !model.selection.isEmpty, !appState.profiles.contains(where: { $0.name == model.selection }) {
                        Text("\(model.selection) (missing)").tag(model.selection)
                    }
                }
                .labelsHidden()
                .frame(maxWidth: 300)
                .accessibilityLabel("Anonymous callers profile")
                .accessibilityIdentifier("settings-anonymous-profile")
                Spacer()
                if model.isDirty { Button("Discard") { model.revert() }.buttonStyle(.borderless) }
                Button {
                    Task {
                        await model.save()
                        if model.savedNote != nil {
                            appState.anonymousProfile = model.selection
                            await store.load()
                        }
                    }
                } label: {
                    if model.isSaving { ProgressView().controlSize(.small) } else { Text("Save") }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.isDirty || model.isSaving)
                .accessibilityIdentifier("settings-anonymous-profile-save")
            }
            Text(AnonymousProfileModel.explanation(requireMCPAuth: requireMCPAuth))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { appState.navigate($0) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
            }
            if let note = model.savedNote {
                Label(note, systemImage: "checkmark.circle.fill").font(.caption).foregroundStyle(.green)
            }
        }
        .padding(.vertical, 10)
        .onAppear { model.preselect(preselect) }
        .onChange(of: preselect) { model.preselect($0) }
        .onChange(of: appState.anonymousProfile) { model.adopt(current: $0) }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("settings-anonymous-section")
    }
}

private struct UnavailableAnonymousSource: AnonymousProfileSource {
    func patchAnonymousProfile(_ value: String) async throws { throw APIClientError.notReady }
}
