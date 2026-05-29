// SettingsView.swift
// MCPProxy
//
// Native Settings window for the menu-bar app. Per macOS HIG, the tray owns
// only its OWN concerns (launch-at-login, interface size) plus the connection
// bootstrap (endpoint/status); all backend configuration lives in the core and
// is edited via the web UI ("Open full configuration…"). The app stays a
// stateless controller (Constitution III) — it does not persist backend config.

import SwiftUI
import ServiceManagement

struct SettingsView: View {
    @ObservedObject var appState: AppState
    /// Opens the web UI in the browser (with the session API key). Wired by the
    /// AppController to its existing openWebUI action.
    var onOpenWebUI: () -> Void

    var body: some View {
        TabView {
            GeneralSettingsTab(appState: appState, onOpenWebUI: onOpenWebUI)
                .tabItem { Label("General", systemImage: "gearshape") }

            ConnectionSettingsTab(appState: appState, onOpenWebUI: onOpenWebUI)
                .tabItem { Label("Connection", systemImage: "network") }
        }
        .frame(width: 480)
    }
}

// MARK: - General

private struct GeneralSettingsTab: View {
    @ObservedObject var appState: AppState
    var onOpenWebUI: () -> Void

    @State private var launchAtLogin = AutoStartService.isEnabled
    @State private var launchError: String?

    var body: some View {
        Form {
            Section {
                Toggle("Launch MCPProxy at login", isOn: $launchAtLogin)
                    .onChange(of: launchAtLogin) { newValue in applyLaunchAtLogin(newValue) }
                if let launchError {
                    Text(launchError).font(.callout).foregroundColor(.red)
                }
            } header: {
                Text("Startup")
            }

            Section {
                HStack {
                    Text("Interface text size")
                    Spacer()
                    Stepper(value: $appState.fontScale, in: 0.8...1.6, step: 0.1) {
                        Text("\(Int(appState.fontScale * 100))%")
                            .monospacedDigit()
                            .frame(width: 48, alignment: .trailing)
                    }
                }
            } header: {
                Text("Appearance")
            }

            Section {
                Button("Open full configuration in browser…", action: onOpenWebUI)
            } header: {
                Text("Configuration")
            } footer: {
                Text("Upstream servers, security, quarantine, tokens and all other settings are managed in the web UI.")
                    .font(.callout)
                    .foregroundColor(.secondary)
            }

            Section {
                LabeledContent("Version", value: appState.version)
            } header: {
                Text("About")
            }
        }
        .formStyle(.grouped)
        .padding(.vertical, 8)
        .frame(width: 480)
        .onAppear { launchAtLogin = AutoStartService.isEnabled }
    }

    private func applyLaunchAtLogin(_ enabled: Bool) {
        do {
            if enabled {
                try AutoStartService.enable()
            } else {
                try AutoStartService.disable()
            }
            launchError = nil
            appState.autoStartEnabled = AutoStartService.isEnabled
            AutostartSidecarService.refresh()
        } catch {
            // Revert the toggle to the true system state on failure.
            launchAtLogin = AutoStartService.isEnabled
            launchError = error.localizedDescription
        }
    }
}

// MARK: - Connection

private struct ConnectionSettingsTab: View {
    @ObservedObject var appState: AppState
    var onOpenWebUI: () -> Void

    var body: some View {
        Form {
            Section {
                LabeledContent("Endpoint", value: appState.webUIBaseURL)
                LabeledContent("Status") {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(statusColor)
                            .frame(width: 8, height: 8)
                        Text(statusText)
                    }
                }
            } header: {
                Text("Core Connection")
            } footer: {
                Text("MCPProxy starts and manages the core automatically; the API key is generated per session. Use the web UI for full access.")
                    .font(.callout)
                    .foregroundColor(.secondary)
            }

            Section {
                Button("Open Web UI…", action: onOpenWebUI)
            }
        }
        .formStyle(.grouped)
        .padding(.vertical, 8)
        .frame(width: 480)
    }

    private var statusText: String {
        appState.isConnected ? "Connected" : "Not connected"
    }

    private var statusColor: Color {
        appState.isConnected ? .green : .secondary
    }
}
