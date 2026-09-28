// SecretFieldToggleView.swift
// MCPProxy
//
// One required-input row in the Add Server sheet's Catalog/Paste tabs
// (Spec 109 FR-065): a name, a Value/Secret segmented toggle, and the field
// itself (a SecureField in Secret mode). Mirrors the Web UI's
// `SecretToggle.vue`.

import SwiftUI

struct SecretFieldToggleView: View {
    let name: String
    @Binding var value: String
    @Binding var mode: SecretFieldInput.Mode
    let keyringAvailable: Bool
    let keyringReason: String
    @Environment(\.fontScale) var fontScale

    /// Value mode is always safe: it lets a secret-like field recover from a
    /// keyring outage. Secret mode is only selectable while the keyring can
    /// accept the value, preserving the add-time fail-closed guard.
    static func canSelect(mode: SecretFieldInput.Mode, keyringAvailable: Bool) -> Bool {
        mode == .value || keyringAvailable
    }

    private var modeSelection: Binding<SecretFieldInput.Mode> {
        Binding(
            get: { mode },
            set: { proposedMode in
                guard Self.canSelect(mode: proposedMode, keyringAvailable: keyringAvailable) else { return }
                mode = proposedMode
            }
        )
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(name)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
                Picker("", selection: modeSelection) {
                    Text("Value").tag(SecretFieldInput.Mode.value)
                    Text("Secret").tag(SecretFieldInput.Mode.secret)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 140)
                .accessibilityIdentifier("secret-toggle-mode-\(name)")
            }
            if !keyringAvailable {
                Text(keyringReason.isEmpty ? "OS keyring unavailable" : keyringReason)
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.orange)
                    .accessibilityIdentifier("secret-toggle-keyring-unavailable-\(name)")
            }
            if mode == .secret {
                SecureField("Secret value", text: $value)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("secret-toggle-value-\(name)")
            } else {
                TextField("Value", text: $value)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("secret-toggle-value-\(name)")
            }
        }
    }
}
