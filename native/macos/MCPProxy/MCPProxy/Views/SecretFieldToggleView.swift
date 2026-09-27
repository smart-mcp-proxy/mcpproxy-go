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
    @Environment(\.fontScale) var fontScale

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(name)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
                Picker("", selection: $mode) {
                    Text("Value").tag(SecretFieldInput.Mode.value)
                    Text("Secret").tag(SecretFieldInput.Mode.secret)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 140)
                .accessibilityIdentifier("secret-toggle-mode-\(name)")
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
