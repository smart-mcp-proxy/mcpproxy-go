// SecretsView.swift
// MCPProxy
//
// Displays secrets stored in the system keyring and environment variables
// referenced in server configurations. Matches the Web UI Secrets page.
//
// Uses the /api/v1/secrets/config endpoint which returns the actual response
// shape with secret_ref objects and is_set booleans.

import SwiftUI

// MARK: - Secret Models (matches /api/v1/secrets/config response)

/// Reference info for a single secret in the configuration.
struct SecretRefInfo: Codable, Equatable {
    let type: String
    let name: String
    let original: String
}

/// A secret entry as returned by the secrets config endpoint.
struct ConfigSecret: Codable, Identifiable, Equatable {
    var id: String { secretRef.name }

    let secretRef: SecretRefInfo
    let isSet: Bool

    enum CodingKeys: String, CodingKey {
        case secretRef = "secret_ref"
        case isSet = "is_set"
    }
}

/// The data payload inside the APIResponse wrapper for /api/v1/secrets/config.
struct ConfigSecretsResponse: Codable {
    let secrets: [ConfigSecret]?
    let environmentVars: [ConfigSecret]?
    let totalSecrets: Int?
    let totalEnvVars: Int?

    enum CodingKeys: String, CodingKey {
        case secrets
        case environmentVars = "environment_vars"
        case totalSecrets = "total_secrets"
        case totalEnvVars = "total_env_vars"
    }
}

// MARK: - Secrets View

struct SecretsView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale
    @State private var secrets: [ConfigSecret] = []
    @State private var envVars: [ConfigSecret] = []
    @State private var isLoading = false
    @State private var searchText = ""
    @State private var filterType = "all"
    @State private var showAddSheet = false
    @State private var newSecretName = ""
    @State private var newSecretValue = ""
    @State private var errorMessage: String?

    private var apiClient: APIClient? { appState.apiClient }

    /// All entries combined for display.
    private var allEntries: [ConfigSecret] {
        secrets + envVars
    }

    private var filteredEntries: [ConfigSecret] {
        var result: [ConfigSecret]
        switch filterType {
        case "keyring":
            result = secrets
        case "env":
            result = envVars
        case "missing":
            result = allEntries.filter { !$0.isSet }
        default:
            result = allEntries
        }
        if !searchText.isEmpty {
            result = result.filter { $0.secretRef.name.localizedCaseInsensitiveContains(searchText) }
        }
        return result
    }

    private var keyringCount: Int { secrets.count }
    private var envCount: Int { envVars.count }
    private var missingCount: Int { allEntries.filter { !$0.isSet }.count }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Secrets & Environment Variables")
                    .font(.scaled(.title2, scale: fontScale).bold())
                Spacer()

                Button {
                    Task { await loadSecrets() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)

                Button("Add Secret") {
                    showAddSheet = true
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("secrets-add-button")
            }
            .padding()

            // Stats bar
            HStack(spacing: 20) {
                StatBadge(label: "Keyring", value: "\(keyringCount)", color: .blue)
                StatBadge(label: "Env Vars", value: "\(envCount)", color: .green)
                StatBadge(label: "Missing", value: "\(missingCount)", color: .red)
                Spacer()
            }
            .padding(.horizontal)
            .padding(.bottom, 8)

            // Filter bar
            HStack {
                Picker("Filter", selection: $filterType) {
                    Text("All (\(allEntries.count))").tag("all")
                    Text("Keyring (\(keyringCount))").tag("keyring")
                    Text("Env Vars (\(envCount))").tag("env")
                    Text("Missing (\(missingCount))").tag("missing")
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 500)
                .accessibilityIdentifier("secrets-filter")

                Spacer()

                TextField("Search secrets...", text: $searchText)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 200)
            }
            .padding(.horizontal)
            .padding(.bottom, 8)

            Divider()

            if isLoading {
                ProgressView("Loading secrets...")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if filteredEntries.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "key")
                        .font(.system(size: 48 * fontScale))
                        .foregroundStyle(.tertiary)
                    Text("No secrets found")
                        .font(.scaled(.title3, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(filteredEntries) { entry in
                        SecretRow(entry: entry, appState: appState, onDelete: {
                            Task { await loadSecrets() }
                        })
                        .accessibilityIdentifier("secret-row-\(entry.id)")
                    }
                }
                .accessibilityIdentifier("secrets-list")
            }
        }
        .sheet(isPresented: $showAddSheet) {
            addSecretSheet
        }
        .task { await loadSecrets() }
    }

    private var addSecretSheet: some View {
        VStack(spacing: 16) {
            Text("Add Secret to Keyring")
                .font(.scaled(.headline, scale: fontScale))

            TextField("Secret Name", text: $newSecretName)
                .textFieldStyle(.roundedBorder)

            SecureField("Secret Value", text: $newSecretValue)
                .textFieldStyle(.roundedBorder)

            if let error = errorMessage {
                Text(error)
                    .foregroundStyle(.red)
                    .font(.scaled(.caption, scale: fontScale))
            }

            HStack {
                Button("Cancel") {
                    showAddSheet = false
                    newSecretName = ""
                    newSecretValue = ""
                    errorMessage = nil
                }
                .keyboardShortcut(.cancelAction)

                Spacer()

                Button("Save") {
                    Task { await addSecret() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(newSecretName.isEmpty || newSecretValue.isEmpty)
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding()
        .frame(width: 400)
    }

    private func loadSecrets() async {
        isLoading = true
        defer { isLoading = false }
        guard let client = apiClient else { return }
        do {
            let data = try await client.fetchRaw(path: "/api/v1/secrets/config")
            let decoder = JSONDecoder()
            // Try the standard APIResponse wrapper first
            if let wrapper = try? decoder.decode(APIResponse<ConfigSecretsResponse>.self, from: data),
               let payload = wrapper.data {
                secrets = payload.secrets ?? []
                envVars = payload.environmentVars ?? []
            } else if let direct = try? decoder.decode(ConfigSecretsResponse.self, from: data) {
                secrets = direct.secrets ?? []
                envVars = direct.environmentVars ?? []
            }
        } catch {}
    }

    private func addSecret() async {
        guard let client = apiClient else { return }
        do {
            let body: [String: Any] = ["name": newSecretName, "value": newSecretValue]
            _ = try await client.postRaw(path: "/api/v1/secrets", body: body)
            showAddSheet = false
            newSecretName = ""
            newSecretValue = ""
            errorMessage = nil
            await loadSecrets()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

// MARK: - Secret Row

struct SecretRow: View {
    let entry: ConfigSecret
    @ObservedObject var appState: AppState
    let onDelete: () -> Void
    @Environment(\.fontScale) var fontScale

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        HStack(spacing: 12) {
            // Type badge
            Text(entry.secretRef.type.capitalized)
                .font(.scaled(.caption2, scale: fontScale).bold())
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(entry.secretRef.type == "keyring" ? Color.blue.opacity(0.2) : Color.green.opacity(0.2))
                .foregroundStyle(entry.secretRef.type == "keyring" ? .blue : .green)
                .cornerRadius(4)

            // Status indicator
            Circle()
                .fill(entry.isSet ? Color.green : Color.red)
                .frame(width: 8, height: 8)

            VStack(alignment: .leading, spacing: 2) {
                Text(entry.secretRef.name)
                    .font(.scaled(.headline, scale: fontScale))
                Text(entry.secretRef.original)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            Spacer()

            if !entry.isSet {
                Label("Missing", systemImage: "exclamationmark.triangle.fill")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.red)
            }

            if entry.secretRef.type == "keyring" {
                Button(role: .destructive) {
                    Task {
                        try? await apiClient?.deleteAction(path: "/api/v1/secrets/\(entry.secretRef.name)")
                        onDelete()
                    }
                } label: {
                    Image(systemName: "trash")
                        .foregroundStyle(.red)
                }
                .buttonStyle(.borderless)
            }
        }
        .padding(.vertical, 4)
    }
}

// MARK: - Stat Badge

struct StatBadge: View {
    let label: String
    let value: String
    let color: Color
    @Environment(\.fontScale) var fontScale

    var body: some View {
        VStack(spacing: 2) {
            Text(value)
                .font(.scaled(.title3, scale: fontScale).bold())
                .foregroundStyle(color)
            Text(label)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(.quaternary)
        .cornerRadius(8)
    }
}
