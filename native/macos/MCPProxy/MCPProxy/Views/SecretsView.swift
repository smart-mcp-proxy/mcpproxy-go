// SecretsView.swift
// MCPProxy
//
// Displays secrets stored in the system keyring and environment variables
// referenced in server configurations. Matches the Web UI Secrets page.

import SwiftUI

// MARK: - Secret Model

struct SecretEntry: Codable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let type: String        // "keyring" or "env"
    let isSet: Bool
    let reference: String?  // e.g. "${keyring:name}"
    let usedBy: [String]?

    enum CodingKeys: String, CodingKey {
        case name, type, reference
        case isSet = "is_set"
        case usedBy = "used_by"
    }
}

struct SecretsResponse: Codable {
    let secrets: [SecretEntry]
}

// MARK: - Secrets View

struct SecretsView: View {
    let apiClient: APIClient?
    @State private var secrets: [SecretEntry] = []
    @State private var isLoading = false
    @State private var searchText = ""
    @State private var filterType = "all"
    @State private var showAddSheet = false
    @State private var newSecretName = ""
    @State private var newSecretValue = ""
    @State private var errorMessage: String?

    private var filteredSecrets: [SecretEntry] {
        var result = secrets
        if filterType == "keyring" {
            result = result.filter { $0.type == "keyring" }
        } else if filterType == "env" {
            result = result.filter { $0.type == "env" }
        } else if filterType == "missing" {
            result = result.filter { !$0.isSet }
        }
        if !searchText.isEmpty {
            result = result.filter { $0.name.localizedCaseInsensitiveContains(searchText) }
        }
        return result
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Secrets & Environment Variables")
                    .font(.title2.bold())
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
            }
            .padding()

            // Stats bar
            HStack(spacing: 20) {
                StatBadge(label: "Keyring", value: "\(secrets.filter { $0.type == "keyring" }.count)", color: .blue)
                StatBadge(label: "Env Vars", value: "\(secrets.filter { $0.type == "env" }.count)", color: .green)
                StatBadge(label: "Missing", value: "\(secrets.filter { !$0.isSet }.count)", color: .red)
                Spacer()
            }
            .padding(.horizontal)
            .padding(.bottom, 8)

            // Filter bar
            HStack {
                Picker("Filter", selection: $filterType) {
                    Text("All (\(secrets.count))").tag("all")
                    Text("Keyring (\(secrets.filter { $0.type == "keyring" }.count))").tag("keyring")
                    Text("Env Vars (\(secrets.filter { $0.type == "env" }.count))").tag("env")
                    Text("Missing (\(secrets.filter { !$0.isSet }.count))").tag("missing")
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 500)

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
            } else if filteredSecrets.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "key")
                        .font(.system(size: 48))
                        .foregroundStyle(.tertiary)
                    Text("No secrets found")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(filteredSecrets) { secret in
                        SecretRow(secret: secret, apiClient: apiClient, onDelete: {
                            Task { await loadSecrets() }
                        })
                    }
                }
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
                .font(.headline)

            TextField("Secret Name", text: $newSecretName)
                .textFieldStyle(.roundedBorder)

            SecureField("Secret Value", text: $newSecretValue)
                .textFieldStyle(.roundedBorder)

            if let error = errorMessage {
                Text(error)
                    .foregroundStyle(.red)
                    .font(.caption)
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
            let data = try await client.fetchRaw(path: "/api/v1/secrets")
            let decoder = JSONDecoder()
            if let wrapper = try? decoder.decode(APIResponse<SecretsResponse>.self, from: data),
               let payload = wrapper.data {
                secrets = payload.secrets
            } else if let direct = try? decoder.decode(SecretsResponse.self, from: data) {
                secrets = direct.secrets
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
    let secret: SecretEntry
    let apiClient: APIClient?
    let onDelete: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            // Type badge
            Text(secret.type.capitalized)
                .font(.caption2.bold())
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(secret.type == "keyring" ? Color.blue.opacity(0.2) : Color.green.opacity(0.2))
                .foregroundStyle(secret.type == "keyring" ? .blue : .green)
                .cornerRadius(4)

            // Status indicator
            Circle()
                .fill(secret.isSet ? Color.green : Color.red)
                .frame(width: 8, height: 8)

            VStack(alignment: .leading, spacing: 2) {
                Text(secret.name)
                    .font(.headline)
                if let ref = secret.reference, !ref.isEmpty {
                    Text(ref)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }

            Spacer()

            if let servers = secret.usedBy, !servers.isEmpty {
                Text("Used by \(servers.count) server(s)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if !secret.isSet {
                Label("Missing", systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            if secret.type == "keyring" {
                Button(role: .destructive) {
                    Task {
                        try? await apiClient?.deleteAction(path: "/api/v1/secrets/\(secret.name)")
                        onDelete()
                    }
                } label: {
                    Image(systemName: "trash")
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

    var body: some View {
        VStack(spacing: 2) {
            Text(value)
                .font(.title3.bold())
                .foregroundStyle(color)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(.quaternary)
        .cornerRadius(8)
    }
}
