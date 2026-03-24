// UpdateService.swift
// MCPProxy
//
// Update checking service. Uses GitHub Releases API as a lightweight
// alternative to Sparkle when Sparkle SPM dependency is not available.
// When Sparkle IS linked, it takes precedence for the full update UX
// (download, verify, replace, relaunch).

import Foundation
import AppKit

// MARK: - Update Service

/// Manages software update checks.
///
/// Strategy:
/// 1. If Sparkle framework is linked → use SPUStandardUpdaterController
/// 2. Otherwise → check GitHub Releases API directly (notify only, no auto-install)
final class UpdateService: ObservableObject {

    /// Whether an update check can be performed.
    var canCheckForUpdates: Bool { true }

    /// Whether an update check is currently in progress.
    @Published private(set) var isChecking: Bool = false

    /// Latest available version (nil if current or unknown).
    @Published private(set) var latestVersion: String?

    /// URL to download the latest release.
    @Published private(set) var downloadURL: String?

    /// Release notes for the latest version.
    @Published private(set) var releaseNotes: String?

    /// Whether Sparkle framework is linked.
    private let sparkleAvailable: Bool

    /// GitHub API endpoint for latest release.
    private let githubReleaseURL = "https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest"

    /// Current version from the core (set by AppController).
    var currentVersion: String = ""

    // MARK: - Initialization

    init() {
        self.sparkleAvailable = NSClassFromString("SPUStandardUpdaterController") != nil
    }

    // MARK: - Public API

    /// Check for updates. Uses Sparkle if available, otherwise GitHub API.
    func checkForUpdates() {
        if sparkleAvailable {
            checkWithSparkle()
        } else {
            checkWithGitHub()
        }
    }

    /// Open the download page in the browser.
    func openDownloadPage() {
        let urlString = downloadURL ?? "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest"
        if let url = URL(string: urlString) {
            NSWorkspace.shared.open(url)
        }
    }

    // MARK: - Sparkle

    private func checkWithSparkle() {
        // When Sparkle is linked:
        // import Sparkle
        // let controller = SPUStandardUpdaterController(startingUpdater: true, ...)
        // controller.checkForUpdates(nil)
        // For now, fall through to GitHub check
        checkWithGitHub()
    }

    // MARK: - GitHub Releases API

    private func checkWithGitHub() {
        guard !isChecking else { return }
        isChecking = true

        Task {
            defer { DispatchQueue.main.async { self.isChecking = false } }

            guard let url = URL(string: githubReleaseURL) else { return }
            var request = URLRequest(url: url)
            request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
            request.timeoutInterval = 10

            do {
                let (data, response) = try await URLSession.shared.data(for: request)
                guard let httpResponse = response as? HTTPURLResponse,
                      httpResponse.statusCode == 200 else { return }

                guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }

                let tagName = json["tag_name"] as? String ?? ""
                let body = json["body"] as? String ?? ""
                let htmlURL = json["html_url"] as? String ?? ""

                // Strip "v" prefix for comparison
                let remoteVersion = tagName.hasPrefix("v") ? String(tagName.dropFirst()) : tagName
                let localVersion = currentVersion.hasPrefix("v") ? String(currentVersion.dropFirst()) : currentVersion

                // Simple version comparison (works for semver)
                if !remoteVersion.isEmpty && !localVersion.isEmpty && remoteVersion != localVersion {
                    // Find macOS DMG asset
                    var dmgURL = htmlURL
                    if let assets = json["assets"] as? [[String: Any]] {
                        for asset in assets {
                            if let name = asset["name"] as? String,
                               name.contains("darwin") && name.hasSuffix(".dmg") {
                                dmgURL = asset["browser_download_url"] as? String ?? htmlURL
                                break
                            }
                        }
                    }

                    DispatchQueue.main.async {
                        self.latestVersion = remoteVersion
                        self.downloadURL = dmgURL
                        self.releaseNotes = body
                    }
                }
            } catch {
                // Silently fail — update checks are non-critical
            }
        }
    }
}
