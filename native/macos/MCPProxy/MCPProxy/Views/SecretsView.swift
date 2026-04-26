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
    @State private var lastStoredReference: String?
    @State private var lastStoredName: String?
    @State private var showConfigFirstHint = true
    @State private var successBannerTask: Task<Void, Never>?

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

            // Success banner — shown after a secret is stored, so the user
            // can see the reference syntax even though the secret won't
            // appear in the list until referenced from a server config.
            if let ref = lastStoredReference, let name = lastStoredName {
                successBanner(name: name, reference: ref)
                    .padding(.horizontal)
                    .padding(.bottom, 8)
                    .accessibilityIdentifier("secrets-success-banner")
            }

            // Config-First workflow hint — explains that secrets only show
            // up here once they're referenced from a server config.
            if showConfigFirstHint {
                configFirstHintBanner
                    .padding(.horizontal)
                    .padding(.bottom, 8)
                    .accessibilityIdentifier("secrets-config-first-hint")
            }

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
                .accessibilityIdentifier("secrets-add-name")

            SecureField("Secret Value", text: $newSecretValue)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("secrets-add-value")

            // Live reference preview so the user knows the exact string to
            // paste into their server config to actually use the secret.
            if !newSecretName.isEmpty {
                HStack(spacing: 6) {
                    Image(systemName: "info.circle")
                        .foregroundStyle(.blue)
                    Text("Reference in config:")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text("${keyring:\(newSecretName)}")
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(Color.blue.opacity(0.08))
                .cornerRadius(6)
                .accessibilityIdentifier("secrets-add-reference-preview")
            }

            // Reminder about the config-first workflow so users aren't
            // surprised when their newly-saved secret doesn't appear in
            // the list below.
            Text("Tip: secrets only appear in this list once a server config references them.")
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)

            if let error = errorMessage {
                Text(error)
                    .foregroundStyle(.red)
                    .font(.scaled(.caption, scale: fontScale))
                    .accessibilityIdentifier("secrets-add-error")
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
            let storedName = newSecretName
            showAddSheet = false
            newSecretName = ""
            newSecretValue = ""
            errorMessage = nil
            // Surface success: a freshly stored secret won't appear in the
            // /api/v1/secrets/config list until a server config references
            // it, so without this banner the user sees nothing happen and
            // assumes the save failed.
            showSuccessBanner(name: storedName)
            await loadSecrets()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func showSuccessBanner(name: String) {
        lastStoredName = name
        lastStoredReference = "${keyring:\(name)}"
        successBannerTask?.cancel()
        successBannerTask = Task { @MainActor in
            try? await Task.sleep(nanoseconds: 12_000_000_000) // 12s
            if !Task.isCancelled {
                lastStoredReference = nil
                lastStoredName = nil
            }
        }
    }

    private func successBanner(name: String, reference: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .font(.scaled(.title3, scale: fontScale))
            VStack(alignment: .leading, spacing: 4) {
                Text("Stored \"\(name)\" in Keychain")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                HStack(spacing: 6) {
                    Text("Reference in your server config:")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text(reference)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                    Button {
                        let pb = NSPasteboard.general
                        pb.clearContents()
                        pb.setString(reference, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                    }
                    .buttonStyle(.borderless)
                    .help("Copy reference")
                    .accessibilityIdentifier("secrets-success-copy")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                successBannerTask?.cancel()
                lastStoredReference = nil
                lastStoredName = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.tertiary)
            }
            .buttonStyle(.borderless)
            .help("Dismiss")
        }
        .padding(10)
        .background(Color.green.opacity(0.10))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color.green.opacity(0.35), lineWidth: 1)
        )
        .cornerRadius(8)
    }

    private var configFirstHintBanner: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "info.circle.fill")
                .foregroundStyle(.blue)
                .font(.scaled(.title3, scale: fontScale))
            VStack(alignment: .leading, spacing: 2) {
                Text("Config-first workflow")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                Text("This list shows secrets that are referenced from a server config (\u{0024}{keyring:NAME}). Adding a secret here stores its value — to make a server use it, also add the reference to that server's env block.")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .lineLimit(nil)
                    .multilineTextAlignment(.leading)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                showConfigFirstHint = false
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.tertiary)
            }
            .buttonStyle(.borderless)
            .help("Hide hint")
        }
        .padding(10)
        .background(Color.blue.opacity(0.08))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color.blue.opacity(0.25), lineWidth: 1)
        )
        .cornerRadius(8)
    }
}

// MARK: - Secret Row

struct SecretRow: View {
    let entry: ConfigSecret
    @ObservedObject var appState: AppState
    let onDelete: () -> Void
    @Environment(\.fontScale) var fontScale

    @State private var showDeleteConfirmation = false
    @State private var showSetValueSheet = false
    @State private var secretValue = ""
    @State private var isSaving = false

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
                // Set/Update value button
                Button {
                    secretValue = ""
                    showSetValueSheet = true
                } label: {
                    Image(systemName: entry.isSet ? "pencil" : "plus.circle.fill")
                        .foregroundColor(entry.isSet ? .secondary : .blue)
                }
                .buttonStyle(.borderless)
                .help(entry.isSet ? "Update secret value" : "Set secret value")

                // Delete button with confirmation
                Button(role: .destructive) {
                    showDeleteConfirmation = true
                } label: {
                    Image(systemName: "trash")
                        .foregroundStyle(.red)
                }
                .buttonStyle(.borderless)
                .help("Delete secret")
            }
        }
        .padding(.vertical, 4)
        .alert("Delete Secret", isPresented: $showDeleteConfirmation) {
            Button("Cancel", role: .cancel) { }
            Button("Delete", role: .destructive) {
                Task {
                    try? await apiClient?.deleteAction(path: "/api/v1/secrets/\(entry.secretRef.name)")
                    onDelete()
                }
            }
        } message: {
            Text("Are you sure you want to delete \"\(entry.secretRef.name)\"? This action cannot be undone.")
        }
        .sheet(isPresented: $showSetValueSheet) {
            VStack(spacing: 16) {
                Text(entry.isSet ? "Update Secret" : "Set Secret Value")
                    .font(.headline)

                Text(entry.secretRef.name)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)

                SecureField("Enter secret value...", text: $secretValue)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 300)

                HStack {
                    Button("Cancel") {
                        showSetValueSheet = false
                    }
                    .keyboardShortcut(.cancelAction)

                    Button(isSaving ? "Saving..." : "Save") {
                        Task {
                            isSaving = true
                            try? await apiClient?.postAction(
                                path: "/api/v1/secrets",
                                body: ["name": entry.secretRef.name, "value": secretValue, "type": "keyring"]
                            )
                            isSaving = false
                            showSetValueSheet = false
                            secretValue = ""
                            onDelete() // Triggers reload
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(secretValue.isEmpty || isSaving)
                }
            }
            .padding(24)
        }
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
