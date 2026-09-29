import SwiftUI

/// The native Clients hub keeps connection, endpoint terminology, and agent
/// tokens together. ConnectClientView remains the single author of the
/// preview-before-write workflow.
struct ClientsView: View {
    @ObservedObject var appState: AppState
    @State private var tab = 0

    var body: some View {
        VStack(spacing: 0) {
            Picker("Clients section", selection: $tab) {
                Text("Clients").tag(0)
                Text("Endpoint & Mode").tag(1)
                Text("Agent Tokens").tag(2)
            }
            .pickerStyle(.segmented)
            .padding()
            Divider()
            if tab == 0 {
                ConnectClientView(model: ConnectClientModel(source: DeferredConnectSource { [weak appState] in appState?.apiClient }))
            } else if tab == 1 {
                VStack(alignment: .leading, spacing: 12) {
                    Text("Endpoint & Mode").font(.title2.bold())
                    Text("Choose how MCPProxy routes tools in Settings. Restart MCPProxy after changing the routing mode.")
                        .foregroundStyle(.secondary)
                    Spacer()
                }.padding()
            } else {
                TokensView(appState: appState)
            }
        }
        .accessibilityIdentifier("clients-view")
    }
}
