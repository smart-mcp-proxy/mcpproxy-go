// ClientBindingControls.swift
// MCPProxy
//
// Spec 108-k (K9, FR-047, FR-026): the profile controls of a Clients row and
// the warnings banner above the list. A row shows a compact binding label and a
// credential badge; its detail carries the profile Picker, the Locked toggle
// and the credential actions. A client WITHOUT an active client credential gets
// no working picker or toggle: they are disabled and replaced by the primary
// button that gets it one ("Connect…", "Upgrade to client credential…" or "Reconnect…").

import SwiftUI

// MARK: - Row label

/// The compact label under a client's name: `Work · Read-only · locked`, plus
/// the credential badge. Every part is text; colour is never the only signal.
struct ClientBindingRowLabel: View {
    let client: ClientPresenceRecord

    var body: some View {
        let state = ClientBindingControlsState(client)
        HStack(spacing: 6) {
            if let label = state.compactLabel {
                HStack(spacing: 3) {
                    if state.isLocked { Image(systemName: "lock.fill") }
                    Text(label)
                }
                .font(.caption)
            }
            Text(state.credentialBadge)
                .font(.caption2)
                .padding(.horizontal, 6).padding(.vertical, 1)
                .background(Color.secondary.opacity(0.15)).clipShape(Capsule())
                .accessibilityIdentifier("client-credential-badge-\(client.id)")
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(state.accessibilityLabel + ", " + state.credentialBadge)
    }
}

// MARK: - Detail controls

struct ClientBindingDetail: View {
    @ObservedObject var appState: AppState
    @ObservedObject var model: ClientBindingModel
    let client: ClientPresenceRecord
    /// A row changed on the server (binding, rotation, …).
    let onUpdate: (ClientPresenceRecord) -> Void
    let onUpgrade: () -> Void
    let onRotate: () -> Void
    let onForget: () -> Void
    let onExplain: () -> Void

    var body: some View {
        let state = ClientBindingControlsState(client)
        VStack(alignment: .leading, spacing: 8) {
            Text("Profile").font(.caption.weight(.semibold))

            if let cta = state.cta {
                // No active client credential: nothing to rebind.
                VStack(alignment: .leading, spacing: 6) {
                    Text(reasonText(for: client))
                        .font(.caption).foregroundStyle(.secondary)
                    Button(cta.title, action: onUpgrade)
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("client-upgrade-\(client.id)")
                    Picker("Profile", selection: .constant(state.profile)) {
                        Text("All servers").tag("")
                    }
                    .disabled(true)
                    .accessibilityIdentifier("client-profile-picker-\(client.id)")
                    .accessibilityLabel(state.accessibilityLabel)
                }
            } else {
                HStack(spacing: 12) {
                    Picker("Profile", selection: Binding(
                        get: { client.boundProfile },
                        set: { chosen in
                            Task { if let row = await model.chooseProfile(client, profile: chosen) { onUpdate(row) } }
                        })) {
                        Text("All servers").tag("")
                        ForEach(appState.profiles) { Text($0.displayTitle).tag($0.name) }
                        if client.profileMissing == true {
                            Text("\(client.boundProfile) (missing)").tag(client.boundProfile)
                        }
                    }
                    .frame(maxWidth: 280)
                    .disabled(model.busyClientId == client.id)
                    .accessibilityLabel(state.accessibilityLabel)
                    .accessibilityIdentifier("client-profile-picker-\(client.id)")

                    VStack(alignment: .leading, spacing: 2) {
                        Toggle("Locked", isOn: Binding(
                            get: { state.isLocked },
                            set: { locked in
                                Task { if let row = await model.setLocked(client, locked: locked) { onUpdate(row) } }
                            }))
                            .disabled(!state.lockEnabled || model.busyClientId == client.id)
                            .accessibilityLabel("\(client.displayName) lock, \(state.isLocked ? "locked" : "switchable")")
                            .accessibilityIdentifier("client-lock-toggle-\(client.id)")
                        if let help = state.lockHelp {
                            Text(help).font(.caption2).foregroundStyle(.secondary)
                        }
                    }
                }
                if let source = state.sourceLabel {
                    Text(source).font(.caption2).foregroundStyle(.secondary)
                }
            }

            credentialLines(state)

            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
            }

            HStack(spacing: 10) {
                if state.canBind {
                    Button("Rotate…", action: onRotate).accessibilityIdentifier("client-rotate-\(client.id)")
                    if client.rotationPending == true {
                        Button("Finalize rotation") {
                            Task { if let row = await model.finalizeRotation(client) { onUpdate(row) } }
                        }
                        .accessibilityIdentifier("client-finalize-\(client.id)")
                    }
                }
                Button("Forget…", role: .destructive, action: onForget)
                    .disabled(client.tokenName == nil && !state.canBind)
                    .accessibilityIdentifier("client-forget-\(client.id)")
                Button("Explain access…", action: onExplain)
                    .accessibilityIdentifier("client-explain-\(client.id)")
            }
            .font(.caption)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("client-binding-\(client.id)")
    }

    @ViewBuilder
    private func credentialLines(_ state: ClientBindingControlsState) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Credential: \(state.credentialBadge)" + (client.tokenName.map { " (\($0))" } ?? ""))
                .font(.caption)
            if let expires = client.expiresAt, !expires.isEmpty {
                Text("Expires: \(Self.formatted(expires))").font(.caption2).foregroundStyle(.secondary)
            }
            if client.rotationPending == true {
                Text("Rotation pending — the old secret stays valid until you finalize (or for 24 hours).")
                    .font(.caption2).foregroundStyle(.orange)
            }
            let blocked = client.blocked24h ?? 0
            HStack(spacing: 6) {
                Text("Blocked (24h): \(blocked)").font(.caption2).foregroundStyle(.secondary)
                if blocked > 0 && appState.scopeFiltersAvailable {
                    Button("Show blocked activity") {
                        var filter = ScopeFilter.forClient(client.id)
                        filter.status = "blocked"
                        appState.openActivity(with: filter)
                    }
                    .buttonStyle(.link).font(.caption2)
                    .accessibilityIdentifier("client-blocked-link-\(client.id)")
                }
            }
        }
    }

    private func reasonText(for client: ClientPresenceRecord) -> String {
        switch client.credentialState {
        case .some(.adminKey): return "This client holds the admin API key, so no profile can limit it."
        case .some(.revoked): return "Its credential was revoked. Reconnect it to bind a profile."
        case .some(.expired): return "Its credential expired. Reconnect it to bind a profile."
        case .some(.none): return "This client has no credential, so no profile applies to it."
        default: return "MCPProxy has not checked this client’s credential yet."
        }
    }

    static func formatted(_ iso: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: iso) else { return iso }
        return date.formatted(date: .abbreviated, time: .omitted)
    }
}

// MARK: - Warnings banner

/// One row per warning above the Clients list: a symbol AND the severity word,
/// the message and a fix button that only navigates.
struct ClientWarningsBanner: View {
    let warnings: [ClientWarning]
    let onRoute: (AppRoute) -> Void

    var body: some View {
        if !warnings.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(warnings) { warning in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Image(systemName: warning.severity.symbolName)
                            .foregroundStyle(warning.severity == .info ? Color.blue : Color.orange)
                        Text(warning.severity.word).font(.caption.weight(.semibold))
                        Text(warning.message).font(.caption)
                        Spacer()
                        if let title = ClientWarningNavigation.buttonTitle(for: warning),
                           let route = ClientWarningNavigation.route(for: warning) {
                            Button(title) { onRoute(route) }
                                .font(.caption)
                                .accessibilityIdentifier("client-warning-fix-\(warning.code)")
                        }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityLabel("\(warning.severity.word): \(warning.message)")
                }
            }
            .padding(.horizontal).padding(.vertical, 8)
            .background(Color.orange.opacity(0.1))
            .accessibilityIdentifier("clients-warnings-banner")
        }
    }
}
