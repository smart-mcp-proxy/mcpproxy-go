// ProfilesView.swift
// MCPProxy
//
// Spec 108-k (K6, FR-047): the Profiles page. A profile is a named scope (which
// servers, how much tool risk) that a client credential or a token is bound to.
// The page lists them as cards, with the built-in "All servers" card first, and
// opens the editor. The goal flow it serves: create a profile, assign it to a
// client, mint a token with it.

import SwiftUI

/// Where the page is: the list, or the editor on a new or an existing profile.
enum ProfileEditorTarget: Hashable {
    case new
    case existing(name: String, focusTool: String?)
}

struct ProfilesView: View {
    @ObservedObject var appState: AppState
    @State private var editor: ProfileEditorTarget?
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var loaded = false
    @State private var assigning: ProfileView?
    @State private var deleting: ProfileView?
    @State private var tokenProfile: ProfileView?

    var body: some View {
        Group {
            if let editor {
                ProfileEditorView(appState: appState, target: editor) {
                    self.editor = nil
                    Task { await refresh() }
                }
                .id(editor)
            } else {
                list
            }
        }
        .accessibilityIdentifier("profiles-view")
        .task { await refresh() }
        .onAppear { consumeHandOffs() }
        .onChange(of: appState.pendingRoute) { _ in consumeHandOffs() }
        .onChange(of: appState.pendingAddAction) { _ in consumeHandOffs() }
        .sheet(item: $assigning) { profile in
            AssignProfileSheet(appState: appState, profile: profile)
        }
        .sheet(item: $deleting) { profile in
            DeleteProfileSheet(appState: appState, profile: profile) {
                Task { await refresh() }
            }
        }
        .sheet(item: $tokenProfile) { profile in
            CreateTokenSheet(appState: appState, presetProfile: profile.name) { _ in }
        }
    }

    // MARK: Hand-offs

    private func consumeHandOffs() {
        if appState.consumePendingAddAction(for: [.profile]) == .profile {
            editor = .new
        }
        if case .profiles? = appState.pendingRoute {
            appState.pendingRoute = nil
            editor = nil
        }
        if let target: ProfileEditorTarget = appState.consumeRoute({ route in
            if case .profileEditor(let name, let focus) = route { return .existing(name: name, focusTool: focus) }
            return nil
        }) {
            editor = target
        }
    }

    // MARK: List

    private var list: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("Profiles").font(.title2.bold())
                    Text("A profile limits which servers and tool risk levels a client or token can reach.")
                        .font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer()
                if isLoading { ProgressView().controlSize(.small) }
                Button { Task { await refresh() } } label: { Image(systemName: "arrow.clockwise") }
                    .buttonStyle(.borderless).help("Refresh profiles")
                Button("New Profile") { editor = .new }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut("n", modifiers: .command)
                    .accessibilityIdentifier("profiles-new")
            }
            .padding()
            Divider()

            if let errorMessage {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(errorMessage).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button("Retry") { Task { await refresh() } }.buttonStyle(.borderless)
                }
                .padding(.horizontal).padding(.vertical, 8)
                .background(Color.orange.opacity(0.1))
            }

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    allServersCard
                    if !loaded && isLoading {
                        ProgressView("Loading profiles…").frame(maxWidth: .infinity).padding()
                    } else if appState.profiles.isEmpty && errorMessage == nil {
                        VStack(spacing: 8) {
                            Text("No profiles yet. Profiles limit which servers and tool tiers a client or token can use.")
                                .font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                            Button("New Profile") { editor = .new }
                        }
                        .frame(maxWidth: .infinity).padding()
                    }
                    ForEach(appState.profiles) { profile in
                        card(profile)
                    }
                }
                .padding()
            }
        }
    }

    // MARK: All servers card

    private var allServersCard: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: "square.stack.3d.up").foregroundStyle(.secondary)
                Text("All servers").font(.headline)
                Spacer()
            }
            Text("Clients and agents with no profile, and anonymous callers when no anonymous profile is set, reach every enabled server.")
                .font(.callout).foregroundStyle(.secondary)
            HStack {
                Text(appState.anonymousProfile.isEmpty
                     ? "Anonymous callers: All servers"
                     : "Anonymous callers: confined to \(appState.anonymousProfile)")
                    .font(.caption)
                Button("Change in Settings") {
                    appState.navigate(.settings(.anonymousProfile(preselect: nil)))
                }
                .buttonStyle(.link).font(.caption)
                .accessibilityIdentifier("profiles-change-anonymous")
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("profile-card-all-servers")
    }

    // MARK: Card

    @ViewBuilder
    private func card(_ profile: ProfileView) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(profile.displayTitle).font(.headline)
                    if profile.displayTitle != profile.name {
                        Text(profile.name).font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                }
                if profile.isServersOnly {
                    Text("Servers only")
                        .font(.caption2).padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Color.secondary.opacity(0.15)).clipShape(Capsule())
                        .help("A profile that limits servers only; it sets no tool-tier policy.")
                }
                Spacer()
                Button("Edit") { editor = .existing(name: profile.name, focusTool: nil) }
                    .accessibilityIdentifier("profile-edit-\(profile.name)")
                Menu {
                    Button("Assign to client…") { assigning = profile }
                    Button("Create token with this profile…") { tokenProfile = profile }
                    Divider()
                    Button("Delete…", role: .destructive) { deleting = profile }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton).frame(width: 28)
                .accessibilityLabel("More actions for \(profile.displayTitle)")
                .accessibilityIdentifier("profile-actions-\(profile.name)")
            }

            if let description = profile.description, !description.isEmpty {
                Text(description).font(.callout).foregroundStyle(.secondary)
            }

            HStack(spacing: 14) {
                Label("Max tier: \(profile.maxTierLabel)", systemImage: "gauge.with.dots.needle.33percent")
                Label("\(profile.reachableServers.count) server\(profile.reachableServers.count == 1 ? "" : "s")",
                      systemImage: "server.rack")
                if let counts = profile.toolCounts {
                    Text("Read \(counts.read) · Write \(counts.write) · Destructive \(counts.destructive)")
                    if counts.unannotatedHidden > 0 {
                        Text("\(counts.unannotatedHidden) unannotated hidden").foregroundStyle(.orange)
                    }
                } else {
                    Text("\(profile.toolCount) tools")
                }
            }
            .font(.caption)

            usedByLine(profile)

            if let calls = profile.calls24h {
                Text("\(calls) calls · \(profile.blocked24h ?? 0) blocked in 24 h")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if appState.scopeFiltersAvailable {
                HStack(spacing: 4) {
                    Text("Show:").font(.caption).foregroundStyle(.secondary)
                    linkButton("Tools", page: .tools, profile: profile)
                    Text("·").foregroundStyle(.tertiary)
                    linkButton("Activity", page: .activity, profile: profile)
                    Text("·").foregroundStyle(.tertiary)
                    linkButton("Clients", page: .clients, profile: profile)
                    Text("·").foregroundStyle(.tertiary)
                    linkButton("Tokens", page: .tokens, profile: profile)
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("profile-card-\(profile.name)")
    }

    @ViewBuilder
    private func usedByLine(_ profile: ProfileView) -> some View {
        if let used = profile.usedBy {
            if used.isEmpty {
                Text("Not used by any client or token").font(.caption).foregroundStyle(.secondary)
            } else {
                HStack(spacing: 6) {
                    Text("Used by:").font(.caption).foregroundStyle(.secondary)
                    ForEach(used.clients, id: \.id) { client in
                        Label(client.id, systemImage: client.mode == .locked ? "lock.fill" : "arrow.triangle.swap")
                            .font(.caption)
                            .accessibilityLabel("\(client.id), \(client.mode == .locked ? "locked" : "switchable")")
                    }
                    ForEach(used.tokens, id: \.self) { token in
                        Label(token, systemImage: "key.fill").font(.caption)
                    }
                    if used.anonymousProfile {
                        Label("Anonymous callers", systemImage: "person.fill.questionmark").font(.caption)
                    }
                }
            }
        }
    }

    private func linkButton(_ title: String, page: ScopePage, profile: ProfileView) -> some View {
        Button(title) { appState.openScoped(page: page, filter: .forProfile(profile.name)) }
            .buttonStyle(.link).font(.caption)
            .accessibilityLabel("\(title) for \(profile.displayTitle)")
            .accessibilityIdentifier("profile-link-\(title.lowercased())-\(profile.name)")
    }

    // MARK: Loading

    private func refresh() async {
        guard let apiClient = appState.apiClient else { return }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false; loaded = true }
        do {
            let list = try await apiClient.profilesV3()
            if appState.profiles != list.profiles { appState.profiles = list.profiles }
            appState.anonymousProfile = list.anonymousProfile ?? ""
        } catch {
            if case APIClientError.service(let status, _) = error, status == 503 {
                errorMessage = "Profile evaluation is unavailable"
            } else {
                errorMessage = "Unable to load profiles: \(error.localizedDescription)"
            }
        }
    }
}
