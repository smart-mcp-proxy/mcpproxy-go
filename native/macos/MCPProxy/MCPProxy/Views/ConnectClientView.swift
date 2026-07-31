import SwiftUI
import AppKit

// MARK: - Presentation route

/// The single way into the Connect Client form.
///
/// Both the tray menu item and (FR-012) the dashboard's connect control post
/// this route, so no native path can reach a config write that skipped the
/// preview step.
enum ConnectClientPresentation {
    /// Posted to request the form. Observed by the app delegate, which presents
    /// it directly — no delayed notification chains (research D5).
    static let route = Notification.Name("MCPProxy.showConnectClient")

    /// Title of the tray menu item, beside "Add Server…".
    static let menuTitle = "Connect Client…"

    /// Request the form.
    static func present() {
        NotificationCenter.default.post(name: route, object: nil)
    }
}

/// Owns the tray menu item so the item and its routing are testable together:
/// a menu item with an action but no target silently does nothing.
final class ConnectClientMenuRouter: NSObject {
    static let shared = ConnectClientMenuRouter()

    @objc func openConnectClientForm(_ sender: Any?) {
        ConnectClientPresentation.present()
    }

    func makeMenuItem(keyEquivalent: String = "") -> NSMenuItem {
        let item = NSMenuItem(
            title: ConnectClientPresentation.menuTitle,
            action: #selector(openConnectClientForm(_:)),
            keyEquivalent: keyEquivalent
        )
        item.target = self
        return item
    }
}

// MARK: - Accessibility identifiers

/// Stable identifiers for VoiceOver and UI tests (FR-010). These are a contract
/// with external callers: rename one and a UI test stops finding the element.
enum ConnectClientAccessibility {
    static let list = "connect-client-list"
    static let preview = "connect-client-preview"
    static let entryText = "connect-client-entry-text"
    static let configPath = "connect-client-config-path"
    static let existingSummary = "connect-client-existing-summary"
    static let safetyNet = "connect-client-safety-net"
    static let credentialNotice = "connect-client-credential-notice"
    static let refusal = "connect-client-refusal"
    static let entryNameField = "connect-client-entry-name"
    static let advancedDisclosure = "connect-client-advanced"
    static let connectButton = "connect-client-connect"
    static let undoButton = "connect-client-undo"
    static let disconnectButton = "connect-client-disconnect"
    static let disconnectConfirm = "connect-client-disconnect-confirm"
    static let closeButton = "connect-client-close"
    static let status = "connect-client-status"
    static let waiting = "connect-client-waiting"
    static let transportNotice = "connect-client-transport-notice"

    /// Identifier of one client row.
    static func row(_ clientId: String) -> String { "connect-client-row-\(clientId)" }

    /// Every fixed identifier, for the uniqueness check.
    static let allIdentifiers: [String] = [
        list, preview, entryText, configPath, existingSummary, safetyNet,
        credentialNotice, refusal, entryNameField, advancedDisclosure,
        connectButton, undoButton, disconnectButton, disconnectConfirm,
        closeButton, status, waiting, transportNotice
    ]
}

// MARK: - View

/// The native Connect Client form: a stat-only client list on the left, and the
/// selected client's authoritative state plus the no-write preview on the right.
///
/// Deliberately thin — every decision (what the change is, whether a Connect
/// control may exist at all, what a failure says) lives in `ConnectClientModel`
/// and is unit-tested there.
struct ConnectClientView: View {
    @ObservedObject var model: ConnectClientModel
    var onClose: () -> Void = {}

    @State private var selectedID: String?
    @State private var showAdvanced = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            HStack(spacing: 0) {
                clientList
                    .frame(width: 260)
                Divider()
                detailPane
                    .frame(maxWidth: .infinity, alignment: .topLeading)
            }
            Divider()
            actionBar
        }
        .frame(minWidth: 760, minHeight: 480)
        .task { await model.loadList() }
        .alert("Disconnect this client?", isPresented: disconnectConfirmationIsPresented,
               presenting: model.pendingDisconnect) { _ in
            Button("Cancel", role: .cancel) { model.cancelDisconnect() }
            Button("Disconnect", role: .destructive) {
                Task { await model.confirmDisconnect() }
            }
            .accessibilityIdentifier(ConnectClientAccessibility.disconnectConfirm)
        } message: { confirmation in
            Text(confirmation.message)
        }
    }

    /// Presentation is driven by the model's pending confirmation. The setter is
    /// deliberately inert: both buttons resolve the state themselves, and having
    /// dismissal clear it would race the confirm action into a no-op.
    private var disconnectConfirmationIsPresented: Binding<Bool> {
        Binding(get: { model.pendingDisconnect != nil }, set: { _ in })
    }

    // MARK: Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Connect a Client")
                .font(.headline)
            Text("Register MCPProxy in an AI client's MCP config. "
                 + "Nothing is written until you review the change and press Connect.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(16)
    }

    // MARK: List

    @ViewBuilder
    private var clientList: some View {
        switch model.list {
        case .loading:
            ProgressView("Loading clients…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .coreUnreachable(let reason):
            VStack(spacing: 8) {
                Image(systemName: "bolt.horizontal.circle")
                    .font(.title2)
                    .foregroundStyle(.secondary)
                Text("Waiting for the MCPProxy core…")
                    .font(.subheadline)
                Text(reason)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding()
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityIdentifier(ConnectClientAccessibility.waiting)
        case .loaded:
            List(model.rows, selection: $selectedID) { row in
                rowView(row)
                    .tag(row.clientId)
                    .accessibilityIdentifier(ConnectClientAccessibility.row(row.clientId))
            }
            .accessibilityIdentifier(ConnectClientAccessibility.list)
            .onChange(of: selectedID) { newValue in
                // An unsupported client stays visible but is not a selection:
                // there is nothing the form could do for it (FR-009).
                guard let newValue,
                      model.rows.first(where: { $0.clientId == newValue })?.isSelectable == true
                else { return }
                Task { await model.select(newValue) }
            }
        }
    }

    /// Every label here is derived in the model (and unit-tested there); the row
    /// only renders it.
    private func rowView(_ row: ConnectClientModel.ClientRow) -> some View {
        HStack(spacing: 10) {
            Image(systemName: row.symbolName)
                .foregroundStyle(row.connected ? Color.green : Color.secondary)
                .frame(width: 20)
            VStack(alignment: .leading, spacing: 2) {
                Text(row.displayName)
                    .font(.subheadline)
                Text(row.stateLabel)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                if let note = row.note {
                    Text(note)
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
            }
            Spacer()
        }
        .padding(.vertical, 2)
        .opacity(row.isSelectable ? 1 : 0.5)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(row.displayName), \(row.stateLabel)")
    }

    // MARK: Detail + preview

    @ViewBuilder
    private var detailPane: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                if model.selection == nil {
                    Text("Select a client to see what would be written.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                } else {
                    detailSection
                    previewSection
                    advancedSection
                    statusSection
                }
            }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    @ViewBuilder
    private var detailSection: some View {
        switch model.detail {
        case .idle, .loading:
            ProgressView().controlSize(.small)
        case .failed(let message):
            Label(message, systemImage: "exclamationmark.triangle")
                .font(.caption)
                .foregroundStyle(.orange)
        case .resolved(let status):
            VStack(alignment: .leading, spacing: 4) {
                Text(status.displayName).font(.title3)
                Text(status.connected
                     ? "Connected as \"\(status.serverName ?? model.entryName)\""
                     : "Not connected to this proxy")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if let remediation = status.remediation, !remediation.isEmpty {
                    Text(remediation)
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            }
        }
    }

    @ViewBuilder
    private var previewSection: some View {
        switch model.preview {
        case .idle, .loading:
            ProgressView("Preparing preview…")
                .controlSize(.small)
        case .failed(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .font(.caption)
                .foregroundStyle(.red)
                .accessibilityIdentifier(ConnectClientAccessibility.preview)
        case .resolved(let preview):
            VStack(alignment: .leading, spacing: 10) {
                labelledRow("Config file", preview.configPath,
                            identifier: ConnectClientAccessibility.configPath)

                VStack(alignment: .leading, spacing: 4) {
                    Text("Entry to be written").font(.caption).foregroundStyle(.secondary)
                    Text(preview.entryText)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.secondary.opacity(0.08))
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                        .accessibilityIdentifier(ConnectClientAccessibility.entryText)
                }

                if let summary = preview.existingEntrySummary {
                    existingSummaryView(summary)
                }

                if let safetyNet = preview.safetyNetStatement {
                    Label(safetyNet, systemImage: "arrow.uturn.backward.circle")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier(ConnectClientAccessibility.safetyNet)
                }

                if let credential = preview.credentialNotice {
                    Label(credential, systemImage: "key.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                        .accessibilityIdentifier(ConnectClientAccessibility.credentialNotice)
                }

                if let refusal = model.connectRefusal {
                    Label(refusal, systemImage: "nosign")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier(ConnectClientAccessibility.refusal)
                }
            }
            .accessibilityIdentifier(ConnectClientAccessibility.preview)
        }
    }

    /// The structural, non-secret description of the entry being replaced. It
    /// carries names only — never a header or environment VALUE.
    private func existingSummaryView(_ summary: ConnectEntrySummary) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text("Replacing existing entry \"\(summary.entryName)\"")
                .font(.caption)
                .foregroundStyle(.secondary)
            if let type = summary.type { Text("Type: \(type)").font(.caption2) }
            if let endpoint = summary.endpoint { Text("Endpoint: \(endpoint)").font(.caption2) }
            if let command = summary.command { Text("Command: \(command)").font(.caption2) }
            if !summary.headerNames.isEmpty {
                Text("Headers: \(summary.headerNames.joined(separator: ", "))").font(.caption2)
            }
            if !summary.envNames.isEmpty {
                Text("Environment: \(summary.envNames.joined(separator: ", "))").font(.caption2)
            }
        }
        .foregroundStyle(.secondary)
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .accessibilityIdentifier(ConnectClientAccessibility.existingSummary)
    }

    /// FR-007: the entry-name override is advanced and collapsed by default.
    private var advancedSection: some View {
        DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
            VStack(alignment: .leading, spacing: 4) {
                TextField("Entry name", text: $model.entryName)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier(ConnectClientAccessibility.entryNameField)
                Text("Changing this discards the preview and fetches a new one.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            .padding(.top, 4)
        }
        .font(.caption)
        .accessibilityIdentifier(ConnectClientAccessibility.advancedDisclosure)
        .onChange(of: model.entryName) { _ in
            Task { await model.refreshPreview() }
        }
    }

    @ViewBuilder
    private var statusSection: some View {
        switch model.action {
        case .idle, .inFlight:
            EmptyView()
        case .succeeded(let result):
            Label(result.message ?? "Connected.", systemImage: "checkmark.circle.fill")
                .font(.caption)
                .foregroundStyle(.green)
                .accessibilityIdentifier(ConnectClientAccessibility.status)
        case .conflict(let reason):
            Label("\(reason) The preview has been refreshed — review it and try again.",
                  systemImage: "arrow.triangle.2.circlepath")
                .font(.caption)
                .foregroundStyle(.orange)
                .accessibilityIdentifier(ConnectClientAccessibility.status)
        case .failed(let reason):
            Label(reason, systemImage: "xmark.octagon.fill")
                .font(.caption)
                .foregroundStyle(.red)
                .accessibilityIdentifier(ConnectClientAccessibility.status)
        }
    }

    // MARK: Actions

    private var actionBar: some View {
        HStack(spacing: 10) {
            if let reason = model.mutatingDisabledReason {
                Label(reason, systemImage: "lock.fill")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier(ConnectClientAccessibility.transportNotice)
            }
            Spacer()
            if model.undoControlExists {
                Button("Undo") {
                    Task { await model.undo() }
                }
                .disabled(!model.isUndoEnabled)
                .accessibilityIdentifier(ConnectClientAccessibility.undoButton)
            }
            if model.disconnectControlExists {
                Button("Disconnect…") {
                    model.requestDisconnect()
                }
                .disabled(!model.isDisconnectEnabled)
                .accessibilityIdentifier(ConnectClientAccessibility.disconnectButton)
            }
            Button("Close") {
                // The undo's scope ends with the form (FR-006).
                model.formWillClose()
                onClose()
            }
                .keyboardShortcut(.cancelAction)
                .accessibilityIdentifier(ConnectClientAccessibility.closeButton)
            if model.connectControlExists {
                Button {
                    Task { await model.connect() }
                } label: {
                    if model.isBusy {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("Connect")
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(!model.isConnectEnabled)
                .accessibilityIdentifier(ConnectClientAccessibility.connectButton)
            }
        }
        .padding(16)
    }

    private func labelledRow(_ title: String, _ value: String, identifier: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .accessibilityIdentifier(identifier)
        }
    }
}
