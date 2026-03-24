// SearchView.swift
// MCPProxy
//
// Global tool search across all servers using BM25 search index.
// Debounces input by 300ms to avoid excessive API calls.

import SwiftUI

// MARK: - Search View

struct SearchView: View {
    @ObservedObject var appState: AppState
    @State private var query = ""
    @State private var results: [SearchResult] = []
    @State private var isSearching = false
    @State private var hasSearched = false
    @State private var debounceTask: Task<Void, Never>?

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            searchHeader
            Divider()
            searchResultsArea
        }
    }

    // MARK: - Header

    @ViewBuilder
    private var searchHeader: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Tool Search")
                .font(.title2.bold())

            HStack {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search tools across all servers...", text: $query)
                    .textFieldStyle(.plain)
                    .font(.body)
                    .onSubmit {
                        triggerSearch()
                    }
                    .onChange(of: query) { _ in
                        debounceSearch()
                    }

                if !query.isEmpty {
                    Button {
                        query = ""
                        results = []
                        hasSearched = false
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                }

                if isSearching {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .padding(8)
            .background(Color(nsColor: .controlBackgroundColor))
            .cornerRadius(8)

            if hasSearched {
                Text("\(results.count) result(s)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding()
    }

    // MARK: - Results

    @ViewBuilder
    private var searchResultsArea: some View {
        if !hasSearched {
            VStack(spacing: 12) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 48))
                    .foregroundStyle(.tertiary)
                Text("Search for tools")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text("Type a query to search across all connected servers")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if results.isEmpty && !isSearching {
            VStack(spacing: 12) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 40))
                    .foregroundStyle(.tertiary)
                Text("No tools found")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text("Check that servers are connected and try a different query")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(results) { result in
                        SearchResultRow(result: result)
                    }
                }
                .padding()
            }
        }
    }

    // MARK: - Search Logic

    private func debounceSearch() {
        debounceTask?.cancel()
        debounceTask = Task {
            try? await Task.sleep(nanoseconds: 300_000_000) // 300ms
            if !Task.isCancelled {
                await performSearch()
            }
        }
    }

    private func triggerSearch() {
        debounceTask?.cancel()
        Task { await performSearch() }
    }

    private func performSearch() async {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else {
            results = []
            hasSearched = false
            return
        }
        guard let client = apiClient else { return }

        isSearching = true
        defer { isSearching = false }
        hasSearched = true

        do {
            results = try await client.searchTools(query: trimmed, limit: 20)
        } catch {
            // Keep existing results on error
        }
    }
}

// MARK: - Search Result Row

struct SearchResultRow: View {
    let result: SearchResult

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(result.tool.name)
                    .font(.system(size: 13, weight: .semibold, design: .monospaced))

                if let serverName = result.tool.serverName, !serverName.isEmpty {
                    Text(serverName)
                        .font(.caption2)
                        .foregroundStyle(.white)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.blue)
                        .clipShape(Capsule())
                }

                annotationBadges

                Spacer()

                Text(String(format: "%.1f%%", result.score * 100))
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            if let desc = result.tool.description, !desc.isEmpty {
                Text(desc)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 8)
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(6)
    }

    @ViewBuilder
    private var annotationBadges: some View {
        if let annotations = result.tool.annotations {
            if annotations.readOnlyHint == true {
                badge(text: "read", color: .green)
            }
            if annotations.destructiveHint == true {
                badge(text: "destructive", color: .red)
            } else if annotations.readOnlyHint != true {
                badge(text: "write", color: .orange)
            }
        }
    }

    @ViewBuilder
    private func badge(text: String, color: Color) -> some View {
        Text(text)
            .font(.caption2)
            .foregroundStyle(color)
            .padding(.horizontal, 5)
            .padding(.vertical, 2)
            .background(color.opacity(0.1))
            .cornerRadius(4)
    }
}
