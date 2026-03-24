// MCPProxyApp.swift
// MCPProxy
//
// The @main entry point for the MCPProxy macOS tray application.
// Uses AppKit NSStatusItem + NSMenu directly (not SwiftUI MenuBarExtra)
// because MenuBarExtra with .menu style has a known bug where ForEach
// over dynamic arrays appends duplicates to the underlying NSMenu.

import SwiftUI
import Combine

// MARK: - App Delegate

/// Manages the status bar item, menu, core process, and app lifecycle.
final class AppController: NSObject, NSApplicationDelegate {
    let appState = AppState()
    let notificationService = NotificationService()
    let updateService = UpdateService()
    var coreManager: CoreProcessManager?

    private var statusItem: NSStatusItem!
    private var cancellables = Set<AnyCancellable>()

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Create the status bar item
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = statusItem.button {
            button.image = NSImage(systemSymbolName: "server.rack",
                                   accessibilityDescription: "MCPProxy")
        }

        // Build initial menu
        rebuildMenu()

        // Subscribe to state changes — rebuild menu and update icon
        appState.objectWillChange
            .debounce(for: .milliseconds(500), scheduler: RunLoop.main)
            .sink { [weak self] _ in
                self?.rebuildMenu()
                self?.updateStatusIcon()
            }
            .store(in: &cancellables)

        // Start core
        Task {
            await startCore()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let process = coreManager?.managedProcess {
            process.terminate()
        }
    }

    // MARK: - Core Startup

    private func startCore() async {
        await notificationService.setup()
        await MainActor.run {
            appState.autoStartEnabled = AutoStartService.isEnabled
        }

        if SymlinkService.needsSetup() {
            if let bundledBinary = resolveBundledCoreBinary() {
                await SymlinkService.updateSymlinkIfNeeded(bundledBinary: bundledBinary)
            }
        }

        let manager = CoreProcessManager(
            appState: appState,
            notificationService: notificationService
        )
        coreManager = manager
        await manager.start()
    }

    private func resolveBundledCoreBinary() -> String? {
        guard let execPath = Bundle.main.executablePath else { return nil }
        let execURL = URL(fileURLWithPath: execPath)
        let macOSDir = execURL.deletingLastPathComponent()
        let contentsDir = macOSDir.deletingLastPathComponent()
        guard contentsDir.lastPathComponent == "Contents" else { return nil }
        let candidate = contentsDir
            .appendingPathComponent("Resources")
            .appendingPathComponent("bin")
            .appendingPathComponent("mcpproxy")
        if FileManager.default.isExecutableFile(atPath: candidate.path) {
            return candidate.path
        }
        return nil
    }

    // MARK: - Status Icon

    /// Update the status bar icon with a health-colored badge dot.
    private func updateStatusIcon() {
        guard let button = statusItem.button else { return }

        let health = appState.healthLevel
        let baseIcon = NSImage(systemSymbolName: "server.rack", accessibilityDescription: "MCPProxy")
        guard let base = baseIcon else { return }

        // Compose the icon with a badge dot
        let size = NSSize(width: 18, height: 18)
        let composed = NSImage(size: size, flipped: false) { rect in
            // Draw base icon
            base.draw(in: rect)

            // Draw badge dot in bottom-right
            let dotSize: CGFloat = 6
            let dotRect = NSRect(
                x: rect.width - dotSize - 0.5,
                y: 0.5,
                width: dotSize,
                height: dotSize
            )

            let dotColor: NSColor
            switch health {
            case .healthy: dotColor = .systemGreen
            case .degraded: dotColor = .systemYellow
            case .unhealthy: dotColor = .systemRed
            case .disconnected: dotColor = .systemGray
            }

            // White outline for visibility
            NSColor.white.setFill()
            NSBezierPath(ovalIn: dotRect.insetBy(dx: -1, dy: -1)).fill()
            // Colored dot
            dotColor.setFill()
            NSBezierPath(ovalIn: dotRect).fill()

            return true
        }
        composed.isTemplate = false // Has colors, can't be template
        button.image = composed
    }

    // MARK: - Menu Building (AppKit NSMenu — no SwiftUI)

    /// Rebuild the entire NSMenu from current appState.
    /// Called on every debounced state change (max once per 500ms).
    private func rebuildMenu() {
        let menu = NSMenu()

        // Header
        let title = appState.version.isEmpty ? "MCPProxy" : "MCPProxy v\(appState.version)"
        let titleItem = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        titleItem.isEnabled = false
        let font = NSFont.boldSystemFont(ofSize: 13)
        titleItem.attributedTitle = NSAttributedString(string: title, attributes: [.font: font])
        menu.addItem(titleItem)

        let summary = NSMenuItem(title: appState.statusSummary, action: nil, keyEquivalent: "")
        summary.isEnabled = false
        menu.addItem(summary)

        // Error state
        if case .error(let coreError) = appState.coreState {
            let errorItem = NSMenuItem(title: "Error: \(coreError.userMessage)", action: nil, keyEquivalent: "")
            errorItem.isEnabled = false
            menu.addItem(errorItem)

            let hintItem = NSMenuItem(title: coreError.remediationHint, action: nil, keyEquivalent: "")
            hintItem.isEnabled = false
            menu.addItem(hintItem)

            if coreError.isRetryable {
                let retryItem = NSMenuItem(title: "Retry", action: #selector(retryCore), keyEquivalent: "")
                retryItem.target = self
                menu.addItem(retryItem)
            }
        }

        menu.addItem(.separator())

        // Needs Attention
        let attentionServers = appState.serversNeedingAttention
        if !attentionServers.isEmpty {
            let header = NSMenuItem(title: "Needs Attention (\(attentionServers.count))", action: nil, keyEquivalent: "")
            header.isEnabled = false
            menu.addItem(header)

            for server in attentionServers {
                let action = server.health?.action ?? ""
                let actionLabel = actionDisplayName(for: action)
                let summary = server.health?.summary ?? ""
                let icon = actionIcon(for: action)

                // Show "servername — Action Needed" format
                let title = summary.isEmpty ? "\(server.name) — \(actionLabel)" : "\(server.name) — \(summary)"
                let item = NSMenuItem(title: title, action: #selector(handleAttentionAction(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = server
                item.image = NSImage(systemSymbolName: icon, accessibilityDescription: action)
                menu.addItem(item)
            }
            menu.addItem(.separator())
        }

        // Quarantine
        if appState.quarantinedToolsCount > 0 {
            let quarantineItem = NSMenuItem(
                title: "\(appState.quarantinedToolsCount) quarantined server(s)",
                action: nil, keyEquivalent: "")
            quarantineItem.isEnabled = false
            quarantineItem.image = NSImage(systemSymbolName: "shield.lefthalf.filled",
                                            accessibilityDescription: "quarantine")
            menu.addItem(quarantineItem)
            menu.addItem(.separator())
        }

        // Servers
        if !appState.servers.isEmpty {
            let serversHeader = NSMenuItem(title: "Servers", action: nil, keyEquivalent: "")
            serversHeader.isEnabled = false
            menu.addItem(serversHeader)

            for server in appState.servers {
                let item = NSMenuItem(title: server.name, action: nil, keyEquivalent: "")

                // Status dot
                let dotColor = serverStatusColor(for: server)
                let dot = NSImage(size: NSSize(width: 10, height: 10), flipped: false) { rect in
                    dotColor.setFill()
                    NSBezierPath(ovalIn: rect.insetBy(dx: 1, dy: 1)).fill()
                    return true
                }
                item.image = dot

                // Submenu with actions
                let sub = NSMenu()
                let statusText = server.health?.summary ?? (server.connected ? "Connected" : "Disconnected")
                let statusItem = NSMenuItem(title: statusText, action: nil, keyEquivalent: "")
                statusItem.isEnabled = false
                sub.addItem(statusItem)
                sub.addItem(.separator())

                if server.enabled {
                    let disable = NSMenuItem(title: "Disable", action: #selector(disableServer(_:)), keyEquivalent: "")
                    disable.target = self
                    disable.representedObject = server.id
                    sub.addItem(disable)
                } else {
                    let enable = NSMenuItem(title: "Enable", action: #selector(enableServer(_:)), keyEquivalent: "")
                    enable.target = self
                    enable.representedObject = server.id
                    sub.addItem(enable)
                }

                let restart = NSMenuItem(title: "Restart", action: #selector(restartServer(_:)), keyEquivalent: "")
                restart.target = self
                restart.representedObject = server.id
                sub.addItem(restart)

                if server.health?.action == "login" {
                    let login = NSMenuItem(title: "Log In", action: #selector(loginServer(_:)), keyEquivalent: "")
                    login.target = self
                    login.representedObject = server.id
                    sub.addItem(login)
                }

                let logs = NSMenuItem(title: "View Logs", action: #selector(viewServerLogs(_:)), keyEquivalent: "")
                logs.target = self
                logs.representedObject = server.name
                sub.addItem(logs)

                item.submenu = sub
                menu.addItem(item)
            }
            menu.addItem(.separator())
        }

        // Recent Activity (deduplicated — show unique entries only)
        if !appState.recentActivity.isEmpty {
            let activityHeader = NSMenuItem(title: "Recent Activity", action: nil, keyEquivalent: "")
            activityHeader.isEnabled = false
            menu.addItem(activityHeader)

            // Deduplicate by (serverName + toolName + type) — keep first occurrence
            var seen = Set<String>()
            var uniqueEntries: [ActivityEntry] = []
            for entry in appState.recentActivity {
                let key = "\(entry.serverName ?? ""):\(entry.toolName ?? ""):\(entry.type)"
                if !seen.contains(key) {
                    seen.insert(key)
                    uniqueEntries.append(entry)
                }
            }

            for entry in uniqueEntries.prefix(5) {
                var text = ""
                if let sn = entry.serverName, !sn.isEmpty {
                    text = sn
                    if let tn = entry.toolName, !tn.isEmpty { text += ":\(tn)" }
                } else {
                    text = entry.type
                }

                // Add relative timestamp
                let timeAgo = relativeTimeString(from: entry.timestamp)
                if !timeAgo.isEmpty { text += " · \(timeAgo)" }

                let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
                item.isEnabled = false
                let iconName: String
                switch entry.status {
                case "error": iconName = "xmark.circle"
                case "blocked": iconName = "hand.raised"
                default: iconName = "checkmark.circle"
                }
                item.image = NSImage(systemSymbolName: iconName, accessibilityDescription: entry.status)
                menu.addItem(item)
            }
            menu.addItem(.separator())
        }

        // Actions
        let webUI = NSMenuItem(title: "Open Web UI", action: #selector(openWebUI), keyEquivalent: "")
        webUI.target = self
        menu.addItem(webUI)

        let configFile = NSMenuItem(title: "Open Config File", action: #selector(openConfigFile), keyEquivalent: "")
        configFile.target = self
        menu.addItem(configFile)

        let logsDir = NSMenuItem(title: "Open Logs Directory", action: #selector(openLogsDirectory), keyEquivalent: "")
        logsDir.target = self
        menu.addItem(logsDir)

        menu.addItem(.separator())

        // Settings
        let autoStart = NSMenuItem(title: "Run at Startup", action: #selector(toggleAutoStart(_:)), keyEquivalent: "")
        autoStart.target = self
        autoStart.state = appState.autoStartEnabled ? .on : .off
        menu.addItem(autoStart)

        let checkUpdates = NSMenuItem(title: "Check for Updates", action: #selector(checkForUpdates), keyEquivalent: "")
        checkUpdates.target = self
        checkUpdates.isEnabled = updateService.canCheckForUpdates
        menu.addItem(checkUpdates)

        // Show update from either appState (from core /api/v1/info) or UpdateService (GitHub check)
        let updateVersion = appState.updateAvailable ?? updateService.latestVersion
        if let available = updateVersion {
            let updateNote = NSMenuItem(title: "Update available: v\(available)", action: #selector(openDownloadPage), keyEquivalent: "")
            updateNote.target = self
            menu.addItem(updateNote)
        }

        menu.addItem(.separator())

        // Quit
        let quit = NSMenuItem(title: "Quit MCPProxy", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        statusItem.menu = menu
    }

    // MARK: - Menu Actions

    @objc private func retryCore() {
        Task { await coreManager?.retry() }
    }

    @objc private func handleAttentionAction(_ sender: NSMenuItem) {
        guard let server = sender.representedObject as? ServerStatus else { return }
        guard let action = server.health?.action else { return }
        Task {
            switch action {
            case "login": try? await coreManager?.apiClientForActions?.loginServer(server.id)
            case "restart": try? await coreManager?.apiClientForActions?.restartServer(server.id)
            case "enable": try? await coreManager?.apiClientForActions?.enableServer(server.id)
            default: openWebUI()
            }
        }
    }

    @objc private func enableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await coreManager?.apiClientForActions?.enableServer(id) }
    }

    @objc private func disableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await coreManager?.apiClientForActions?.disableServer(id) }
    }

    @objc private func restartServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await coreManager?.apiClientForActions?.restartServer(id) }
    }

    @objc private func loginServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await coreManager?.apiClientForActions?.loginServer(id) }
    }

    @objc private func viewServerLogs(_ sender: NSMenuItem) {
        guard let name = sender.representedObject as? String else { return }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(name).log")
        if FileManager.default.fileExists(atPath: logFile.path) {
            NSWorkspace.shared.open(logFile)
        } else {
            openLogsDirectory()
        }
    }

    @objc private func openWebUI() {
        if let url = URL(string: "http://127.0.0.1:8080/ui/") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func openConfigFile() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        NSWorkspace.shared.open(home.appendingPathComponent(".mcpproxy/mcp_config.json"))
    }

    @objc private func openLogsDirectory() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        NSWorkspace.shared.open(home.appendingPathComponent("Library/Logs/mcpproxy"))
    }

    @objc private func toggleAutoStart(_ sender: NSMenuItem) {
        do {
            if appState.autoStartEnabled {
                try AutoStartService.disable()
                appState.autoStartEnabled = false
            } else {
                try AutoStartService.enable()
                appState.autoStartEnabled = true
            }
        } catch {}
        rebuildMenu()
    }

    @objc private func checkForUpdates() {
        updateService.currentVersion = appState.version
        updateService.checkForUpdates()
    }

    @objc private func openDownloadPage() {
        updateService.openDownloadPage()
    }

    @objc private func quitApp() {
        Task {
            await coreManager?.shutdown()
            try? await Task.sleep(nanoseconds: 200_000_000)
            NSApplication.shared.terminate(nil)
        }
    }

    // MARK: - Helpers

    private func serverStatusColor(for server: ServerStatus) -> NSColor {
        if server.quarantined { return .systemOrange }
        if let health = server.health {
            switch health.level {
            case "healthy": return .systemGreen
            case "degraded": return .systemYellow
            case "unhealthy": return .systemRed
            default: return server.connected ? .systemGreen : .systemGray
            }
        }
        return server.connected ? .systemGreen : .systemGray
    }

    private func actionIcon(for action: String) -> String {
        switch action {
        case "login": return "person.badge.key"
        case "restart": return "arrow.clockwise"
        case "enable": return "power"
        case "approve": return "checkmark.shield"
        default: return "exclamationmark.circle"
        }
    }

    private func actionDisplayName(for action: String) -> String {
        switch action {
        case "login": return "Login Required"
        case "restart": return "Restart Needed"
        case "enable": return "Disabled"
        case "approve": return "Approval Needed"
        case "set_secret": return "Secret Missing"
        case "configure": return "Configuration Needed"
        case "view_logs": return "Check Logs"
        default: return "Action Needed"
        }
    }

    private func relativeTimeString(from timestamp: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = formatter.date(from: timestamp)
        if date == nil {
            formatter.formatOptions = [.withInternetDateTime]
            date = formatter.date(from: timestamp)
        }
        guard let d = date else { return "" }

        let elapsed = -d.timeIntervalSinceNow
        if elapsed < 60 { return "just now" }
        if elapsed < 3600 { return "\(Int(elapsed / 60))m ago" }
        if elapsed < 86400 { return "\(Int(elapsed / 3600))h ago" }
        return "\(Int(elapsed / 86400))d ago"
    }
}

// MARK: - App

@main
struct MCPProxyApp: App {
    @NSApplicationDelegateAdaptor(AppController.self) var controller

    var body: some Scene {
        // No SwiftUI scenes — the tray menu is pure AppKit (NSStatusItem + NSMenu).
        // This avoids the MenuBarExtra .menu style bug where ForEach duplicates items.
        // A Settings scene can be added here for Spec B (main window).
        Settings {
            EmptyView()
        }
    }
}
