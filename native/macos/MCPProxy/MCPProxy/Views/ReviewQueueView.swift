import SwiftUI

/// Native presenter of the one-server-per-row review queue. Definitions come
/// from an upstream server and are displayed as inert text.
struct ReviewQueueView: View {
    @ObservedObject var appState: AppState
    @State private var queue = ReviewQueueResponse(count: 0, servers: [])
    @State private var selected: String?

    var body: some View {
        Group {
            if let server = selected {
                ReviewSheet(serverName: server, appState: appState, onDismiss: { selected = nil })
            } else {
                List(queue.servers) { row in
                    Button { selected = row.server } label: {
                        HStack {
                            VStack(alignment: .leading) {
                                Text(row.server)
                                Text(row.kind == "server_review" ? "Server awaiting review" : "\(row.pending ?? 0) new · \(row.changed ?? 0) changed")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer(); Image(systemName: "chevron.right")
                        }
                    }.buttonStyle(.plain)
                }.overlay {
                    if queue.servers.isEmpty {
                        VStack(spacing: 8) { Image(systemName: "checkmark.shield").font(.title2).foregroundStyle(.secondary); Text("Nothing is waiting for review").foregroundStyle(.secondary) }
                    }
                }
            }
        }
        .navigationTitle("Review Queue")
        .task { await load() }
        .onReceive(NotificationCenter.default.publisher(for: .showReview)) { note in if let server = note.object as? String { selected = server } }
        .onReceive(NotificationCenter.default.publisher(for: .reviewChanged)) { _ in Task { await load() } }
    }

    private func load() async {
        guard let client = appState.apiClient else { return }
        queue = (try? await client.reviewQueue()) ?? queue
    }
}

/// Pure presentation rules of the review sheet (Spec 109 fix-review-screen).
/// The sentences are identical to the Web review screen
/// (frontend/src/utils/reviewPresentation.ts) and `mcpproxy review show`.
enum ReviewPresentation {
    enum Severity: Equatable { case error, warning, success, info }
    /// `.fetchDefinitions` marks the not-captured banner; its button lives in
    /// the dedicated capture row, so the banner itself shows no second one.
    enum BannerAction: Equatable { case rescan, scanNow, fetchDefinitions, none }
    struct Banner: Equatable { let severity: Severity; let text: String; let action: BannerAction }

    static let scanningBanner = Banner(severity: .info, text: "Scan in progress…", action: .none)

    /// Returns nil when the payload carries no scan. The risk score is shown
    /// only for a scan that covers every captured definition as it is now.
    static func scanBanner(_ scan: ReviewScan?, definitionsCaptured: Bool) -> Banner? {
        guard let scan else { return nil }
        let coverage = definitionsCaptured ? (scan.coverage ?? "none") : "not_captured"
        switch coverage {
        case "current":
            let tools = scan.toolsScanned ?? 0
            let severity: Severity = scan.verdict == "dangerous" ? .error : (scan.verdict == "clean" ? .success : .warning)
            return Banner(severity: severity, text: "Baseline scan: \(scan.verdict) · risk \(scan.riskScore ?? 0)/100 · covers all \(tools) \(tools == 1 ? "tool" : "tools")", action: .none)
        case "stale":
            let names = scan.unscannedTools ?? []
            let what = names.count == 1 ? "1 tool definition changed or was added" : "\(names.count) tool definitions changed or were added"
            let list = names.isEmpty ? "" : " (\(names.joined(separator: ", ")))"
            return Banner(severity: .warning, text: "Scan out of date: \(what) after the last scan\(list). Last result: \(scan.verdict).", action: .rescan)
        case "not_captured":
            return Banner(severity: .warning, text: "Scan not checked against tool definitions: they have not been captured yet.", action: .fetchDefinitions)
        case "tools_not_scanned":
            return Banner(severity: .warning, text: "The last scan did not analyse tool definitions (0 exported).", action: .rescan)
        case "scanning":
            return scanningBanner
        default:
            return Banner(severity: .warning, text: "Not scanned yet.", action: .scanNow)
        }
    }

    struct Headline: Equatable {
        enum State: Equatable { case review, approved }
        let state: State; let title: String; let subtitle: String
    }

    /// A server that is not quarantined and has nothing pending reads as approved.
    static func headline(_ review: ServerReviewResponse) -> Headline {
        let name = review.server.name
        let reviewSubtitle = "Review tool definitions before changing what agents can call."
        if review.server.quarantined { return Headline(state: .review, title: "Review \(name)", subtitle: reviewSubtitle) }
        let pending = review.tools.filter { $0.approvalStatus == "pending" || $0.approvalStatus == "changed" }.count
        if pending > 0 {
            return Headline(state: .review, title: "Review \(name)", subtitle: "\(pending) \(pending == 1 ? "tool needs" : "tools need") review. Agents cannot call \(pending == 1 ? "it" : "them") until approved.")
        }
        if review.tools.isEmpty {
            // Approved without seeing tools: still an approved server, with nothing captured yet.
            return Headline(state: .approved, title: "\(name) is approved", subtitle: "No tool definitions have been captured yet. New or changed tools come back here for review.")
        }
        let blocked = review.tools.filter(\.disabled).count
        let total = review.tools.count
        let summary = "All \(total) \(total == 1 ? "tool" : "tools") approved\(blocked > 0 ? " (\(blocked) blocked)" : "")."
        return Headline(state: .approved, title: "\(name) is approved", subtitle: "\(summary) New or changed tools come back here for review.")
    }

    // MARK: Default selection (Spec 109 fix-review-defaults, D43)
    // The core decides which tools start checked (`default_allowed`); these
    // helpers only read it. A missing field (an older core) reads as false, so a
    // mismatched core fails closed. Sentences match the Web screen.

    static let approveAllHint = "Allows every pending or changed tool. Tools you blocked earlier on a re-quarantined server stay blocked."
    static let selectionHint = "Only read-only tools with a clean scan start checked. Unchecked tools stay blocked after approval until you enable them on the Tools tab."

    /// What the user explicitly chose for one tool, with the payload they saw.
    struct Choice: Equatable { let allowed: Bool; let tool: ReviewTool }

    static func initialSelection(_ tools: [ReviewTool]) -> Set<String> {
        Set(tools.filter { $0.defaultAllowed == true }.map(\.name))
    }

    /// The selection after a reload: an explicit uncheck always survives; an
    /// explicit check survives only while the tool's payload is the one the user
    /// saw (a changed definition, verdict or tier falls back to the default).
    static func mergeSelection(_ tools: [ReviewTool], choices: [String: Choice]) -> Set<String> {
        Set(tools.filter { tool in
            guard let choice = choices[tool.name] else { return tool.defaultAllowed == true }
            if !choice.allowed { return false }
            return choice.tool == tool ? true : tool.defaultAllowed == true
        }.map(\.name))
    }

    static func approveLabel(selected: Int, total: Int, definitionsCaptured: Bool) -> String {
        if !definitionsCaptured || total == 0 { return "Approve Without Seeing Tools" }
        return "Approve Server (\(selected) of \(total) \(total == 1 ? "tool" : "tools"))"
    }

    static func approveAllLabel(total: Int) -> String { "Approve All (\(total) \(total == 1 ? "tool" : "tools"))" }

    enum ToolControl: Equatable { case allowToggle, approveReject, approved, blocked }

    /// The control a tool row gets: the quarantine toggle, Approve/Reject, or a plain state.
    static func toolState(_ tool: ReviewTool, quarantined: Bool) -> ToolControl {
        if quarantined { return .allowToggle }
        if tool.approvalStatus == "approved" { return tool.disabled ? .blocked : .approved }
        return .approveReject
    }
}

struct ReviewSheet: View {
    let serverName: String
    @ObservedObject var appState: AppState
    let onDismiss: () -> Void
    @State private var review: ServerReviewResponse?
    @State private var allowed = Set<String>()
    @State private var choices: [String: ReviewPresentation.Choice] = [:]
    @State private var pendingBlock: [String]?
    @State private var error: String?
    @State private var scanning = false
    @State private var showBlindApprovalConfirmation = false
    @State private var showForceApprovalConfirmation = false
    @State private var rescanning = false
    @State private var showRequarantineConfirmation = false

    var body: some View {
        VStack(alignment: .leading) {
            HStack {
                Button("Back") { onDismiss() }; Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text(headline?.title ?? "Review \(serverName)").font(.title2).bold()
                    if let subtitle = headline?.subtitle { Text(subtitle).font(.caption).foregroundStyle(.secondary) }
                }
            }.padding()
            if let error { Text(error).foregroundStyle(.red).padding(.horizontal) }
            if let server = review?.server {
                VStack(alignment: .leading, spacing: 3) {
                    Text("Transport: \(server.transport ?? "unknown")")
                    if let command = server.command, !command.isEmpty { Text("Command: \(command)").font(.caption.monospaced()) }
                    else if let url = server.url, !url.isEmpty { Text("URL: \(url)").font(.caption.monospaced()) }
                    if let trustMode = server.trustMode { Text("Trust mode: \(trustMode)") }
                    if let origin = server.sourceRegistryID { Text("Origin: \(origin)\(server.sourceRegistryProvenance.map { " · \($0)" } ?? "")") }
                }.font(.caption).foregroundStyle(.secondary).padding(.horizontal)
            }
            if let banner = scanBanner {
                HStack {
                    Label(banner.text, systemImage: bannerIcon(banner.severity)).font(.subheadline).foregroundStyle(bannerColor(banner.severity))
                    if banner.action == .rescan || banner.action == .scanNow {
                        Button(banner.action == .scanNow ? "Scan now" : "Rescan") { Task { await rescan() } }.disabled(rescanning)
                    }
                }.padding(.horizontal)
            }
            if review?.server.definitionsCaptured == false {
                HStack { Text(scanning ? "Scan started. Refreshing when it finishes…" : "Tool definitions have not been captured yet."); Button("Fetch tool definitions") { Task { await fetchDefinitions() } }.disabled(scanning) }.padding(.horizontal)
            }
            if let tools = review?.tools {
                List(tools) { tool in
                VStack(alignment: .leading, spacing: 6) {
                    HStack { Text(tool.name).font(.headline); Spacer(); Text(tool.scanVerdict).font(.caption).foregroundStyle(.secondary); Text(tool.tier).font(.caption).padding(4).background(.quaternary).clipShape(Capsule()) }
                    Text(verbatim: tool.description)
                    Text("from the server, not verified").font(.caption).foregroundStyle(.secondary)
                    DisclosureGroup("Definition") { Text(verbatim: definitionText(tool)).font(.caption.monospaced()) }
                    if tool.diff != nil || tool.previous != nil { DisclosureGroup("Changed definition") { Text(verbatim: diffText(tool)).font(.caption.monospaced()) } }
                    if review?.server.quarantined == true {
                        Toggle("Allow this tool", isOn: Binding(
                            get: { allowed.contains(tool.name) },
                            set: { isAllowed in
                                if isAllowed { allowed.insert(tool.name) }
                                else { allowed.remove(tool.name) }
                                // An explicit click survives a reload (D43.4).
                                choices[tool.name] = ReviewPresentation.Choice(allowed: isAllowed, tool: tool)
                            }
                        )).toggleStyle(.checkbox)
                    } else {
                        switch ReviewPresentation.toolState(tool, quarantined: false) {
                        case .approveReject:
                            HStack { Button("Approve") { Task { await approveTool(tool.name) } }; Button("Reject", role: .destructive) { Task { await rejectTool(tool.name) } } }
                        case .approved: Text("Approved").font(.caption).foregroundStyle(.green)
                        case .blocked: Text("Blocked").font(.caption).foregroundStyle(.red)
                        case .allowToggle: EmptyView()
                        }
                    }
                }
                }
            } else {
                Spacer()
            }
            if review?.server.quarantined == true {
                if let review, review.server.definitionsCaptured, !review.tools.isEmpty {
                    Text(ReviewPresentation.selectionHint).font(.caption).foregroundStyle(.secondary).padding(.horizontal)
                }
                HStack {
                    Button(ReviewPresentation.approveLabel(selected: allowed.count, total: review?.tools.count ?? 0, definitionsCaptured: review?.server.definitionsCaptured ?? false)) { requestApprove(everything: false) }.buttonStyle(.borderedProminent)
                    if let review, review.server.definitionsCaptured, !review.tools.isEmpty, allowed.count < review.tools.count {
                        Button(ReviewPresentation.approveAllLabel(total: review.tools.count)) { requestApprove(everything: true) }.help(ReviewPresentation.approveAllHint)
                    }
                    Button("Reject Server", role: .destructive) { Task { await rejectServer() } }
                }.padding()
            } else if headline?.state == .approved {
                HStack { Button("Quarantine to Review Again…") { showRequarantineConfirmation = true } }.padding()
            }
        }
        .task { await load() }
        .onReceive(NotificationCenter.default.publisher(for: .reviewChanged)) { _ in
            scanning = false
            rescanning = false
            Task { await load() }
        }
        .onReceive(NotificationCenter.default.publisher(for: .scanSettled)) { note in
            guard let settledServer = note.object as? String, settledServer == serverName else { return }
            scanning = false
            rescanning = false
            Task { await load() }
        }
        .alert("Approve without seeing tools?", isPresented: $showBlindApprovalConfirmation) { Button("Cancel", role: .cancel) {}; Button("Approve", role: .destructive) { Task { await approve(force: false) } } } message: { Text("No tool definitions were captured. Fetch them before approval whenever possible.") }
        .alert("Quarantine \(serverName) to review again?", isPresented: $showRequarantineConfirmation) { Button("Cancel", role: .cancel) {}; Button("Quarantine", role: .destructive) { Task { await requarantine() } } } message: { Text("Agents lose access to every tool on \(serverName) until you approve it again.") }
        .alert("Dangerous findings detected", isPresented: $showForceApprovalConfirmation) { Button("Cancel", role: .cancel) {}; Button("Force Approve", role: .destructive) { Task { await approve(force: true) } } } message: { Text("Force approval activates this server despite dangerous baseline scan findings.") }
    }

    private var headline: ReviewPresentation.Headline? { review.map(ReviewPresentation.headline) }
    private var scanBanner: ReviewPresentation.Banner? {
        guard let server = review?.server else { return nil }
        let banner = ReviewPresentation.scanBanner(server.scan, definitionsCaptured: server.definitionsCaptured)
        // A rescan the operator just started reads as in progress until it settles.
        return banner != nil && rescanning ? ReviewPresentation.scanningBanner : banner
    }
    private func bannerIcon(_ severity: ReviewPresentation.Severity) -> String {
        switch severity { case .error: return "xmark.octagon"; case .warning: return "exclamationmark.triangle"; case .success: return "checkmark.shield"; case .info: return "clock" }
    }
    private func bannerColor(_ severity: ReviewPresentation.Severity) -> Color {
        switch severity { case .error: return .red; case .warning: return .orange; case .success: return .green; case .info: return .secondary }
    }
    private func rescan() async {
        guard let client = appState.apiClient else { return }
        rescanning = true
        do { try await client.startSecurityScan(serverName) } catch { rescanning = false; self.error = error.localizedDescription }
    }
    private func requarantine() async {
        guard let client = appState.apiClient else { return }
        do { try await client.quarantineServer(serverName); await load(); NotificationCenter.default.post(name: .reviewChanged, object: nil) }
        catch { self.error = error.localizedDescription }
    }
    private func load() async {
        guard let client = appState.apiClient else { return }
        do { let value = try await client.serverReview(serverName); review = value; allowed = ReviewPresentation.mergeSelection(value.tools, choices: choices) } catch { self.error = error.localizedDescription }
    }
    private func requestApprove(everything: Bool) {
        if review?.server.definitionsCaptured == false { pendingBlock = nil; showBlindApprovalConfirmation = true; return }
        pendingBlock = everything ? [] : nil
        Task { await approve(force: false) }
    }
    /// The force retry re-sends the block list of the attempt that triggered it (D43.5).
    private func approve(force: Bool) async {
        guard let client = appState.apiClient, let review else { return }
        let block = pendingBlock ?? review.tools.map(\.name).filter { !allowed.contains($0) }
        pendingBlock = block
        do { try await client.securityApproveServer(serverName, force: force, block: block); choices = [:]; pendingBlock = nil; await load() }
        catch { self.error = error.localizedDescription; if !force, case let APIClientError.httpError(status, message) = error, status == 409, message.localizedCaseInsensitiveContains("dangerous") { showForceApprovalConfirmation = true } }
    }
    private func fetchDefinitions() async {
        guard let client = appState.apiClient else { return }
        scanning = true
        do { try await client.discoverServerTools(serverName); scanning = false; await load() }
        catch { scanning = false; self.error = error.localizedDescription }
    }
    private func approveTool(_ name: String) async { guard let client = appState.apiClient else { return }; do { try await client.approveSpecificTools(serverName, tools: [name]); await load() } catch { self.error = error.localizedDescription } }
    private func rejectTool(_ name: String) async { guard let client = appState.apiClient else { return }; do { try await client.blockSpecificTools(serverName, tools: [name]); await load() } catch { self.error = error.localizedDescription } }
    private func rejectServer() async { guard let client = appState.apiClient else { return }; do { try await client.securityRejectServer(serverName); await load() } catch { self.error = error.localizedDescription } }
    private func definitionText(_ tool: ReviewTool) -> String {
        "input schema:\n\(tool.inputSchema?.prettyString ?? "null")\n\noutput schema:\n\(tool.outputSchema?.prettyString ?? "null")\n\nannotations:\n\(tool.annotations?.prettyString ?? "null")"
    }
    private func diffText(_ tool: ReviewTool) -> String {
        var sections: [String] = []
        if let diff = tool.diff {
            if let description = diff.description, !description.isEmpty { sections.append("description:\n\(description)") }
            if let schema = diff.inputSchema, !schema.isEmpty { sections.append("input schema:\n\(schema)") }
            if let schema = diff.outputSchema, !schema.isEmpty { sections.append("output schema:\n\(schema)") }
            if let annotations = diff.annotations, !annotations.isEmpty { sections.append("annotations:\n\(annotations)") }
        }
        if sections.isEmpty, let previous = tool.previous {
            sections.append("previous description:\n\(previous.description)")
            sections.append("previous input schema:\n\(previous.inputSchema?.prettyString ?? "null")")
            sections.append("previous output schema:\n\(previous.outputSchema?.prettyString ?? "null")")
            sections.append("previous annotations:\n\(previous.annotations?.prettyString ?? "null")")
        }
        return sections.isEmpty ? "No changed fields supplied." : sections.joined(separator: "\n\n")
    }
}
