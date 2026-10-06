// AccessExplainerView.swift
// MCPProxy
//
// Spec 108-k (K19, FR-046): the "Explain access" sheet. Pick a subject and a
// `server:tool`; the answer is the ordered chain of steps (each with a symbol
// AND its word), a verdict banner and one button per fix. A fix opens the
// place where it is made; it never changes anything itself.

import SwiftUI

/// What a presented explainer starts from.
struct ExplainerRequest: Identifiable, Equatable {
    let id = UUID()
    var subject: ExplainerSubject
    var tool: String
}

struct AccessExplainerSheet: View {
    @ObservedObject var appState: AppState
    let request: ExplainerRequest

    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: AccessExplainerModel
    @State private var kind: String
    @State private var tokenNames: [String] = []

    init(appState: AppState, request: ExplainerRequest) {
        self.appState = appState
        self.request = request
        let source: AccessExplainSource = appState.apiClient ?? NoCoreExplainSource()
        _model = StateObject(wrappedValue: AccessExplainerModel(
            source: source, subject: request.subject, tool: request.tool))
        _kind = State(initialValue: request.subject.kindLabel)
    }

    private static let kinds = ["Client", "Token", "Profile", "Anonymous"]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Explain access").font(.title3.bold())
            Text("Why can — or can’t — a caller use a tool? Each step of the access chain is checked in order.")
                .font(.caption).foregroundStyle(.secondary)

            subjectRow
            toolRow

            if let error = model.errorMessage {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .accessibilityIdentifier("access-explainer-error")
            }

            if let explanation = model.explanation {
                ScrollView { result(explanation) }
            } else if model.isLoading {
                ProgressView("Checking…").controlSize(.small)
            } else {
                Text("Choose a subject and a tool, then Explain.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Explain") { Task { await model.explain() } }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(!model.canExplain)
                    .accessibilityIdentifier("access-explainer-run")
            }
        }
        .padding(20)
        .frame(minWidth: 560, idealWidth: 620, minHeight: 440, idealHeight: 520)
        .task {
            await model.loadToolNames()
            tokenNames = ((try? await appState.apiClient?.tokens()) ?? [])
                .filter { $0.kind != "client" }.map(\.name)
            if model.canExplain { await model.explain() }
        }
        .accessibilityIdentifier("access-explainer")
    }

    // MARK: Subject

    private var subjectRow: some View {
        HStack {
            Picker("Subject", selection: $kind) {
                ForEach(Self.kinds, id: \.self) { Text($0).tag($0) }
            }
            .frame(width: 200)
            .onChange(of: kind) { newKind in
                switch newKind {
                case "Client": model.subject = .client(appState.clients.first?.id ?? "")
                case "Token": model.subject = .token(tokenNames.first ?? "")
                case "Profile": model.subject = .profile(appState.profiles.first?.name ?? "")
                default: model.subject = .anonymous
                }
            }
            .accessibilityIdentifier("access-explainer-subject-kind")

            switch kind {
            case "Client":
                Picker("Client", selection: Binding(
                    get: { model.subject.name ?? "" },
                    set: { model.subject = .client($0) })) {
                    ForEach(appState.clients, id: \.id) { Text($0.displayName).tag($0.id) }
                }
                .accessibilityIdentifier("access-explainer-subject-value")
            case "Token":
                Picker("Token", selection: Binding(
                    get: { model.subject.name ?? "" },
                    set: { model.subject = .token($0) })) {
                    ForEach(tokenNames, id: \.self) { Text($0).tag($0) }
                }
                .accessibilityIdentifier("access-explainer-subject-value")
            case "Profile":
                Picker("Profile", selection: Binding(
                    get: { model.subject.name ?? "" },
                    set: { model.subject = .profile($0) })) {
                    ForEach(appState.profiles) { Text($0.pickerTitle(in: appState.profiles)).tag($0.name) }
                }
                .accessibilityIdentifier("access-explainer-subject-value")
            default:
                Text("A caller that presents no credential").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private var toolRow: some View {
        VStack(alignment: .leading, spacing: 4) {
            TextField("server:tool, for example github:create_issue", text: $model.tool)
                .textFieldStyle(.roundedBorder)
                .onSubmit { Task { await model.explain() } }
                .accessibilityLabel("Tool to explain")
                .accessibilityIdentifier("access-explainer-tool")
            if !model.tool.isEmpty && !model.toolIsValid {
                Text("Use the form server:tool.").font(.caption2).foregroundStyle(.secondary)
            }
            if !model.suggestions.isEmpty && model.explanation == nil {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack {
                        ForEach(model.suggestions, id: \.self) { name in
                            Button(name) { model.tool = name }
                                .buttonStyle(.bordered).controlSize(.small)
                        }
                    }
                }
            }
        }
    }

    // MARK: Result

    @ViewBuilder
    private func result(_ explanation: AccessExplanation) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            if let verdict = model.verdictText {
                Label(verdict, systemImage: explanation.verdict == .allowed ? "checkmark.seal.fill" : "eye.slash.fill")
                    .font(.headline)
                    .foregroundStyle(explanation.verdict == .allowed ? Color.green : Color.orange)
                    .accessibilityIdentifier("access-explainer-verdict")
            }
            if !explanation.profile.name.isEmpty {
                Text("Resolved profile: \(explanation.profile.name) (\(explanation.profile.source))")
                    .font(.caption).foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: 4) {
                ForEach(Array(explanation.steps.enumerated()), id: \.element.id) { index, step in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Image(systemName: step.status.symbolName)
                            .foregroundStyle(color(for: step.status))
                            .frame(width: 18)
                        Text("\(index + 1). \(step.stepLabel)").font(.callout)
                        Text(step.status.word).font(.caption).foregroundStyle(.secondary)
                        if !step.detail.isEmpty {
                            Text(step.detail).font(.caption.monospaced()).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityLabel("\(step.stepLabel): \(step.status.word)\(step.detail.isEmpty ? "" : ", \(step.detail)")")
                }
            }
            if !explanation.fixes.isEmpty {
                Divider()
                Text("Ways to change this").font(.subheadline.weight(.semibold))
                ForEach(explanation.fixes) { fix in
                    if let route = model.route(for: fix) {
                        Button(fix.label.isEmpty ? fix.action.wire : fix.label) {
                            dismiss()
                            appState.navigate(route)
                        }
                        .accessibilityIdentifier("access-explainer-fix-\(fix.action.wire)")
                    } else {
                        Text(fix.label).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Text("These open the place where the change is made; nothing is changed here.")
                    .font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private func color(for status: ExplainStatus) -> Color {
        switch status {
        case .pass: return .green
        case .fail: return .red
        default: return .secondary
        }
    }
}

/// Stands in when no core connection exists yet: every call fails politely.
private struct NoCoreExplainSource: AccessExplainSource {
    func explain(tool: String, subject: APIClient.ExplainSubjectQuery) async throws -> AccessExplanation {
        throw APIClientError.notReady
    }

    func allToolNames() async throws -> [String] { [] }
}
