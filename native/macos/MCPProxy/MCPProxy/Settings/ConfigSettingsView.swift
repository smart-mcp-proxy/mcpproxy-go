// ConfigSettingsView.swift
// MCPProxy
//
// Native, full-fidelity config editor for the tray app — mirrors the web UI
// Configuration page (Security & Access / General / Advanced). The tray is a
// pure REST client: it loads via GET /api/v1/config and saves only the changed
// fields via PATCH /api/v1/config (deep-merge), so unrelated settings and
// redacted secrets are never clobbered and the JSON file is never touched
// directly (Constitution III).

import SwiftUI

// MARK: - Store

@MainActor
final class ConfigStore: ObservableObject {
    @Published var working: [String: Any] = [:]
    @Published var loaded = false
    @Published var loading = false
    @Published var loadError: String?
    /// Bumped on every mutation so SwiftUI re-evaluates dirty state.
    @Published var revision = 0

    private var original: [String: Any] = [:]
    private let appState: AppState

    init(appState: AppState) { self.appState = appState }

    func load() async {
        guard let api = appState.apiClient else {
            loadError = "Not connected to the MCPProxy core."
            return
        }
        loading = true
        loadError = nil
        do {
            let cfg = try await api.getConfig()
            working = cfg
            original = cfg
            loaded = true
            revision += 1
        } catch {
            loadError = (error as? APIClientError)?.errorDescription ?? error.localizedDescription
        }
        loading = false
    }

    // MARK: value access

    func value(_ key: String) -> Any? { configGet(working, key) }

    func setValue(_ key: String, _ value: Any?) {
        configSet(&working, key, value)
        revision += 1
    }

    func isDirty(_ key: String) -> Bool {
        !valuesEqual(configGet(working, key), configGet(original, key))
    }

    func dirtyKeys(in fields: [ConfigField]) -> [String] {
        fields.map(\.key).filter { isDirty($0) }
    }

    func revert(_ keys: [String]) {
        for k in keys { configSet(&working, k, configGet(original, k)) }
        revision += 1
    }

    /// Apply the given keys via PATCH. Returns (requiresRestart, restartReason)
    /// on success; throws on failure (incl. validation errors).
    func save(_ keys: [String]) async throws -> (requiresRestart: Bool, reason: String?) {
        guard let api = appState.apiClient else {
            throw APIClientError.httpError(statusCode: 0, message: "Not connected to the core.")
        }
        let partial = buildPartial(working, keys)
        let result = try await api.patchConfig(partial)
        if let errs = result["validation_errors"] as? [[String: Any]], !errs.isEmpty {
            let msg = errs.compactMap { e -> String? in
                let field = (e["field"] as? String) ?? ""
                let m = (e["message"] as? String) ?? ""
                return field.isEmpty ? m : "\(field): \(m)"
            }.joined(separator: "; ")
            throw APIClientError.httpError(statusCode: 422, message: msg.isEmpty ? "Validation failed" : msg)
        }
        // Commit saved keys into the snapshot so they're no longer dirty.
        for k in keys { configSet(&original, k, configGet(working, k)) }
        revision += 1
        let requiresRestart = (result["requires_restart"] as? Bool) ?? false
        return (requiresRestart, result["restart_reason"] as? String)
    }

    // MARK: typed bindings

    func boolBinding(_ key: String) -> Binding<Bool> {
        Binding(
            get: { (self.value(key) as? NSNumber)?.boolValue ?? (self.value(key) as? Bool) ?? false },
            set: { self.setValue(key, $0) }
        )
    }

    func stringBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { coerceString(self.value(key)) },
            set: { self.setValue(key, $0) }
        )
    }

    func doubleBinding(_ key: String) -> Binding<Double> {
        Binding(
            get: { coerceDouble(self.value(key)) ?? 0 },
            set: { self.setValue(key, $0) }
        )
    }

    func stringArrayContains(_ key: String, _ option: String) -> Bool {
        (value(key) as? [Any])?.compactMap { $0 as? String }.contains(option) ?? false
    }

    func toggleStringArray(_ key: String, _ option: String, _ on: Bool) {
        var arr = (value(key) as? [Any])?.compactMap { $0 as? String } ?? []
        if on, !arr.contains(option) { arr.append(option) }
        if !on { arr.removeAll { $0 == option } }
        setValue(key, arr)
    }
}

// helpers usable from the view layer
func coerceString(_ v: Any?) -> String {
    switch v {
    case let s as String: return s
    case let n as NSNumber: return n.stringValue
    default: return ""
    }
}
func coerceDouble(_ v: Any?) -> Double? {
    switch v {
    case let n as NSNumber: return n.doubleValue
    case let d as Double: return d
    case let i as Int: return Double(i)
    case let s as String: return Double(s)
    default: return nil
    }
}
func valuesEqual(_ a: Any?, _ b: Any?) -> Bool {
    if a == nil && b == nil { return true }
    guard let a = a as? NSObject, let b = b as? NSObject else { return false }
    return a.isEqual(b)
}

// MARK: - Section view (one Save button per section)

struct ConfigSectionView: View {
    @ObservedObject var store: ConfigStore
    let sectionId: String
    let fields: [ConfigField]

    @State private var saving = false
    @State private var savedNote: String?
    @State private var errorNote: String?
    @State private var showConfirm = false
    @State private var confirmMessages: [String] = []
    @State private var confirmInfoOnly = false

    private var dirty: [String] { store.dirtyKeys(in: fields) }
    private var invalid: Bool {
        dirty.contains { key in
            guard let f = fields.first(where: { $0.key == key }) else { return false }
            return validateConfigField(f, store.value(key)) != nil
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(fields) { field in
                ConfigFieldRow(store: store, field: field)
                Divider()
            }

            HStack {
                Group {
                    if let savedNote { Text(savedNote).foregroundColor(.green) }
                    else if let errorNote { Text(errorNote).foregroundColor(.red) }
                    else if !dirty.isEmpty { Text("\(dirty.count) unsaved change\(dirty.count > 1 ? "s" : "")").foregroundColor(.secondary) }
                }
                .font(.callout)
                Spacer()
                if !dirty.isEmpty {
                    Button("Discard") { store.revert(dirty); savedNote = nil; errorNote = nil }
                        .buttonStyle(.borderless)
                }
                Button {
                    attemptSave()
                } label: {
                    if saving { ProgressView().controlSize(.small) } else { Text("Save changes") }
                }
                .buttonStyle(.borderedProminent)
                .disabled(dirty.isEmpty || saving || invalid)
            }
            .padding(.top, 10)
        }
        .alert(confirmInfoOnly ? "Are you sure?" : "Confirm sensitive change",
               isPresented: $showConfirm) {
            Button(confirmInfoOnly ? "Keep it on" : "Cancel", role: .cancel) {}
            Button(confirmInfoOnly ? "Turn off anyway" : "Apply anyway",
                   role: confirmInfoOnly ? nil : .destructive) { Task { await doSave() } }
        } message: {
            Text(confirmMessages.joined(separator: "\n\n"))
        }
    }

    private func attemptSave() {
        var msgs: [String] = []
        var infoOnly = true
        for key in dirty {
            guard let f = fields.first(where: { $0.key == key }), let dm = f.dangerMessage else { continue }
            let val = store.value(key)
            let triggers: Bool
            if let cv = f.dangerConfirmValue {
                triggers = ((val as? NSNumber)?.boolValue ?? (val as? Bool) ?? false) == cv
            } else if f.key == "listen" {
                triggers = !isLoopbackAddress(coerceString(val))
            } else {
                triggers = true
            }
            if triggers {
                msgs.append(dm)
                if !f.dangerInfoTone { infoOnly = false }
            }
        }
        if msgs.isEmpty {
            Task { await doSave() }
        } else {
            confirmMessages = msgs
            confirmInfoOnly = infoOnly
            showConfirm = true
        }
    }

    private func doSave() async {
        saving = true
        savedNote = nil
        errorNote = nil
        let keys = dirty
        do {
            let r = try await store.save(keys)
            savedNote = r.requiresRestart
                ? "Saved — restart required\(r.reason.map { ": \($0)" } ?? "")"
                : "Saved"
        } catch {
            errorNote = (error as? APIClientError)?.errorDescription ?? error.localizedDescription
        }
        saving = false
    }
}

private func isLoopbackAddress(_ s: String) -> Bool {
    let host = s.split(separator: "@").last.map(String.init) ?? s
    return host.hasPrefix("127.") || host.hasPrefix("localhost") || host.hasPrefix("[::1]") || host.hasPrefix("::1")
}

// MARK: - Field row

struct ConfigFieldRow: View {
    @ObservedObject var store: ConfigStore
    let field: ConfigField
    @State private var revealSecret = false

    private var dirty: Bool { store.isDirty(field.key) }
    private var validationError: String? { dirty ? validateConfigField(field, store.value(field.key)) : nil }

    var body: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(field.label).fontWeight(.medium)
                    if dirty { Circle().fill(Color.orange).frame(width: 6, height: 6) }
                    if field.restart {
                        Text("restart").font(.caption2).padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Color.orange.opacity(0.25)).cornerRadius(4)
                    }
                    if let docs = field.docs, let url = URL(string: SettingsCatalog.docsBase + docs) {
                        Link("docs ↗", destination: url).font(.caption)
                    }
                }
                if let help = field.help {
                    Text(help).font(.caption).foregroundColor(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                if let err = validationError {
                    Text(err).font(.caption).foregroundColor(.red)
                }
            }
            Spacer(minLength: 16)
            control.frame(maxWidth: 240, alignment: .trailing)
        }
        .padding(.vertical, 8)
    }

    @ViewBuilder private var control: some View {
        switch field.control {
        case .toggle:
            Toggle("", isOn: store.boolBinding(field.key)).labelsHidden()
        case .select:
            Picker("", selection: store.stringBinding(field.key)) {
                ForEach(field.options) { opt in Text(opt.label).tag(opt.value) }
            }
            .labelsHidden().frame(maxWidth: 220)
        case .number:
            TextField("", value: store.doubleBinding(field.key), format: .number)
                .multilineTextAlignment(.trailing).frame(width: 100)
                .textFieldStyle(.roundedBorder)
        case .text, .duration:
            TextField(field.control == .duration ? "2m" : "", text: store.stringBinding(field.key))
                .frame(width: 200).textFieldStyle(.roundedBorder).font(.system(.body, design: .monospaced))
        case .secret:
            HStack(spacing: 4) {
                if revealSecret {
                    TextField("", text: store.stringBinding(field.key))
                        .frame(width: 170).textFieldStyle(.roundedBorder).font(.system(.body, design: .monospaced))
                } else {
                    SecureField("", text: store.stringBinding(field.key))
                        .frame(width: 170).textFieldStyle(.roundedBorder)
                }
                Button { revealSecret.toggle() } label: { Image(systemName: revealSecret ? "eye.slash" : "eye") }
                    .buttonStyle(.borderless)
            }
        case .multiselect:
            VStack(alignment: .trailing, spacing: 2) {
                ForEach(field.options) { opt in
                    Toggle(opt.label, isOn: Binding(
                        get: { store.stringArrayContains(field.key, opt.value) },
                        set: { store.toggleStringArray(field.key, opt.value, $0) }
                    ))
                    .toggleStyle(.checkbox).font(.caption)
                }
            }
        }
    }
}

// MARK: - Tab containers (load once via the shared store)

struct ConfigTabContainer<Content: View>: View {
    @ObservedObject var store: ConfigStore
    @ViewBuilder let content: () -> Content

    var body: some View {
        Group {
            if let err = store.loadError {
                VStack(spacing: 8) {
                    Text("Couldn’t load configuration").font(.headline)
                    Text(err).font(.callout).foregroundColor(.secondary)
                    Button("Retry") { Task { await store.load() } }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity).padding()
            } else if !store.loaded {
                ProgressView("Loading configuration…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView { content().padding(20) }
            }
        }
        .task { if !store.loaded { await store.load() } }
    }
}

struct SecuritySettingsTab: View {
    @ObservedObject var store: ConfigStore
    var body: some View {
        ConfigTabContainer(store: store) {
            ConfigSectionView(store: store, sectionId: "security", fields: SettingsCatalog.security)
        }
    }
}

struct GeneralConfigTab: View {
    @ObservedObject var store: ConfigStore
    var body: some View {
        ConfigTabContainer(store: store) {
            ConfigSectionView(store: store, sectionId: "general", fields: SettingsCatalog.general)
        }
    }
}

struct AdvancedSettingsTab: View {
    @ObservedObject var store: ConfigStore
    var body: some View {
        ConfigTabContainer(store: store) {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(SettingsCatalog.advanced) { section in
                    DisclosureGroup {
                        ConfigSectionView(store: store, sectionId: section.id, fields: section.fields)
                            .padding(.top, 6)
                    } label: {
                        Text(section.title).fontWeight(.semibold)
                    }
                    .padding(12)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .cornerRadius(8)
                }
            }
        }
    }
}
