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

struct ReviewSheet: View {
    let serverName: String
    @ObservedObject var appState: AppState
    let onDismiss: () -> Void
    @State private var review: ServerReviewResponse?
    @State private var allowed = Set<String>()
    @State private var error: String?
    @State private var scanning = false
    @State private var showBlindApprovalConfirmation = false
    @State private var showForceApprovalConfirmation = false

    var body: some View {
        VStack(alignment: .leading) {
            HStack { Button("Back") { onDismiss() }; Spacer(); Text("Review \(serverName)").font(.title2).bold() }.padding()
            if let error { Text(error).foregroundStyle(.red).padding(.horizontal) }
            if let scan = review?.server.scan { Text("Baseline scan: \(scan.verdict)\(scan.riskScore.map { " · risk \($0)/100" } ?? "")").font(.subheadline).padding(.horizontal) }
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
                            }
                        )).toggleStyle(.checkbox)
                    } else if tool.approvalStatus == "pending" || tool.approvalStatus == "changed" {
                        HStack { Button("Approve") { Task { await approveTool(tool.name) } }; Button("Reject", role: .destructive) { Task { await rejectTool(tool.name) } } }
                    }
                }
                }
            } else {
                Spacer()
            }
            if review?.server.quarantined == true {
                HStack {
                    Button("Approve Server (\(allowed.count) tools)") { requestApprove() }.buttonStyle(.borderedProminent)
                    Button("Reject Server", role: .destructive) { Task { await rejectServer() } }
                }.padding()
            }
        }
        .task { await load() }
        .onReceive(NotificationCenter.default.publisher(for: .reviewChanged)) { _ in
            scanning = false
            Task { await load() }
        }
        .onReceive(NotificationCenter.default.publisher(for: .scanSettled)) { note in
            guard let settledServer = note.object as? String, settledServer == serverName else { return }
            scanning = false
            Task { await load() }
        }
        .alert("Approve without seeing tools?", isPresented: $showBlindApprovalConfirmation) { Button("Cancel", role: .cancel) {}; Button("Approve", role: .destructive) { Task { await approve(force: false) } } } message: { Text("No tool definitions were captured. Fetch them before approval whenever possible.") }
        .alert("Dangerous findings detected", isPresented: $showForceApprovalConfirmation) { Button("Cancel", role: .cancel) {}; Button("Force Approve", role: .destructive) { Task { await approve(force: true) } } } message: { Text("Force approval activates this server despite dangerous baseline scan findings.") }
    }

    private func load() async {
        guard let client = appState.apiClient else { return }
        do { let value = try await client.serverReview(serverName); review = value; allowed = Set(value.tools.filter { !$0.disabled }.map(\.name)) } catch { self.error = error.localizedDescription }
    }
    private func requestApprove() { if review?.server.definitionsCaptured == false { showBlindApprovalConfirmation = true } else { Task { await approve(force: false) } } }
    private func approve(force: Bool) async {
        guard let client = appState.apiClient, let review else { return }
        do { try await client.securityApproveServer(serverName, force: force, block: review.tools.map(\.name).filter { !allowed.contains($0) }); await load() }
        catch { self.error = error.localizedDescription; if !force, case let APIClientError.httpError(status, message) = error, status == 409, message.localizedCaseInsensitiveContains("dangerous") { showForceApprovalConfirmation = true } }
    }
    private func fetchDefinitions() async {
        guard let client = appState.apiClient else { return }
        scanning = true
        do { try await client.startSecurityScan(serverName) }
        catch { scanning = false; self.error = error.localizedDescription }
    }
    private func approveTool(_ name: String) async { guard let client = appState.apiClient else { return }; do { try await client.approveSpecificTools(serverName, tools: [name]); await load() } catch { self.error = error.localizedDescription } }
    private func rejectTool(_ name: String) async { guard let client = appState.apiClient else { return }; do { try await client.blockSpecificTools(serverName, tools: [name]); await load() } catch { self.error = error.localizedDescription } }
    private func rejectServer() async { guard let client = appState.apiClient else { return }; do { try await client.securityRejectServer(serverName); await load() } catch { self.error = error.localizedDescription } }
    private func definitionText(_ tool: ReviewTool) -> String { "input_schema: \(String(describing: tool.inputSchema))\noutput_schema: \(String(describing: tool.outputSchema))\nannotations: \(String(describing: tool.annotations))" }
    private func diffText(_ tool: ReviewTool) -> String { "diff: \(String(describing: tool.diff))\nprevious: \(String(describing: tool.previous))" }
}
