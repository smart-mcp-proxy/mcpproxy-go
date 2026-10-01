// ProfileEditorView.swift
// MCPProxy
//
// Spec 108-k (K7, FR-047, FR-041, FR-005): edit a profile. A form for the
// profile's fields, a per-tool table showing what the profile does to each tool
// (with Allow / Deny and, for unannotated tools only, Classify), and "Try it",
// which previews a search under the UNSAVED draft. The table is a list with a
// header row so a fix from the access explainer can scroll to and select one
// row. Wide windows put the form and the table side by side; narrow ones stack
// them.

import SwiftUI

struct ProfileEditorView: View {
    @ObservedObject var appState: AppState
    let target: ProfileEditorTarget
    let onClose: () -> Void

    @StateObject private var model: ProfileEditorModel
    @State private var toolFilter = ""
    @State private var hiddenOnly = false
    @State private var highlightedTool: String?
    @State private var showRename = false
    @State private var showDelete = false
    @State private var showAssign = false
    @State private var focusApplied = false

    init(appState: AppState, target: ProfileEditorTarget, onClose: @escaping () -> Void) {
        self.appState = appState
        self.target = target
        self.onClose = onClose
        let source: ProfileEditorSource = appState.apiClient ?? UnavailableProfileSource()
        switch target {
        case .new:
            _model = StateObject(wrappedValue: ProfileEditorModel(source: source, profile: nil))
        case .existing(let name, _):
            _model = StateObject(wrappedValue: ProfileEditorModel(
                source: source, profile: appState.profiles.first { $0.name == name } ?? ProfileView(name: name)))
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if model.notFound {
                notFound
            } else {
                GeometryReader { proxy in
                    if proxy.size.width >= 1100 && !model.isNew {
                        HStack(alignment: .top, spacing: 0) {
                            ScrollView { formSection.padding() }.frame(width: min(480, proxy.size.width * 0.42))
                            Divider()
                            toolsSection.padding()
                        }
                    } else {
                        ScrollView {
                            VStack(alignment: .leading, spacing: 16) {
                                formSection
                                if !model.isNew { Divider(); toolsSection }
                            }
                            .padding()
                        }
                    }
                }
            }
        }
        .accessibilityIdentifier("profile-editor")
        .task {
            await model.load()
            applyFocus()
        }
        .onChange(of: model.effective) { _ in applyFocus() }
        .onChange(of: appState.profiles) { profiles in
            if let name = model.original?.name, let fresh = profiles.first(where: { $0.name == name }) {
                model.profileChangedElsewhere(fresh)
            }
        }
        .onChange(of: model.announcement) { text in
            if let text { announce(text) }
        }
        .sheet(isPresented: $showRename) {
            RenameProfileSheet(appState: appState, model: model)
        }
        .sheet(isPresented: $showDelete) {
            if let original = model.original {
                DeleteProfileSheet(appState: appState, profile: original, model: model) { onClose() }
            }
        }
        .sheet(isPresented: $showAssign) {
            if let original = model.original { AssignProfileSheet(appState: appState, profile: original) }
        }
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 10) {
            Button { onClose() } label: { Label("Profiles", systemImage: "chevron.left") }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("profile-editor-back")
            Text(model.isNew ? "New profile" : model.draft.title.isEmpty ? (model.original?.name ?? "") : model.draft.title)
                .font(.title3.bold()).lineLimit(1)
            if model.isDirty { Text("Unsaved changes").font(.caption).foregroundStyle(.orange) }
            Spacer()
            if model.isSaving || model.isLoading { ProgressView().controlSize(.small) }
            if !model.isNew {
                Button("Rename…") { showRename = true }.accessibilityIdentifier("profile-editor-rename")
                Button("Delete…", role: .destructive) { showDelete = true }
                    .accessibilityIdentifier("profile-editor-delete")
            }
            Button("Revert") { model.revert() }
                .disabled(!model.isDirty)
                .accessibilityIdentifier("profile-editor-revert")
            Button(model.isNew ? "Create" : "Save") { Task { await save() } }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut("s", modifiers: .command)
                .disabled((!model.isDirty && !model.isNew) || model.isSaving || (model.isNew && model.draft.name.isEmpty))
                .accessibilityIdentifier("profile-editor-save")
        }
        .padding(.horizontal).padding(.vertical, 10)
    }

    private var notFound: some View {
        VStack(spacing: 8) {
            Image(systemName: "questionmark.folder").font(.largeTitle).foregroundStyle(.tertiary)
            Text("Profile not found").font(.headline)
            Text(model.loadError ?? "It may have been deleted or renamed.")
                .font(.caption).foregroundStyle(.secondary)
            Button("Back to Profiles") { onClose() }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func save() async {
        let wasNew = model.isNew
        await model.save()
        if wasNew, model.hasBeenCreated, model.fieldError == nil, model.guardRefusal == nil, model.errorMessage == nil {
            // A created profile is now an existing one: reopen on it.
            onClose()
            if let name = model.original?.name {
                appState.openProfileEditor(name: name)
            }
        }
    }

    // MARK: Form

    @ViewBuilder
    private var formSection: some View {
        VStack(alignment: .leading, spacing: 14) {
            if model.changedElsewhere {
                HStack {
                    Image(systemName: "arrow.triangle.2.circlepath")
                    Text("Changed elsewhere — Reload").font(.callout)
                    Spacer()
                    Button("Reload") { Task { await model.reload() } }
                        .accessibilityIdentifier("profile-editor-reload")
                }
                .padding(8).background(Color.blue.opacity(0.1)).clipShape(RoundedRectangle(cornerRadius: 6))
            }
            if let refusal = model.guardRefusal {
                GuardRefusalView(body: refusal) { route in appState.navigate(route) }
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.red)
            }
            ForEach(model.warnings, id: \.self) { warning in
                Label(warning, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
            }

            // Name, title, description
            if model.isNew {
                field("Name (slug)", errorFields: ["name"]) {
                    TextField("e.g. work-readonly", text: $model.draft.name)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityIdentifier("profile-editor-name")
                }
            }
            field("Title", counter: "\(model.draft.title.count)/80", errorFields: ["title"]) {
                TextField("Shown in lists and menus", text: $model.draft.title)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("profile-editor-title")
            }
            field("Description", counter: "\(model.draft.description.count)/500", errorFields: ["description"]) {
                TextField("What this profile is for", text: $model.draft.description, axis: .vertical)
                    .lineLimit(2...4)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("profile-editor-description")
            }

            serversField
            tierField
            unannotatedField
            toggleField("Code execution", errorFields: ["code_execution"], value: $model.draft.codeExecution,
                        effective: model.original?.effectiveCodeExecution.map { $0 ? "On" : "Off" },
                        identifier: "profile-editor-code-execution")
            toggleField("Management tools", errorFields: ["management_tools"], value: $model.draft.managementTools,
                        effective: nil, identifier: "profile-editor-management-tools")
            switchableField

            if !model.isNew { assignedTo }
        }
    }

    private func field<Content: View>(
        _ title: String, counter: String? = nil, errorFields: [String], @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title).font(.subheadline.weight(.semibold))
                Spacer()
                if let counter { Text(counter).font(.caption2).foregroundStyle(.secondary) }
            }
            content()
            if let error = model.fieldError, errorFields.contains(error.field) {
                Label(error.message, systemImage: "exclamationmark.circle.fill")
                    .font(.caption).foregroundStyle(.red)
                    .accessibilityIdentifier("profile-editor-error-\(error.field)")
            }
        }
    }

    private var serversField: some View {
        let configured = appState.servers.map(\.name)
        let extra = model.draft.servers.filter { !configured.contains($0) }
        return field("Servers", errorFields: ["servers"]) {
            VStack(alignment: .leading, spacing: 2) {
                if configured.isEmpty && extra.isEmpty {
                    Text("No servers configured.").font(.caption).foregroundStyle(.secondary)
                }
                ForEach(configured + extra, id: \.self) { name in
                    Toggle(isOn: Binding(
                        get: { model.draft.servers.contains(name) },
                        set: { on in
                            if on { if !model.draft.servers.contains(name) { model.draft.servers.append(name) } }
                            else { model.draft.servers.removeAll { $0 == name } }
                        })) {
                        HStack(spacing: 4) {
                            Text(name)
                            if extra.contains(name) { Text("(not configured)").font(.caption).foregroundStyle(.orange) }
                        }
                    }
                    .accessibilityIdentifier("profile-editor-server-\(name)")
                }
            }
        }
    }

    private var tierField: some View {
        field("Max tool tier", errorFields: ["max_tier"]) {
            Picker("Max tool tier", selection: Binding(
                get: { model.draft.maxTier ?? "" },
                set: { model.draft.maxTier = $0.isEmpty ? nil : $0 })) {
                Text("Read").tag("read")
                Text("+ Write").tag("write")
                Text("+ Destructive").tag("destructive")
                Text("No cap").tag("")
            }
            .pickerStyle(.segmented).labelsHidden()
            .accessibilityIdentifier("profile-editor-max-tier")
        }
    }

    private var unannotatedField: some View {
        field("Unannotated tools", errorFields: ["unannotated"]) {
            Picker("Unannotated tools", selection: Binding(
                get: { model.draft.unannotated ?? "" },
                set: { model.draft.unannotated = $0.isEmpty ? nil : $0 })) {
                Text("Hide").tag("deny")
                Text("Treat as write").tag("as_write")
                Text("Treat as read").tag("as_read")
                Text("Default\(model.original?.effectiveUnannotated.map { " (\($0))" } ?? "")").tag("")
            }
            .labelsHidden()
            .accessibilityIdentifier("profile-editor-unannotated")
        }
    }

    private func toggleField(
        _ title: String, errorFields: [String], value: Binding<Bool?>, effective: String?, identifier: String
    ) -> some View {
        field(title, errorFields: errorFields) {
            Picker(title, selection: Binding<Int>(
                get: { value.wrappedValue == nil ? 0 : (value.wrappedValue == true ? 1 : 2) },
                set: { value.wrappedValue = $0 == 0 ? nil : $0 == 1 })) {
                Text("Inherit\(effective.map { " (\($0))" } ?? "")").tag(0)
                Text("On").tag(1)
                Text("Off").tag(2)
            }
            .pickerStyle(.segmented).labelsHidden()
            .accessibilityIdentifier(identifier)
        }
    }

    private var switchableField: some View {
        let others = appState.profiles.filter { $0.name != model.draft.name }
        return field("Agent may switch to", errorFields: ["switchable_to"]) {
            VStack(alignment: .leading, spacing: 4) {
                Picker("Agent may switch to", selection: Binding<Int>(
                    get: {
                        switch model.draft.switchableTo { case .unset: return 0; case .none: return 1; case .list: return 2 }
                    },
                    set: { choice in
                        switch choice {
                        case 0: model.draft.switchableTo = .unset
                        case 1: model.draft.switchableTo = .none
                        default: model.draft.switchableTo = .list(others.first.map { [$0.name] } ?? [])
                        }
                    })) {
                    Text("Not set").tag(0)
                    Text("None").tag(1)
                    Text("These profiles").tag(2)
                }
                .pickerStyle(.segmented).labelsHidden()
                .accessibilityIdentifier("profile-editor-switchable-mode")
                if case .list(let names) = model.draft.switchableTo {
                    ForEach(others) { other in
                        Toggle(other.displayTitle, isOn: Binding(
                            get: { names.contains(other.name) },
                            set: { on in
                                var next = names
                                if on { next.append(other.name) } else { next.removeAll { $0 == other.name } }
                                model.draft.switchableTo = next.isEmpty ? .none : .list(next)
                            }))
                    }
                }
            }
        }
    }

    private var assignedTo: some View {
        let impact = model.impact(in: appState.profiles)
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Assigned to").font(.subheadline.weight(.semibold))
                Spacer()
                Button("Assign…") { showAssign = true }
                    .accessibilityIdentifier("profile-editor-assign")
            }
            if impact.isEmpty {
                Text("Not assigned to any client or token yet.").font(.caption).foregroundStyle(.secondary)
            } else {
                ForEach(impact.lines, id: \.self) { Text($0).font(.caption) }
            }
        }
    }

    // MARK: Tools

    @ViewBuilder
    private var toolsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Tools").font(.headline)
                if let counts = model.effective?.counts {
                    Text("\(counts.visible) visible · \(counts.hidden) hidden")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                TextField("Filter tools", text: $toolFilter)
                    .textFieldStyle(.roundedBorder).frame(width: 160)
                    .accessibilityIdentifier("profile-editor-tool-filter")
                Toggle("Hidden only", isOn: $hiddenOnly).toggleStyle(.checkbox)
            }
            if model.isDirty {
                Text("The table shows the saved profile. Save to see the effect of your edits, or use Try it below for a preview.")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            toolTable
            tryIt
        }
    }

    private var rows: [EffectiveTool] {
        let all = model.effective?.tools ?? []
        let typed = toolFilter.lowercased()
        return all.filter { row in
            (!hiddenOnly || !row.access.visible)
                && (typed.isEmpty || row.fullName.lowercased().contains(typed))
        }
    }

    @ViewBuilder
    private var toolTable: some View {
        if model.effective == nil {
            if model.isLoading { ProgressView("Loading tools…").controlSize(.small) }
            else { Text("No tools indexed for this profile yet.").font(.caption).foregroundStyle(.secondary) }
        } else if model.draft.servers.isEmpty {
            Text("Choose servers to see their tools").font(.caption).foregroundStyle(.secondary)
        } else {
            // Columns total ~530 pt (Tool flexes), so the table fits a 900 pt
            // window's ~640 pt content area without clipping or a horizontal
            // scroll (Spec 108-k live QA). Tier and Access share one stacked
            // column to save width.
            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    Text("Tool").frame(minWidth: 120, maxWidth: .infinity, alignment: .leading)
                    Text("Tier / Access").frame(width: 150, alignment: .leading)
                    Text("Rule").frame(width: 110, alignment: .leading)
                    Text("Classify").frame(width: 110, alignment: .leading)
                }
                .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                .padding(.horizontal, 8).padding(.vertical, 4)
                .accessibilityAddTraits(.isHeader)
                Divider()
                ScrollViewReader { proxy in
                    List(rows) { row in
                        toolRow(row)
                            .id(row.id)
                            .listRowBackground(highlightedTool == row.fullName ? Color.accentColor.opacity(0.18) : Color.clear)
                    }
                    .listStyle(.plain)
                    .frame(minHeight: 220)
                    .onChange(of: highlightedTool) { tool in
                        guard let tool else { return }
                        withAnimation { proxy.scrollTo(tool, anchor: .center) }
                    }
                }
            }
            .background(Color.secondary.opacity(0.04))
            .clipShape(RoundedRectangle(cornerRadius: 6))
        }
    }

    @ViewBuilder
    private func toolRow(_ row: EffectiveTool) -> some View {
        let rule = model.ruleState(for: row.fullName)
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 8) {
                Text(row.fullName).font(.callout.monospaced()).lineLimit(1).truncationMode(.middle)
                    .frame(minWidth: 120, maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: 2) {
                    Text(tierText(row)).font(.caption).lineLimit(2)
                    HStack(spacing: 4) {
                        Image(systemName: row.access.visible ? "eye" : "eye.slash")
                        Text(row.access.visible ? "Visible" : ProfileEditorModel.reasonLabel(row.access.reason)).lineLimit(2)
                    }
                    .font(.caption).foregroundStyle(row.access.visible ? Color.primary : Color.orange)
                }
                .frame(width: 150, alignment: .leading)
                HStack(spacing: 4) {
                    Toggle("Allow", isOn: Binding(
                        get: { rule == .allow }, set: { _ in model.toggle(.allow, for: row.fullName) }))
                        .toggleStyle(.button).controlSize(.small)
                        .accessibilityLabel("Allow \(row.fullName) in \(model.draft.title.isEmpty ? model.draft.name : model.draft.title)")
                    Toggle("Deny", isOn: Binding(
                        get: { rule == .deny }, set: { _ in model.toggle(.deny, for: row.fullName) }))
                        .toggleStyle(.button).controlSize(.small)
                        .accessibilityLabel("Deny \(row.fullName) in \(model.draft.title.isEmpty ? model.draft.name : model.draft.title)")
                }
                .frame(width: 110, alignment: .leading)
                Group {
                    if model.canClassify(row) {
                        Menu(model.classification(for: row.fullName)?.label ?? "Classify") {
                            Button("Mark as read") { model.classify(row, as: .read) }
                            Button("Mark as write") { model.classify(row, as: .write) }
                            Button("Mark as destructive") { model.classify(row, as: .destructive) }
                            if model.classification(for: row.fullName) != nil {
                                Divider()
                                Button("Remove classification") { model.classify(row, as: nil) }
                            }
                        }
                        .menuStyle(.borderlessButton)
                    } else {
                        Text("—").foregroundStyle(.tertiary)
                    }
                }
                .font(.caption).frame(width: 110, alignment: .leading)
            }
            if let stale = model.staleMarker(for: row) {
                HStack {
                    Label(stale, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                    Button("Remove classification") { model.removeClassification(row) }
                        .buttonStyle(.link).font(.caption)
                }
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("profile-tool-row-\(row.server)__\(row.tool)")
    }

    /// `Write → Read` when the profile changes the tool's tier, else the tier.
    private func tierText(_ row: EffectiveTool) -> String {
        row.intrinsicTier == row.profileTier
            ? row.intrinsicTier.label
            : "\(row.intrinsicTier.label) → \(row.profileTier.label)"
    }

    // MARK: Try it

    private var tryIt: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Try it").font(.subheadline.weight(.semibold))
            Text("Search as an agent on this profile would — using your unsaved edits.")
                .font(.caption).foregroundStyle(.secondary)
            HStack {
                TextField("Search, for example “issue”", text: $model.tryQuery)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await model.tryIt() } }
                    .accessibilityIdentifier("profile-try-query")
                Button("Try") { Task { await model.tryIt() } }
                    .disabled(model.tryQuery.trimmingCharacters(in: .whitespaces).isEmpty || model.isTrying)
                    .accessibilityIdentifier("profile-try")
                if model.isTrying { ProgressView().controlSize(.small) }
            }
            if let result = model.tryResult {
                Text("Hidden by profile: \(result.hiddenByProfile)").font(.caption.weight(.semibold))
                    .accessibilityIdentifier("profile-try-hidden-count")
                ForEach(result.results) { hit in
                    HStack(alignment: .firstTextBaseline) {
                        Text(hit.displayName).font(.caption.monospaced())
                        Text(hit.description).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                if result.results.isEmpty { Text("No visible matches.").font(.caption).foregroundStyle(.secondary) }
            }
        }
    }

    // MARK: Focus (explainer fixes)

    private func applyFocus() {
        guard !focusApplied, case .existing(_, let focus?) = target else { return }
        guard let tools = model.effective?.tools, tools.contains(where: { $0.fullName == focus }) else { return }
        focusApplied = true
        toolFilter = ""
        hiddenOnly = false
        highlightedTool = focus
    }

    private func announce(_ text: String) { AccessibilityAnnouncer.post(text) }
}
