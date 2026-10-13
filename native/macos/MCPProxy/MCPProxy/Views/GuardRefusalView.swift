// GuardRefusalView.swift
// MCPProxy
//
// Spec 108-k (K13, FR-008a): how a `409 binding_bypassable_without_auth` is
// shown. The refusal says which client bindings could escape their profile
// while authentication is off, and offers one button per `fixes[]` entry. A fix
// button only NAVIGATES (to Settings, with the control focused or preselected);
// it never changes configuration. The operator reviews the change there and
// saves it.

import SwiftUI

struct GuardRefusalView: View {
    let error: String
    let bindings: [BindingRef]
    let fixes: [GuardFix]
    /// Follow a fix. The default hands the route to `AppState`.
    let onRoute: (AppRoute) -> Void

    init(body: ServiceErrorBody, onRoute: @escaping (AppRoute) -> Void) {
        self.error = body.error
        self.bindings = body.bindings ?? []
        self.fixes = body.fixes ?? []
        self.onRoute = onRoute
    }

    init(error: String, bindings: [BindingRef], fixes: [GuardFix], onRoute: @escaping (AppRoute) -> Void) {
        self.error = error
        self.bindings = bindings
        self.fixes = fixes
        self.onRoute = onRoute
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label {
                Text("Not saved — " + error).font(.callout)
            } icon: {
                Image(systemName: "exclamationmark.shield.fill").foregroundStyle(.orange)
            }
            if !bindings.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(bindings.enumerated()), id: \.offset) { _, binding in
                        Text("\(binding.clientId) → \(binding.profile.isEmpty ? "All servers" : binding.profile) (\(binding.mode == .locked ? "locked" : "switchable"))")
                            .font(.caption.monospaced())
                    }
                }
            }
            HStack {
                ForEach(fixes, id: \.self) { fix in
                    if let title = ClientWarningNavigation.buttonTitle(for: fix),
                       let route = ClientWarningNavigation.route(for: fix) {
                        Button(title) { onRoute(route) }
                            .accessibilityIdentifier("guard-fix-\(fix.kind)")
                    }
                }
            }
            Text("These buttons only open Settings; nothing is changed until you save there.")
                .font(.caption2).foregroundStyle(.secondary)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.1))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Change refused: \(error)")
        .accessibilityIdentifier("guard-refusal")
    }
}
