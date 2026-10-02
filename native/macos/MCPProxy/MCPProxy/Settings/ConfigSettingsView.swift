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
import AppKit
import Combine

// MARK: - Store

@MainActor
final class ConfigStore: ObservableObject {
    @Published var working: [String: Any] = [:]
    @Published var loaded = false
    @Published var loading = false
    @Published var loadError: String?
    /// Bumped on every mutation so SwiftUI re-evaluates dirty state.
    @Published var revision = 0
    /// Spec 108-k: a setting a fix button asked to show ("Require
    /// authentication…"). The tab scrolls to it and its row is highlighted.
    @Published var highlightedKey: String?
    /// The core's built-in MCP `instructions` text (MCP-2176), shown as the
    /// placeholder of the instructions field so a blank box reads as "the
    /// default is this" instead of "nothing". Fetched, never hardcoded — the
    /// Web UI does the same and the two must not drift.
    @Published var defaultInstructions: String?
    /// Spec 109 FR-044a / F-10: the listen address the connected core is
    /// actually bound to (`status.listen_addr`), so Settings can name which core
    /// is being edited and when the saved address differs (pending restart).
    @Published var runningListenAddr: String?
    /// Settings whose effective value is forced from outside the config file
    /// (an environment telemetry opt-out), keyed by setting key. The value is
    /// the reason shown under the row. Locked rows are read-only and never dirty.
    @Published private(set) var locks: [String: String] = [:]

    private var original: [String: Any] = [:]
    /// The API response exactly as the core sent it. `working`/`original` are
    /// normalized (absent `omitempty` defaults filled in) so blank Pickers show
    /// their real default, but the Raw tab must keep showing server truth.
    private var raw: [String: Any] = [:]
    private let appState: AppState

    private var connectionObserver: AnyCancellable?

    init(appState: AppState) {
        self.appState = appState
        // The Settings window is reused across core restarts, so the running
        // address cached from the first load goes stale the moment the core is
        // replaced. Re-read /status whenever the tray lands on a new connection.
        connectionObserver = appState.$connectionGeneration
            .dropFirst()
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in
                guard let self, self.appState.coreState == .connected else { return }
                Task { await self.refreshStatus() }
            }
    }

    func load() async {
        guard let api = appState.apiClient else {
            loadError = "Not connected to the MCPProxy core."
            return
        }
        loading = true
        loadError = nil
        do {
            hydrate(from: try await api.getConfig())
        } catch {
            loadError = (error as? APIClientError)?.errorDescription ?? error.localizedDescription
        }
        // Best-effort: an older core without the field just leaves the generic
        // placeholder in place, so this never fails the settings load.
        if let status = try? await api.status() {
            if let text = status.defaultInstructions, !text.isEmpty {
                defaultInstructions = text
            }
            applyStatus(status)
        }
        loading = false
    }

    /// Re-read `GET /api/v1/status` for what the connected core says about
    /// itself (running listen address, effective telemetry state) without
    /// touching the config the user may be editing. Best effort: a failed read
    /// keeps what was last known.
    func refreshStatus() async {
        guard loaded, let api = appState.apiClient, let status = try? await api.status() else { return }
        applyStatus(status)
    }

    /// Adopt what `GET /api/v1/status` says about the connected core: its running
    /// listen address (F-10) and the effective telemetry state (F-03).
    func applyStatus(_ status: StatusResponse) {
        let running = status.listenAddr.flatMap { $0.isEmpty ? nil : $0 }
        runningListenAddr = running
        adoptRunningListenIfBlank()
        if let reason = TelemetryNotice.settingLock(state: status.telemetry) {
            locks["telemetry.enabled"] = reason
        } else {
            locks.removeValue(forKey: "telemetry.enabled")
        }
        revision += 1
    }

    /// A config without `listen` would render the catalogue placeholder, which a
    /// reader (and an accessibility client) takes for a value. Show the address
    /// the core is really bound to instead, in both `working` and `original` so
    /// the field is not dirty and a Save never PATCHes `listen`.
    private func adoptRunningListenIfBlank() {
        guard loaded, let running = runningListenAddr,
              isBlankValue(configGet(working, "listen")), isBlankValue(configGet(original, "listen"))
        else { return }
        configSet(&working, "listen", running)
        configSet(&original, "listen", running)
    }

    func lockReason(_ key: String) -> String? { locks[key] }

    /// The line under the Listen address field: which core this is and where it
    /// listens, plus a pending-restart note when the saved address differs.
    var listenNote: String? {
        guard let running = runningListenAddr else { return nil }
        let version = appState.version.trimmingCharacters(in: .whitespaces)
        let core = version.isEmpty ? "Connected core" : "Connected core \(version)"
        var note = "\(core) is listening on \(running)."
        if let saved = (configGet(original, "listen") as? String)?.trimmingCharacters(in: .whitespaces),
           !saved.isEmpty, saved != running {
            note += " The saved address \(saved) takes effect after a restart."
        }
        return note
    }

    /// Populate the store from a raw `GET /api/v1/config` response.
    ///
    /// Split out of `load()` so the hydration invariant is testable without an
    /// API client. That invariant: `working` and `original` BOTH get the
    /// resolved defaults for `omitempty` keys the core omits (the serialization
    /// modes — absent means "full") so their Picker shows the real default
    /// instead of blank, while `raw` keeps the untouched response because the
    /// Raw tab must show server truth. Normalizing only one of the two would
    /// make an untouched field read as an unsaved change.
    func hydrate(from cfg: [String: Any]) {
        raw = cfg
        let normalized = SettingsCatalog.normalizeDefaults(cfg)
        working = normalized
        original = normalized
        loaded = true
        adoptRunningListenIfBlank()
        revision += 1
    }

    /// A single key was saved elsewhere (the Anonymous callers section PATCHes
    /// `anonymous_profile` on its own): adopt just that key as saved, in the
    /// snapshot, the working copy and the Raw tab, leaving every other unsaved
    /// edit alone. A full `load()` would reset them all. No-op before the first
    /// load, which brings the truth itself.
    func adoptSaved(_ key: String, value: Any?) {
        guard loaded else { return }
        configSet(&raw, key, value)
        configSet(&original, key, value)
        configSet(&working, key, value)
        revision += 1
    }

    /// The core's current (saved) configuration, pretty-printed for the
    /// read-only Raw tab. Reflects server truth — not unsaved form edits.
    var prettyJSON: String {
        guard JSONSerialization.isValidJSONObject(raw),
              let data = try? JSONSerialization.data(
                withJSONObject: raw, options: [.prettyPrinted, .sortedKeys]),
              let str = String(data: data, encoding: .utf8)
        else { return "{}" }
        return str
    }

    // MARK: value access

    func value(_ key: String) -> Any? { configGet(working, key) }

    func setValue(_ key: String, _ value: Any?) {
        configSet(&working, key, value)
        revision += 1
    }

    func isDirty(_ key: String) -> Bool {
        if locks[key] != nil { return false }
        return !valuesEqual(configGet(working, key), configGet(original, key))
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
        // Commit saved keys into the snapshot so they're no longer dirty. The
        // Raw tab tracks the same commit, so it keeps reflecting what the core
        // now holds rather than the config as it was at load time.
        for k in keys {
            configSet(&original, k, configGet(working, k))
            configSet(&raw, k, configGet(working, k))
        }
        revision += 1
        let requiresRestart = (result["requires_restart"] as? Bool) ?? false
        return (requiresRestart, result["restart_reason"] as? String)
    }

    // MARK: typed bindings

    func boolBinding(_ key: String) -> Binding<Bool> {
        Binding(
            // A locked key shows its forced effective value (off), not the stored one.
            get: {
                if self.locks[key] != nil { return false }
                return (self.value(key) as? NSNumber)?.boolValue ?? (self.value(key) as? Bool) ?? false
            },
            set: { if self.locks[key] == nil { self.setValue(key, $0) } }
        )
    }

    func stringBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { coerceString(self.value(key)) },
            set: { self.setValue(key, $0) }
        )
    }

    /// Binding for an *optional* scalar field (tri-state durations). Reads
    /// nil/NSNull as an empty field; writes a blank value back as "unset"
    /// (nil → NSNull → JSON null = reset to default) instead of an empty
    /// string, so the field neither reads as dirty when absent nor sends an
    /// unparseable "" to the backend.
    func optionalStringBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { coerceString(self.value(key)) },
            set: { self.setValue(key, optionalScalarStored($0)) }
        )
    }

    /// Binding for a `listLines` textarea (fields.ts `listKind: 'lines'`): the
    /// JSON value is a []string shown one entry per line. Writing splits on
    /// newlines AND commas (a pasted "a,b" still yields two entries), trims,
    /// and drops blank entries, so a cleared box is an empty list — never a
    /// string the Go side cannot decode into []string. Clearing a key the core
    /// never sent stores "unset" rather than [], so the row does not read as
    /// dirty against an absent key (same tri-state as optionalStringBinding).
    func linesBinding(_ key: String) -> Binding<String> {
        Binding(
            get: { linesText(self.value(key)) },
            set: {
                let list = parseLines($0)
                if list.isEmpty, isBlankValue(configGet(self.original, key)) {
                    self.setValue(key, nil)
                } else {
                    self.setValue(key, list)
                }
            }
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
/// Render a []string as one entry per line; a scalar falls back to its string
/// form so a hand-edited file never shows as blank.
func linesText(_ v: Any?) -> String {
    if let arr = v as? [Any] { return arr.map { coerceString($0) }.joined(separator: "\n") }
    return coerceString(v)
}

/// Parse textarea text into a trimmed []string (mirrors textToList in
/// fields.ts): newlines and commas both separate entries; blanks are dropped.
func parseLines(_ text: String) -> [String] {
    text.components(separatedBy: CharacterSet(charactersIn: "\n,"))
        .map { $0.trimmingCharacters(in: .whitespaces) }
        .filter { !$0.isEmpty }
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
/// True for the values that all mean "no value": nil (absent key), NSNull
/// (explicit null) and an empty/whitespace-only string (the text-binding
/// round-trip of a blank field). Treating these as one class keeps an
/// untouched optional field from reading as an unsaved change.
func isBlankValue(_ v: Any?) -> Bool {
    if v == nil || v is NSNull { return true }
    if let s = v as? String { return s.trimmingCharacters(in: .whitespaces).isEmpty }
    return false
}

func valuesEqual(_ a: Any?, _ b: Any?) -> Bool {
    if isBlankValue(a) && isBlankValue(b) { return true }
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
                    .id(field.key)
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
        Group {
            // A textarea cannot share a row with its label — 240pt of trailing
            // column is not a place to edit a paragraph. It stacks instead.
            if field.control == .textarea {
                VStack(alignment: .leading, spacing: 6) {
                    labelBlock
                    control.frame(maxWidth: .infinity, alignment: .leading)
                }
            } else {
                HStack(alignment: .top) {
                    labelBlock
                    Spacer(minLength: 16)
                    control.frame(maxWidth: 240, alignment: .trailing)
                }
            }
        }
        .padding(.vertical, 8)
        .background(store.highlightedKey == field.key ? Color.accentColor.opacity(0.15) : Color.clear)
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .accessibilityIdentifier("setting-\(field.key)")
    }

    private var labelBlock: some View {
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
            // Spec 109 FR-044a: forced from outside the config file; say why.
            if let reason = store.lockReason(field.key) {
                Text(reason).font(.caption).foregroundColor(.orange)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("setting-locked-\(field.key)")
            }
            // F-10: which core this is, and where it really listens.
            if field.key == "listen", let note = store.listenNote {
                Text(note).font(.caption).foregroundColor(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("setting-listen-running")
            }
            if let err = validationError {
                Text(err).font(.caption).foregroundColor(.red)
            }
        }
    }

    @ViewBuilder private var control: some View {
        controlBody.disabled(store.lockReason(field.key) != nil)
    }

    @ViewBuilder private var controlBody: some View {
        switch field.control {
        case .toggle:
            Toggle("", isOn: store.boolBinding(field.key)).labelsHidden()
        case .select:
            Picker("", selection: store.stringBinding(field.key)) {
                ForEach(field.options) { opt in Text(opt.label).tag(opt.value) }
            }
            .labelsHidden().frame(maxWidth: 220)
        case .number:
            HStack(spacing: 4) {
                TextField("", value: store.doubleBinding(field.key), format: .number)
                    .multilineTextAlignment(.trailing).frame(width: 100)
                    .textFieldStyle(.roundedBorder)
                // F13: fields.ts gives entropy_threshold step 0.1 and
                // oauth_expiry_warning_hours step 0.5; without a stepper the
                // native form could only be typed into.
                if let step = field.step {
                    Stepper("", value: store.doubleBinding(field.key),
                            in: (field.min ?? -.greatestFiniteMagnitude)...(field.max ?? .greatestFiniteMagnitude),
                            step: step)
                        .labelsHidden()
                }
            }
        case .textarea:
            // The instructions field: multi-line, and its placeholder is the
            // live built-in default rather than an example. A `listLines`
            // field (trusted_proxies) binds a []string one entry per line and
            // keeps its own example placeholder.
            let text = field.listLines ? store.linesBinding(field.key) : store.optionalStringBinding(field.key)
            ZStack(alignment: .topLeading) {
                TextEditor(text: text)
                    .font(.system(.callout, design: .monospaced))
                    .frame(minHeight: 120)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color(nsColor: .separatorColor)))
                if text.wrappedValue.isEmpty {
                    Text(field.listLines ? (field.placeholder ?? "") : (store.defaultInstructions ?? field.placeholder ?? ""))
                        .font(.system(.caption, design: .monospaced))
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 6).padding(.vertical, 10)
                        .allowsHitTesting(false)
                }
            }
        case .text, .duration:
            // Duration fields are tri-state: an optional one stores a blank
            // value as "unset" (reset to default) via optionalStringBinding.
            // The placeholder is the field's real default (e.g. 30s / 5m).
            TextField(field.placeholder ?? "",
                      text: (field.control == .duration && field.optional)
                          ? store.optionalStringBinding(field.key)
                          : store.stringBinding(field.key))
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
    /// A setting key to scroll to once loaded (Spec 108-k fix buttons).
    var scrollTarget: String? = nil
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
                ScrollViewReader { proxy in
                    ScrollView { content().padding(20) }
                        .onAppear { scroll(proxy) }
                        .onChange(of: scrollTarget) { _ in scroll(proxy) }
                }
            }
        }
        .task {
            if !store.loaded { await store.load() } else { await store.refreshStatus() }
        }
    }

    private func scroll(_ proxy: ScrollViewProxy) {
        guard let scrollTarget else { return }
        DispatchQueue.main.async { withAnimation { proxy.scrollTo(scrollTarget, anchor: .top) } }
    }
}

struct SecuritySettingsTab: View {
    @ObservedObject var appState: AppState
    @ObservedObject var store: ConfigStore
    /// Setting key to scroll to and highlight (`require_mcp_auth`).
    var scrollTarget: String? = nil
    /// Profile to PRESELECT (not save) in the Anonymous callers picker.
    var anonymousPreselect: String? = nil

    var body: some View {
        ConfigTabContainer(store: store, scrollTarget: scrollTarget) {
            VStack(alignment: .leading, spacing: 0) {
                ConfigSectionView(store: store, sectionId: "security", fields: SettingsCatalog.security)
                Divider().padding(.vertical, 8)
                // Spec 108-k K20: custom (its options are the live profile list).
                AnonymousProfileSection(appState: appState, store: store, preselect: anonymousPreselect)
                    .id("anonymous_profile")
            }
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
    @State private var expanded: Set<String> = []

    var body: some View {
        ConfigTabContainer(store: store) {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(SettingsCatalog.advanced) { section in
                    let isOpen = expanded.contains(section.id)
                    VStack(alignment: .leading, spacing: 0) {
                        // Whole header row toggles the section (not just the chevron).
                        Button {
                            if isOpen { expanded.remove(section.id) } else { expanded.insert(section.id) }
                        } label: {
                            HStack(spacing: 8) {
                                Image(systemName: isOpen ? "chevron.down" : "chevron.right")
                                    .font(.caption.weight(.bold))
                                    .foregroundColor(.secondary)
                                Text(section.title).fontWeight(.semibold)
                                Spacer()
                            }
                            .contentShape(Rectangle()) // entire row is the hit target
                        }
                        .buttonStyle(.plain)

                        if isOpen {
                            ConfigSectionView(store: store, sectionId: section.id, fields: section.fields)
                                .padding(.top, 6)
                        }
                    }
                    .padding(12)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .cornerRadius(8)
                }
            }
        }
    }
}

/// Read-only view of the effective configuration the core is running, sourced
/// over REST (GET /api/v1/config via ConfigStore). Editing happens only in the
/// other tabs — this tab never writes, mirroring the tray's REST-only contract.
struct RawConfigTab: View {
    @ObservedObject var store: ConfigStore
    @State private var copied = false

    var body: some View {
        ConfigTabContainer(store: store) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .top, spacing: 12) {
                    Text("Read-only — the effective configuration the core is running. To change values, use the App, Security, General and Advanced tabs.")
                        .font(.callout)
                        .foregroundColor(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 0)
                    Button {
                        let pb = NSPasteboard.general
                        pb.clearContents()
                        pb.setString(store.prettyJSON, forType: .string)
                        copied = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
                    } label: {
                        Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                    }
                    .help("Copy the full configuration JSON")
                }

                Text(store.prettyJSON)
                    .font(.system(.callout, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
                    .background(Color(nsColor: .textBackgroundColor))
                    .cornerRadius(8)
            }
        }
    }
}
