// SettingsView.swift
// MCPProxy
//
// Native Settings window. The tray is a full alternative client to the core:
// every backend setting is edited here over REST (GET/PATCH /api/v1/config),
// mirroring the web UI Configuration page — the config JSON file is never read
// or written directly. The "App" tab holds the few OS-level prefs that are
// genuinely the app's own concern (launch-at-login, interface size).

import SwiftUI
import ServiceManagement

struct SettingsView: View {
    @ObservedObject var appState: AppState
    @StateObject private var store: ConfigStore
    @State private var tab = 0

    init(appState: AppState) {
        self.appState = appState
        _store = StateObject(wrappedValue: ConfigStore(appState: appState))
    }

    var body: some View {
        TabView(selection: $tab) {
            AppPrefsTab(appState: appState)
                .tabItem { Label("App", systemImage: "macwindow") }.tag(0)

            SecuritySettingsTab(store: store)
                .tabItem { Label("Security", systemImage: "lock.shield") }.tag(1)

            GeneralConfigTab(store: store)
                .tabItem { Label("General", systemImage: "gearshape") }.tag(2)

            AdvancedSettingsTab(store: store)
                .tabItem { Label("Advanced", systemImage: "slider.horizontal.3") }.tag(3)
        }
        .frame(minWidth: 540, minHeight: 560)
        // ⌘1–⌘4 switch tabs (handy, and lets UI tests navigate).
        .background {
            ForEach(0..<4, id: \.self) { i in
                Button("") { tab = i }
                    .keyboardShortcut(KeyEquivalent(Character(String(i + 1))), modifiers: .command)
                    .opacity(0)
            }
        }
    }
}

// MARK: - App preferences (OS-level, app-owned)

private struct AppPrefsTab: View {
    @ObservedObject var appState: AppState
    @State private var launchAtLogin = AutoStartService.isEnabled
    @State private var launchError: String?

    var body: some View {
        Form {
            Section {
                Toggle("Launch MCPProxy at login", isOn: $launchAtLogin)
                    .onChange(of: launchAtLogin) { applyLaunchAtLogin($0) }
                if let launchError {
                    Text(launchError).font(.callout).foregroundColor(.red)
                }
            } header: { Text("Startup") }

            Section {
                HStack {
                    Text("Interface text size")
                    Spacer()
                    Stepper(value: $appState.fontScale, in: 0.8...1.6, step: 0.1) {
                        Text("\(Int(appState.fontScale * 100))%").monospacedDigit().frame(width: 48, alignment: .trailing)
                    }
                }
            } header: { Text("Appearance") }

            Section {
                LabeledContent("Version", value: appState.version)
                LabeledContent("Core") {
                    HStack(spacing: 6) {
                        Circle().fill(appState.isConnected ? .green : .secondary).frame(width: 8, height: 8)
                        Text(appState.isConnected ? "Connected" : "Not connected")
                    }
                }
            } header: { Text("About") } footer: {
                Text("All server configuration is managed in the Security, General and Advanced tabs.")
                    .font(.callout).foregroundColor(.secondary)
            }
        }
        .formStyle(.grouped)
        .padding(.vertical, 8)
        .onAppear { launchAtLogin = AutoStartService.isEnabled }
    }

    private func applyLaunchAtLogin(_ enabled: Bool) {
        do {
            if enabled { try AutoStartService.enable() } else { try AutoStartService.disable() }
            launchError = nil
            appState.autoStartEnabled = AutoStartService.isEnabled
            AutostartSidecarService.refresh()
        } catch {
            launchAtLogin = AutoStartService.isEnabled
            launchError = error.localizedDescription
        }
    }
}
