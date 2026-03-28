import AppKit
import ApplicationServices
import CoreGraphics

// MARK: - Configuration

let defaultBundleID = "com.smartmcpproxy.mcpproxy"
var configuredBundleID: String = defaultBundleID

// MARK: - Logging (stderr only — stdout is reserved for MCP JSON-RPC)

func log(_ message: String) {
    FileHandle.standardError.write(Data("[mcpproxy-ui-test] \(message)\n".utf8))
}

// MARK: - JSON Helpers

func jsonString(_ obj: Any) -> String {
    guard let data = try? JSONSerialization.data(withJSONObject: obj, options: []),
          let str = String(data: data, encoding: .utf8) else {
        return "{}"
    }
    return str
}

func parseJSON(_ str: String) -> [String: Any]? {
    guard let data = str.data(using: .utf8),
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
        return nil
    }
    return obj
}

// MARK: - MCP Response Builders

func makeResponse(id: Any, result: Any) -> [String: Any] {
    return [
        "jsonrpc": "2.0",
        "id": id,
        "result": result
    ]
}

func makeError(id: Any, code: Int, message: String, data: Any? = nil) -> [String: Any] {
    var err: [String: Any] = [
        "code": code,
        "message": message
    ]
    if let d = data {
        err["data"] = d
    }
    return [
        "jsonrpc": "2.0",
        "id": id,
        "error": err
    ]
}

func makeToolResult(id: Any, text: String, isError: Bool = false) -> [String: Any] {
    var result: [String: Any] = [
        "content": [
            ["type": "text", "text": text]
        ]
    ]
    if isError {
        result["isError"] = true
    }
    return makeResponse(id: id, result: result)
}

// MARK: - Send response to stdout

func sendResponse(_ response: [String: Any]) {
    let str = jsonString(response)
    print(str)
    fflush(stdout)
}

// MARK: - Tool Definitions

func toolDefinitions() -> [[String: Any]] {
    return [
        [
            "name": "check_accessibility",
            "description": "Check whether macOS Accessibility API access is granted to this process. Returns permission status and instructions if not granted.",
            "inputSchema": [
                "type": "object",
                "properties": [:] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "list_running_apps",
            "description": "List running macOS applications with their name, bundle ID, and PID. Includes apps with regular and accessory activation policies.",
            "inputSchema": [
                "type": "object",
                "properties": [:] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "list_menu_items",
            "description": "List menu items from an app's status bar (extras menu bar) dropdown. Opens the status bar menu, reads items, and closes it. Returns a JSON tree of menu items with title, enabled, checked, and children.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID."
                    ] as [String: Any],
                    "path": [
                        "type": "array",
                        "items": ["type": "string"],
                        "description": "Optional path to navigate to a submenu before listing. Array of menu item titles."
                    ] as [String: Any]
                ] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "click_menu_item",
            "description": "Click a menu item in the app's status bar dropdown. Navigate through submenus using the path array. For example, path: [\"Servers\", \"tavily\", \"Disable\"] navigates Servers > tavily > Disable.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID."
                    ] as [String: Any],
                    "path": [
                        "type": "array",
                        "items": ["type": "string"],
                        "description": "Path of menu item titles to navigate and click. The last element is the item to click."
                    ] as [String: Any]
                ] as [String: Any],
                "required": ["path"]
            ]
        ],
        [
            "name": "read_status_bar",
            "description": "Read the status bar item info for an app, including title text, tooltip, and description.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID."
                    ] as [String: Any]
                ] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "screenshot_window",
            "description": "Take a screenshot of an app window or the full screen. Returns the image as a base64-encoded PNG. Use for visual verification of UI state after changes.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID. Use 'screen' for full screen capture."
                    ] as [String: Any],
                    "output_path": [
                        "type": "string",
                        "description": "Optional file path to save the PNG. If omitted, returns base64 in the response."
                    ] as [String: Any],
                    "window_title": [
                        "type": "string",
                        "description": "Optional: capture only the window with this title substring. If omitted, captures the frontmost window of the app."
                    ] as [String: Any]
                ] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "screenshot_status_bar_menu",
            "description": "Open the app's status bar menu and take a screenshot of it, then close the menu. Useful for verifying tray menu appearance.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID."
                    ] as [String: Any],
                    "output_path": [
                        "type": "string",
                        "description": "Optional file path to save the PNG. If omitted, returns base64 in the response."
                    ] as [String: Any]
                ] as [String: Any],
                "required": [] as [String]
            ]
        ],
        [
            "name": "send_keypress",
            "description": "Send a keyboard shortcut to a running application using CGEvent. The app must be frontmost (will be activated automatically). Supports modifier+key combos like 'cmd+=', 'cmd+-', 'cmd+0', 'cmd+shift+=', 'cmd+c', etc. Use for testing keyboard shortcuts like Cmd+/Cmd- zoom.",
            "inputSchema": [
                "type": "object",
                "properties": [
                    "key": [
                        "type": "string",
                        "description": "Key combo string, e.g. 'cmd+=', 'cmd+-', 'cmd+0', 'cmd+shift+=', 'cmd+c'. Modifier names: cmd, shift, ctrl, opt/alt. Separated by '+'. The last component is the key."
                    ] as [String: Any],
                    "bundle_id": [
                        "type": "string",
                        "description": "Bundle ID of the target app. Defaults to configured bundle ID. The app will be activated (brought to front) before sending the key."
                    ] as [String: Any],
                    "repeat": [
                        "type": "integer",
                        "description": "Number of times to send the keypress. Defaults to 1. Useful for repeated zoom in/out."
                    ] as [String: Any]
                ] as [String: Any],
                "required": ["key"]
            ]
        ]
    ]
}

// MARK: - Accessibility Helpers

func checkAccessibilityPermission() -> (trusted: Bool, message: String) {
    let trusted = AXIsProcessTrusted()
    if trusted {
        return (true, "Accessibility access is GRANTED. All UI automation tools are available.")
    } else {
        let msg = """
        Accessibility access is NOT GRANTED.

        To grant permission:
        1. Open System Settings > Privacy & Security > Accessibility
        2. Click the '+' button
        3. Add the terminal application you're running this from (e.g., Terminal.app, iTerm2, or your IDE)
        4. If using 'claude' CLI, add the Claude Code binary
        5. You may need to restart the terminal after granting permission

        Alternatively, run: tccutil reset Accessibility
        Then re-run and approve the prompt.
        """
        return (false, msg)
    }
}

func findRunningApp(bundleID: String) -> NSRunningApplication? {
    let apps = NSWorkspace.shared.runningApplications
    return apps.first { $0.bundleIdentifier == bundleID }
}

func getExtrasMenuBar(for pid: pid_t) -> AXUIElement? {
    let appElement = AXUIElementCreateApplication(pid)
    var extrasMenuBar: AnyObject?
    let result = AXUIElementCopyAttributeValue(appElement, "AXExtrasMenuBar" as CFString, &extrasMenuBar)
    if result == AXError.success, let menuBar = extrasMenuBar {
        return (menuBar as! AXUIElement)
    }
    log("AXExtrasMenuBar not available (error: \(result.rawValue)), trying system-wide approach")
    return findStatusItemSystemWide(for: pid)
}

func findStatusItemSystemWide(for targetPID: pid_t) -> AXUIElement? {
    // The system menu bar extras are owned by the SystemUIServer process.
    // We can also try accessing via the system-wide AX element.
    let apps = NSWorkspace.shared.runningApplications

    // First try SystemUIServer
    if let systemUIServer = apps.first(where: { $0.bundleIdentifier == "com.apple.systemuiserver" }) {
        let sysApp = AXUIElementCreateApplication(systemUIServer.processIdentifier)
        var menuBar: AnyObject?
        let result = AXUIElementCopyAttributeValue(sysApp, "AXExtrasMenuBar" as CFString, &menuBar)
        if result == AXError.success, let mb = menuBar {
            return (mb as! AXUIElement)
        }
        log("SystemUIServer AXExtrasMenuBar failed (error: \(result.rawValue))")
    } else {
        log("SystemUIServer not found")
    }

    // Fallback: try ControlCenter (macOS 13+)
    if let controlCenter = apps.first(where: { $0.bundleIdentifier == "com.apple.controlcenter" }) {
        let ccApp = AXUIElementCreateApplication(controlCenter.processIdentifier)
        var menuBar: AnyObject?
        let result = AXUIElementCopyAttributeValue(ccApp, "AXExtrasMenuBar" as CFString, &menuBar)
        if result == AXError.success, let mb = menuBar {
            return (mb as! AXUIElement)
        }
        log("ControlCenter AXExtrasMenuBar also failed (error: \(result.rawValue))")
    }

    return nil
}

func getStatusBarChildren(menuBar: AXUIElement) -> [AXUIElement] {
    var children: AnyObject?
    let result = AXUIElementCopyAttributeValue(menuBar, kAXChildrenAttribute as CFString, &children)
    if result == AXError.success, let items = children as? [AXUIElement] {
        return items
    }
    return []
}

func findStatusItem(in menuBar: AXUIElement, for pid: pid_t, bundleID: String) -> AXUIElement? {
    let children = getStatusBarChildren(menuBar: menuBar)
    log("Found \(children.count) status bar children")

    for child in children {
        // AXUIElement doesn't directly expose PID, but we can use AXUIElementGetPid
        var childPID: pid_t = 0
        let pidResult = AXUIElementGetPid(child, &childPID)
        if pidResult == AXError.success && childPID == pid {
            return child
        }
    }

    // Fallback: match by title containing app name
    for child in children {
        var title: AnyObject?
        AXUIElementCopyAttributeValue(child, kAXTitleAttribute as CFString, &title)
        if let titleStr = title as? String {
            log("Status item title: '\(titleStr)' (pid check)")
            let appName = bundleID.components(separatedBy: ".").last ?? bundleID
            if titleStr.lowercased().contains(appName.lowercased()) ||
               titleStr.lowercased().contains("mcpproxy") {
                return child
            }
        }

        var desc: AnyObject?
        AXUIElementCopyAttributeValue(child, kAXDescriptionAttribute as CFString, &desc)
        if let descStr = desc as? String {
            log("Status item description: '\(descStr)'")
            if descStr.lowercased().contains("mcpproxy") {
                return child
            }
        }
    }

    return nil
}

func readMenuItems(from element: AXUIElement, depth: Int = 0) -> [[String: Any]] {
    if depth > 10 { return [] } // safety limit

    var children: AnyObject?
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children)
    guard let items = children as? [AXUIElement] else { return [] }

    var results: [[String: Any]] = []

    for item in items {
        var title: AnyObject?
        AXUIElementCopyAttributeValue(item, kAXTitleAttribute as CFString, &title)

        var role: AnyObject?
        AXUIElementCopyAttributeValue(item, kAXRoleAttribute as CFString, &role)
        let roleStr = role as? String ?? ""

        // Separator items have no title
        if let titleStr = title as? String {
            if titleStr.isEmpty {
                // Check if it's a separator
                if roleStr == "AXMenuItemSeparator" || roleStr == "" {
                    results.append(["title": "---", "separator": true])
                }
                continue
            }

            var enabled: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXEnabledAttribute as CFString, &enabled)

            var markChar: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXMenuItemMarkCharAttribute as CFString, &markChar)

            var cmdChar: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXMenuItemCmdCharAttribute as CFString, &cmdChar)

            var cmdModifiers: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXMenuItemCmdModifiersAttribute as CFString, &cmdModifiers)

            var entry: [String: Any] = [
                "title": titleStr,
                "enabled": (enabled as? Bool) ?? true
            ]

            if let mark = markChar as? String, !mark.isEmpty {
                entry["checked"] = true
                entry["mark"] = mark
            }

            // Build shortcut string
            if let cmd = cmdChar as? String, !cmd.isEmpty {
                var shortcut = ""
                if let mods = cmdModifiers as? Int {
                    // Bit flags: 0 = Cmd, 1 = Shift+Cmd, etc.
                    // kAXMenuItemCmdModifiersAttribute: 0=none extra (just Cmd), shift=2, option=4, control=8
                    // Actually these are standard Carbon modifier flags shifted
                    if mods & (1 << 2) != 0 { shortcut += "Ctrl+" }
                    if mods & (1 << 0) != 0 { shortcut += "Shift+" }
                    if mods & (1 << 1) != 0 { shortcut += "Opt+" }
                    // Cmd is implied unless kAXMenuItemCmdModifierNone
                    shortcut += "Cmd+"
                } else {
                    shortcut = "Cmd+"
                }
                shortcut += cmd
                entry["shortcut"] = shortcut
            }

            // Check for submenu children
            var submenuChildren: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXChildrenAttribute as CFString, &submenuChildren)
            if let subItems = submenuChildren as? [AXUIElement], !subItems.isEmpty {
                entry["hasSubmenu"] = true
                // Recursively read the submenu
                // For submenus, the children of the menu item is a single AXMenu element,
                // and the actual items are children of that AXMenu
                for sub in subItems {
                    var subRole: AnyObject?
                    AXUIElementCopyAttributeValue(sub, kAXRoleAttribute as CFString, &subRole)
                    if (subRole as? String) == "AXMenu" {
                        let subMenuItems = readMenuItems(from: sub, depth: depth + 1)
                        if !subMenuItems.isEmpty {
                            entry["children"] = subMenuItems
                        }
                    }
                }
            }

            results.append(entry)
        } else {
            // No title — possibly a separator
            if roleStr.contains("Separator") {
                results.append(["title": "---", "separator": true])
            }
        }
    }

    return results
}

func navigateToSubmenu(from element: AXUIElement, path: [String], currentIndex: Int) -> (element: AXUIElement, items: [[String: Any]])? {
    if currentIndex >= path.count {
        return (element, readMenuItems(from: element))
    }

    let target = path[currentIndex]
    var children: AnyObject?
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children)
    guard let items = children as? [AXUIElement] else {
        return nil
    }

    for item in items {
        var title: AnyObject?
        AXUIElementCopyAttributeValue(item, kAXTitleAttribute as CFString, &title)
        // Match exact title OR title prefix (for "Servers (24)" matching "Servers")
        guard let titleStr = title as? String,
              (titleStr == target || titleStr.hasPrefix(target)) else { continue }

        // Check for submenu
        var submenuChildren: AnyObject?
        AXUIElementCopyAttributeValue(item, kAXChildrenAttribute as CFString, &submenuChildren)
        if let subItems = submenuChildren as? [AXUIElement] {
            for sub in subItems {
                var subRole: AnyObject?
                AXUIElementCopyAttributeValue(sub, kAXRoleAttribute as CFString, &subRole)
                if (subRole as? String) == "AXMenu" {
                    // Always hover/press the item to populate submenu children
                    // macOS populates AXMenu children lazily on hover
                    AXUIElementPerformAction(item, kAXPressAction as CFString)
                    Thread.sleep(forTimeInterval: 0.5)

                    if currentIndex + 1 < path.count {
                        return navigateToSubmenu(from: sub, path: path, currentIndex: currentIndex + 1)
                    } else {
                        return (sub, readMenuItems(from: sub))
                    }
                }
            }
        }

        // No submenu but matches — return current
        if currentIndex + 1 >= path.count {
            return (item, [])
        }
    }

    return nil
}

func openStatusBarMenu(bundleID: String) -> (statusItem: AXUIElement, menuElement: AXUIElement?, error: String?)? {
    guard let app = findRunningApp(bundleID: bundleID) else {
        return nil
    }
    let pid = app.processIdentifier

    guard let menuBar = getExtrasMenuBar(for: pid) else {
        // Try using the app element directly
        let appElement = AXUIElementCreateApplication(pid)
        // Some apps expose their status item differently
        // Try to find it among all extras
        return (appElement, nil, "Could not find the extras menu bar for \(bundleID). The app may not have a status bar item, or Accessibility permissions may be missing.")
    }

    guard let statusItem = findStatusItem(in: menuBar, for: pid, bundleID: bundleID) else {
        let children = getStatusBarChildren(menuBar: menuBar)
        var descriptions: [String] = []
        for child in children {
            var title: AnyObject?
            AXUIElementCopyAttributeValue(child, kAXTitleAttribute as CFString, &title)
            var desc: AnyObject?
            AXUIElementCopyAttributeValue(child, kAXDescriptionAttribute as CFString, &desc)
            var childPID: pid_t = 0
            AXUIElementGetPid(child, &childPID)
            descriptions.append("title='\(title as? String ?? "nil")' desc='\(desc as? String ?? "nil")' pid=\(childPID)")
        }
        return (menuBar, nil, "Could not find status bar item for \(bundleID). Available items: \(descriptions.joined(separator: "; "))")
    }

    // Open the status item menu by simulating a mouse click at its position.
    // AXUIElementPerformAction(kAXPressAction) doesn't work reliably for
    // NSStatusItem menus — it returns -25204 or -25206 depending on the
    // app's frontmost state. CGEvent-based click is the reliable approach.
    var positionValue: AnyObject?
    var sizeValue: AnyObject?
    AXUIElementCopyAttributeValue(statusItem, kAXPositionAttribute as CFString, &positionValue)
    AXUIElementCopyAttributeValue(statusItem, kAXSizeAttribute as CFString, &sizeValue)

    var position = CGPoint.zero
    var size = CGSize.zero
    if let pv = positionValue {
        AXValueGetValue(pv as! AXValue, .cgPoint, &position)
    }
    if let sv = sizeValue {
        AXValueGetValue(sv as! AXValue, .cgSize, &size)
    }

    if position == .zero && size == .zero {
        return (statusItem, nil, "Cannot determine status bar item position for click simulation")
    }

    // Click at the center of the status item
    let clickPoint = CGPoint(x: position.x + size.width / 2, y: position.y + size.height / 2)
    log("Clicking status bar item at (\(clickPoint.x), \(clickPoint.y))")

    if let mouseDown = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: clickPoint, mouseButton: .left) {
        mouseDown.post(tap: .cghidEventTap)
    }
    Thread.sleep(forTimeInterval: 0.05)
    if let mouseUp = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: clickPoint, mouseButton: .left) {
        mouseUp.post(tap: .cghidEventTap)
    }

    // Wait for menu to render
    Thread.sleep(forTimeInterval: 0.3)

    // The opened menu should now be a child of the status item
    var menuChildren: AnyObject?
    AXUIElementCopyAttributeValue(statusItem, kAXChildrenAttribute as CFString, &menuChildren)
    if let menus = menuChildren as? [AXUIElement], let menu = menus.first {
        return (statusItem, menu, nil)
    }

    // Sometimes the menu is a sibling or accessible via different path
    // Try reading children of the menu bar after pressing
    var menuBarChildren: AnyObject?
    AXUIElementCopyAttributeValue(menuBar, kAXChildrenAttribute as CFString, &menuBarChildren)
    if let allChildren = menuBarChildren as? [AXUIElement] {
        for child in allChildren {
            var childChildren: AnyObject?
            AXUIElementCopyAttributeValue(child, kAXChildrenAttribute as CFString, &childChildren)
            if let cc = childChildren as? [AXUIElement], !cc.isEmpty {
                for c in cc {
                    var role: AnyObject?
                    AXUIElementCopyAttributeValue(c, kAXRoleAttribute as CFString, &role)
                    if (role as? String) == "AXMenu" {
                        return (statusItem, c, nil)
                    }
                }
            }
        }
    }

    return (statusItem, nil, "Menu opened but could not read menu items. The menu may have appeared as a window instead.")
}

func closeMenu(statusItem: AXUIElement) {
    // Send Escape key to close the menu (most reliable method)
    if let escDown = CGEvent(keyboardEventSource: nil, virtualKey: 53, keyDown: true) { // 53 = Escape
        escDown.post(tap: .cghidEventTap)
    }
    Thread.sleep(forTimeInterval: 0.05)
    if let escUp = CGEvent(keyboardEventSource: nil, virtualKey: 53, keyDown: false) {
        escUp.post(tap: .cghidEventTap)
    }
    Thread.sleep(forTimeInterval: 0.15)
}

func collectAvailableTitles(from element: AXUIElement) -> [String] {
    var children: AnyObject?
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children)
    guard let items = children as? [AXUIElement] else { return [] }

    var titles: [String] = []
    for item in items {
        var title: AnyObject?
        AXUIElementCopyAttributeValue(item, kAXTitleAttribute as CFString, &title)
        if let t = title as? String, !t.isEmpty {
            titles.append(t)
        }
    }
    return titles
}

// MARK: - Tool Implementations

func handleCheckAccessibility(id: Any) -> [String: Any] {
    let (trusted, message) = checkAccessibilityPermission()
    let result: [String: Any] = [
        "trusted": trusted,
        "message": message
    ]
    return makeToolResult(id: id, text: jsonString(result))
}

func handleListRunningApps(id: Any) -> [String: Any] {
    let apps = NSWorkspace.shared.runningApplications
    var appList: [[String: Any]] = []

    for app in apps {
        let policy = app.activationPolicy
        guard policy == .regular || policy == .accessory else { continue }

        var entry: [String: Any] = [
            "pid": Int(app.processIdentifier),
            "name": app.localizedName ?? "Unknown",
            "bundle_id": app.bundleIdentifier ?? "unknown",
            "active": app.isActive
        ]

        if policy == .regular {
            entry["type"] = "regular"
        } else {
            entry["type"] = "accessory"
        }

        appList.append(entry)
    }

    // Sort by name
    appList.sort { ($0["name"] as? String ?? "") < ($1["name"] as? String ?? "") }

    let result: [String: Any] = [
        "count": appList.count,
        "applications": appList
    ]
    return makeToolResult(id: id, text: jsonString(result))
}

func handleListMenuItems(id: Any, arguments: [String: Any]) -> [String: Any] {
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID
    let path = arguments["path"] as? [String] ?? []

    guard AXIsProcessTrusted() else {
        return makeToolResult(id: id, text: "Accessibility permission is not granted. Run 'check_accessibility' tool for instructions.", isError: true)
    }

    guard let app = findRunningApp(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    log("Opening status bar menu for \(bundleID) (pid: \(app.processIdentifier))")

    guard let result = openStatusBarMenu(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    if let error = result.error, result.menuElement == nil {
        return makeToolResult(id: id, text: error, isError: true)
    }

    guard let menu = result.menuElement else {
        closeMenu(statusItem: result.statusItem)
        return makeToolResult(id: id, text: "Could not access menu element.", isError: true)
    }

    var menuItems: [[String: Any]]

    if path.isEmpty {
        menuItems = readMenuItems(from: menu)
    } else {
        if let navResult = navigateToSubmenu(from: menu, path: path, currentIndex: 0) {
            menuItems = navResult.items
            if menuItems.isEmpty {
                menuItems = readMenuItems(from: navResult.element)
            }
        } else {
            let available = collectAvailableTitles(from: menu)
            closeMenu(statusItem: result.statusItem)
            return makeToolResult(id: id, text: "Could not navigate to path: \(path). Available items at root: \(available)", isError: true)
        }
    }

    closeMenu(statusItem: result.statusItem)

    let response: [String: Any] = [
        "bundle_id": bundleID,
        "path": path,
        "items": menuItems
    ]
    return makeToolResult(id: id, text: jsonString(response))
}

func handleClickMenuItem(id: Any, arguments: [String: Any]) -> [String: Any] {
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID
    guard let path = arguments["path"] as? [String], !path.isEmpty else {
        return makeToolResult(id: id, text: "The 'path' argument is required and must be a non-empty array of strings.", isError: true)
    }

    guard AXIsProcessTrusted() else {
        return makeToolResult(id: id, text: "Accessibility permission is not granted. Run 'check_accessibility' tool for instructions.", isError: true)
    }

    guard findRunningApp(bundleID: bundleID) != nil else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    guard let result = openStatusBarMenu(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    if let error = result.error, result.menuElement == nil {
        return makeToolResult(id: id, text: error, isError: true)
    }

    guard let menu = result.menuElement else {
        closeMenu(statusItem: result.statusItem)
        return makeToolResult(id: id, text: "Could not access menu element.", isError: true)
    }

    // Navigate the path
    var currentElement: AXUIElement = menu
    for (index, segment) in path.enumerated() {
        let isLast = index == path.count - 1

        var children: AnyObject?
        AXUIElementCopyAttributeValue(currentElement, kAXChildrenAttribute as CFString, &children)
        guard let items = children as? [AXUIElement] else {
            closeMenu(statusItem: result.statusItem)
            return makeToolResult(id: id, text: "No menu items found at path segment '\(segment)' (index \(index)).", isError: true)
        }

        var found = false
        for item in items {
            var title: AnyObject?
            AXUIElementCopyAttributeValue(item, kAXTitleAttribute as CFString, &title)
            guard let titleStr = title as? String, titleStr == segment else { continue }

            if isLast {
                // Check if the item is enabled
                var enabled: AnyObject?
                AXUIElementCopyAttributeValue(item, kAXEnabledAttribute as CFString, &enabled)
                if let isEnabled = enabled as? Bool, !isEnabled {
                    closeMenu(statusItem: result.statusItem)
                    return makeToolResult(id: id, text: "Menu item '\(segment)' is disabled and cannot be clicked.", isError: true)
                }

                // Click the final item
                let pressResult = AXUIElementPerformAction(item, kAXPressAction as CFString)
                if pressResult == AXError.success {
                    // No need to close menu — the click action closes it
                    Thread.sleep(forTimeInterval: 0.1)
                    return makeToolResult(id: id, text: jsonString([
                        "success": true,
                        "clicked": path,
                        "message": "Successfully clicked menu item: \(path.joined(separator: " > "))"
                    ]))
                } else {
                    closeMenu(statusItem: result.statusItem)
                    return makeToolResult(id: id, text: "Failed to click menu item '\(segment)' (AXPress error: \(pressResult.rawValue)).", isError: true)
                }
            } else {
                // Intermediate item — open its submenu
                // First check for submenu children
                var submenuChildren: AnyObject?
                AXUIElementCopyAttributeValue(item, kAXChildrenAttribute as CFString, &submenuChildren)
                if let subItems = submenuChildren as? [AXUIElement] {
                    for sub in subItems {
                        var subRole: AnyObject?
                        AXUIElementCopyAttributeValue(sub, kAXRoleAttribute as CFString, &subRole)
                        if (subRole as? String) == "AXMenu" {
                            // Open the submenu by hovering/pressing
                            AXUIElementPerformAction(item, kAXPressAction as CFString)
                            Thread.sleep(forTimeInterval: 0.2)
                            currentElement = sub
                            found = true
                            break
                        }
                    }
                }

                if !found {
                    // Maybe the submenu is accessed by pressing the item
                    AXUIElementPerformAction(item, kAXPressAction as CFString)
                    Thread.sleep(forTimeInterval: 0.2)

                    // Re-check for children after pressing
                    AXUIElementCopyAttributeValue(item, kAXChildrenAttribute as CFString, &submenuChildren)
                    if let subItems = submenuChildren as? [AXUIElement] {
                        for sub in subItems {
                            var subRole: AnyObject?
                            AXUIElementCopyAttributeValue(sub, kAXRoleAttribute as CFString, &subRole)
                            if (subRole as? String) == "AXMenu" {
                                currentElement = sub
                                found = true
                                break
                            }
                        }
                    }
                }

                if found { break }
            }
        }

        if !found {
            let available = collectAvailableTitles(from: currentElement)
            closeMenu(statusItem: result.statusItem)
            return makeToolResult(id: id, text: "Menu item '\(segment)' not found at path index \(index). Available items: \(available)", isError: true)
        }
    }

    closeMenu(statusItem: result.statusItem)
    return makeToolResult(id: id, text: "Unexpected state: path navigation completed without clicking.", isError: true)
}

func handleReadStatusBar(id: Any, arguments: [String: Any]) -> [String: Any] {
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID

    guard AXIsProcessTrusted() else {
        return makeToolResult(id: id, text: "Accessibility permission is not granted. Run 'check_accessibility' tool for instructions.", isError: true)
    }

    guard let app = findRunningApp(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    let pid = app.processIdentifier

    guard let menuBar = getExtrasMenuBar(for: pid) else {
        return makeToolResult(id: id, text: "Could not access extras menu bar for \(bundleID).", isError: true)
    }

    guard let statusItem = findStatusItem(in: menuBar, for: pid, bundleID: bundleID) else {
        let children = getStatusBarChildren(menuBar: menuBar)
        var descriptions: [String] = []
        for child in children {
            var title: AnyObject?
            AXUIElementCopyAttributeValue(child, kAXTitleAttribute as CFString, &title)
            var desc: AnyObject?
            AXUIElementCopyAttributeValue(child, kAXDescriptionAttribute as CFString, &desc)
            descriptions.append("title='\(title as? String ?? "nil")' desc='\(desc as? String ?? "nil")'")
        }
        return makeToolResult(id: id, text: "Could not find status bar item for \(bundleID). Available items: \(descriptions.joined(separator: "; "))", isError: true)
    }

    var info: [String: Any] = [
        "bundle_id": bundleID,
        "pid": Int(pid)
    ]

    var title: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXTitleAttribute as CFString, &title) == AXError.success {
        info["title"] = title as? String ?? ""
    }

    var description: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXDescriptionAttribute as CFString, &description) == AXError.success {
        info["description"] = description as? String ?? ""
    }

    var help: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXHelpAttribute as CFString, &help) == AXError.success {
        info["help"] = help as? String ?? ""
    }

    var value: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXValueAttribute as CFString, &value) == AXError.success {
        info["value"] = value as? String ?? ""
    }

    var role: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXRoleAttribute as CFString, &role) == AXError.success {
        info["role"] = role as? String ?? ""
    }

    var roleDesc: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXRoleDescriptionAttribute as CFString, &roleDesc) == AXError.success {
        info["role_description"] = roleDesc as? String ?? ""
    }

    var subrole: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXSubroleAttribute as CFString, &subrole) == AXError.success {
        info["subrole"] = subrole as? String ?? ""
    }

    // Read position and size
    var position: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXPositionAttribute as CFString, &position) == AXError.success {
        var point = CGPoint.zero
        if AXValueGetValue(position as! AXValue, .cgPoint, &point) {
            info["position"] = ["x": point.x, "y": point.y]
        }
    }

    var size: AnyObject?
    if AXUIElementCopyAttributeValue(statusItem, kAXSizeAttribute as CFString, &size) == AXError.success {
        var sizeVal = CGSize.zero
        if AXValueGetValue(size as! AXValue, .cgSize, &sizeVal) {
            info["size"] = ["width": sizeVal.width, "height": sizeVal.height]
        }
    }

    return makeToolResult(id: id, text: jsonString(info))
}

// MARK: - Screenshot Helpers

// Using deprecated CG APIs because ScreenCaptureKit requires async/await
// and entitlements that are impractical for a CLI tool.
@available(macOS, deprecated: 15.0, message: "Using CGWindowListCreateImage until ScreenCaptureKit migration")
func captureWindowImage(windowID: CGWindowID) -> CGImage? {
    let imageRef = CGWindowListCreateImage(
        .null,
        .optionIncludingWindow,
        windowID,
        [.boundsIgnoreFraming, .bestResolution]
    )
    return imageRef
}

@available(macOS, deprecated: 15.0, message: "Using CGDisplayCreateImage until ScreenCaptureKit migration")
func captureFullScreen() -> CGImage? {
    return CGDisplayCreateImage(CGMainDisplayID())
}

func findWindowID(for pid: pid_t, titleSubstring: String? = nil) -> CGWindowID? {
    guard let windowList = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] else {
        return nil
    }

    for window in windowList {
        guard let ownerPID = window[kCGWindowOwnerPID as String] as? Int,
              ownerPID == Int(pid),
              let windowID = window[kCGWindowNumber as String] as? Int else { continue }

        // Skip tiny windows (status bar items, etc.)
        if let bounds = window[kCGWindowBounds as String] as? [String: Any],
           let width = bounds["Width"] as? Double,
           let height = bounds["Height"] as? Double,
           width > 50 && height > 50 {

            if let titleSubstring = titleSubstring {
                let name = window[kCGWindowName as String] as? String ?? ""
                if name.localizedCaseInsensitiveContains(titleSubstring) {
                    return CGWindowID(windowID)
                }
            } else {
                // Return the first sizable window (likely the main window)
                return CGWindowID(windowID)
            }
        }
    }
    return nil
}

func pngData(from image: CGImage) -> Data? {
    let bitmapRep = NSBitmapImageRep(cgImage: image)
    return bitmapRep.representation(using: .png, properties: [:])
}

func saveOrEncode(imageData: Data, outputPath: String?) -> [String: Any] {
    if let path = outputPath, !path.isEmpty {
        do {
            try imageData.write(to: URL(fileURLWithPath: path))
            return [
                "success": true,
                "path": path,
                "size_bytes": imageData.count,
                "message": "Screenshot saved to \(path)"
            ]
        } catch {
            return [
                "success": false,
                "error": "Failed to write file: \(error.localizedDescription)"
            ]
        }
    } else {
        let base64 = imageData.base64EncodedString()
        return [
            "success": true,
            "format": "png",
            "size_bytes": imageData.count,
            "base64": base64
        ]
    }
}

func handleScreenshotWindow(id: Any, arguments: [String: Any]) -> [String: Any] {
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID
    let outputPath = arguments["output_path"] as? String
    let windowTitle = arguments["window_title"] as? String

    // Full screen capture
    if bundleID == "screen" {
        guard let image = captureFullScreen() else {
            return makeToolResult(id: id, text: "Failed to capture screen.", isError: true)
        }
        guard let data = pngData(from: image) else {
            return makeToolResult(id: id, text: "Failed to encode screenshot as PNG.", isError: true)
        }
        let result = saveOrEncode(imageData: data, outputPath: outputPath)
        return makeToolResult(id: id, text: jsonString(result))
    }

    // App window capture
    guard let app = findRunningApp(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    guard let windowID = findWindowID(for: app.processIdentifier, titleSubstring: windowTitle) else {
        return makeToolResult(id: id, text: "No visible window found for \(bundleID)" + (windowTitle != nil ? " with title containing '\(windowTitle!)'" : "") + ".", isError: true)
    }

    guard let image = captureWindowImage(windowID: windowID) else {
        return makeToolResult(id: id, text: "Failed to capture window (ID: \(windowID)).", isError: true)
    }

    guard let data = pngData(from: image) else {
        return makeToolResult(id: id, text: "Failed to encode screenshot as PNG.", isError: true)
    }

    let result = saveOrEncode(imageData: data, outputPath: outputPath)
    return makeToolResult(id: id, text: jsonString(result))
}

func handleScreenshotStatusBarMenu(id: Any, arguments: [String: Any]) -> [String: Any] {
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID
    let outputPath = arguments["output_path"] as? String ?? "/tmp/mcpproxy-menu-screenshot.png"

    guard AXIsProcessTrusted() else {
        return makeToolResult(id: id, text: "Accessibility permission is not granted.", isError: true)
    }

    guard let result = openStatusBarMenu(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    if let error = result.error, result.menuElement == nil {
        return makeToolResult(id: id, text: error, isError: true)
    }

    // Wait for menu to fully render
    Thread.sleep(forTimeInterval: 0.8)

    // Use osascript to invoke screencapture with inherited terminal permissions.
    // Direct CGDisplayCreateImage/CGWindowListCreateImage require Screen Recording
    // TCC permission which the ui-test binary may not have, producing black images.
    // osascript + do shell script inherits the terminal's TCC grants.
    let captureProcess = Process()
    captureProcess.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
    captureProcess.arguments = ["-e", "do shell script \"/usr/sbin/screencapture -x \(outputPath)\""]
    do {
        try captureProcess.run()
        captureProcess.waitUntilExit()
    } catch {
        log("osascript screencapture failed: \(error)")
    }

    // Brief pause then close menu
    Thread.sleep(forTimeInterval: 0.2)
    closeMenu(statusItem: result.statusItem)

    // Read the captured file
    if let data = try? Data(contentsOf: URL(fileURLWithPath: outputPath)), data.count > 1000 {
        let saveResult: [String: Any] = [
            "success": true,
            "path": outputPath,
            "size_bytes": data.count,
            "message": "Screenshot saved to \(outputPath)"
        ]
        return makeToolResult(id: id, text: jsonString(saveResult))
    } else {
        return makeToolResult(id: id, text: jsonString([
            "success": false,
            "error": "Screen Recording permission required. Grant permission to Terminal/Claude Code in System Settings > Privacy & Security > Screen Recording, OR use 'screencapture -x /tmp/menu.png' from bash while menu is open.",
            "workaround": "Use list_menu_items to verify menu contents without a screenshot."
        ] as [String: Any]), isError: true)
    }
}

// MARK: - Keypress Helpers

/// Map a character string to a macOS virtual key code.
/// Virtual key codes are hardware-independent (unlike CGKeyCode from key position).
func virtualKeyCode(for key: String) -> (keyCode: UInt16, needsShift: Bool)? {
    switch key.lowercased() {
    // Row 1: number row
    case "`", "~": return (50, key == "~")
    case "1", "!": return (18, key == "!")
    case "2", "@": return (19, key == "@")
    case "3", "#": return (20, key == "#")
    case "4", "$": return (21, key == "$")
    case "5", "%": return (23, key == "%")
    case "6", "^": return (22, key == "^")
    case "7", "&": return (26, key == "&")
    case "8", "*": return (28, key == "*")
    case "9", "(": return (25, key == "(")
    case "0", ")": return (29, key == ")")
    case "-", "_": return (27, key == "_")
    case "=", "+": return (24, key == "+")
    // Row 2: QWERTY
    case "q": return (12, false)
    case "w": return (13, false)
    case "e": return (14, false)
    case "r": return (15, false)
    case "t": return (17, false)
    case "y": return (16, false)
    case "u": return (32, false)
    case "i": return (34, false)
    case "o": return (31, false)
    case "p": return (35, false)
    case "[", "{": return (33, key == "{")
    case "]", "}": return (30, key == "}")
    case "\\", "|": return (42, key == "|")
    // Row 3: ASDF
    case "a": return (0, false)
    case "s": return (1, false)
    case "d": return (2, false)
    case "f": return (3, false)
    case "g": return (5, false)
    case "h": return (4, false)
    case "j": return (38, false)
    case "k": return (40, false)
    case "l": return (37, false)
    case ";", ":": return (41, key == ":")
    case "'", "\"": return (39, key == "\"")
    // Row 4: ZXCV
    case "z": return (6, false)
    case "x": return (7, false)
    case "c": return (8, false)
    case "v": return (9, false)
    case "b": return (11, false)
    case "n": return (45, false)
    case "m": return (46, false)
    case ",", "<": return (43, key == "<")
    case ".", ">": return (47, key == ">")
    case "/", "?": return (44, key == "?")
    // Special keys
    case "space", " ": return (49, false)
    case "return", "enter": return (36, false)
    case "tab": return (48, false)
    case "delete", "backspace": return (51, false)
    case "escape", "esc": return (53, false)
    case "left": return (123, false)
    case "right": return (124, false)
    case "down": return (125, false)
    case "up": return (126, false)
    case "f1": return (122, false)
    case "f2": return (120, false)
    case "f3": return (99, false)
    case "f4": return (118, false)
    case "f5": return (96, false)
    case "f6": return (97, false)
    case "f7": return (98, false)
    case "f8": return (100, false)
    case "f9": return (101, false)
    case "f10": return (109, false)
    case "f11": return (103, false)
    case "f12": return (111, false)
    default: return nil
    }
}

/// Parse a key combo string like "cmd+=" or "cmd+shift+=" into modifiers and key code.
func parseKeyCombo(_ combo: String) -> (keyCode: UInt16, flags: CGEventFlags)? {
    let parts = combo.lowercased().components(separatedBy: "+")
    guard parts.count >= 1 else { return nil }

    var flags: CGEventFlags = []
    var keyPart = ""

    for (index, part) in parts.enumerated() {
        let trimmed = part.trimmingCharacters(in: .whitespaces)
        if index == parts.count - 1 {
            // Last part is the key (unless it's empty from trailing +)
            if trimmed.isEmpty {
                // Trailing "+" means the key is literally "+"
                keyPart = "+"
            } else {
                keyPart = trimmed
            }
        } else {
            switch trimmed {
            case "cmd", "command": flags.insert(.maskCommand)
            case "shift": flags.insert(.maskShift)
            case "ctrl", "control": flags.insert(.maskControl)
            case "opt", "option", "alt": flags.insert(.maskAlternate)
            default:
                // Unknown modifier; maybe the user meant a multi-char key
                return nil
            }
        }
    }

    // Special handling: if keyPart is empty and we have a modifier that looks like a key
    guard !keyPart.isEmpty else { return nil }

    guard let (code, needsShift) = virtualKeyCode(for: keyPart) else {
        return nil
    }

    if needsShift {
        flags.insert(.maskShift)
    }

    return (code, flags)
}

func handleSendKeypress(id: Any, arguments: [String: Any]) -> [String: Any] {
    guard let keyCombo = arguments["key"] as? String, !keyCombo.isEmpty else {
        return makeToolResult(id: id, text: "The 'key' argument is required (e.g. 'cmd+=', 'cmd+-', 'cmd+0').", isError: true)
    }
    let bundleID = arguments["bundle_id"] as? String ?? configuredBundleID
    let repeatCount = arguments["repeat"] as? Int ?? 1

    guard let (keyCode, flags) = parseKeyCombo(keyCombo) else {
        return makeToolResult(id: id, text: "Could not parse key combo '\(keyCombo)'. Expected format: 'cmd+=', 'cmd+shift+=', 'cmd+-', 'cmd+0', etc. Supported modifiers: cmd, shift, ctrl, opt/alt. Key must be a single character or special key name (space, return, tab, escape, f1-f12, left/right/up/down).", isError: true)
    }

    // Activate the target app so it receives key events
    guard let app = findRunningApp(bundleID: bundleID) else {
        return makeToolResult(id: id, text: "Application with bundle ID '\(bundleID)' is not running.", isError: true)
    }

    app.activate()
    Thread.sleep(forTimeInterval: 0.3) // Wait for app to come to front

    var sentCount = 0
    for i in 0..<max(1, repeatCount) {
        guard let keyDown = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: true),
              let keyUp = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: false) else {
            return makeToolResult(id: id, text: "Failed to create CGEvent for key code \(keyCode) (iteration \(i)).", isError: true)
        }

        keyDown.flags = flags
        keyUp.flags = flags

        keyDown.post(tap: .cghidEventTap)
        Thread.sleep(forTimeInterval: 0.02)
        keyUp.post(tap: .cghidEventTap)
        sentCount += 1

        if i < repeatCount - 1 {
            Thread.sleep(forTimeInterval: 0.1) // Brief pause between repeats
        }
    }

    let result: [String: Any] = [
        "success": true,
        "key_combo": keyCombo,
        "key_code": Int(keyCode),
        "modifiers": describeCGFlags(flags),
        "repeat_count": sentCount,
        "target_app": bundleID,
        "message": "Sent '\(keyCombo)' \(sentCount) time(s) to \(bundleID)"
    ]
    return makeToolResult(id: id, text: jsonString(result))
}

func describeCGFlags(_ flags: CGEventFlags) -> [String] {
    var mods: [String] = []
    if flags.contains(.maskCommand) { mods.append("cmd") }
    if flags.contains(.maskShift) { mods.append("shift") }
    if flags.contains(.maskControl) { mods.append("ctrl") }
    if flags.contains(.maskAlternate) { mods.append("opt") }
    return mods
}

// MARK: - Request Handling

func handleRequest(_ request: [String: Any]) {
    let method = request["method"] as? String ?? ""
    let id = request["id"] // Can be nil for notifications

    switch method {
    case "initialize":
        guard let reqID = id else {
            log("Initialize request missing id")
            return
        }
        let result: [String: Any] = [
            "protocolVersion": "2024-11-05",
            "capabilities": [
                "tools": [:] as [String: Any]
            ],
            "serverInfo": [
                "name": "mcpproxy-ui-test",
                "version": "1.0.0"
            ]
        ]
        sendResponse(makeResponse(id: reqID, result: result))
        log("Handled initialize")

    case "notifications/initialized":
        // Notification — no response needed
        log("Received initialized notification")

    case "tools/list":
        guard let reqID = id else {
            log("tools/list request missing id")
            return
        }
        let result: [String: Any] = [
            "tools": toolDefinitions()
        ]
        sendResponse(makeResponse(id: reqID, result: result))
        log("Handled tools/list")

    case "tools/call":
        guard let reqID = id else {
            log("tools/call request missing id")
            return
        }
        guard let params = request["params"] as? [String: Any],
              let toolName = params["name"] as? String else {
            sendResponse(makeError(id: reqID, code: -32602, message: "Invalid params: missing tool name"))
            return
        }
        let arguments = params["arguments"] as? [String: Any] ?? [:]

        log("Calling tool: \(toolName)")

        // All AX calls must happen on the main thread
        var response: [String: Any]?
        if Thread.isMainThread {
            response = dispatchToolCall(id: reqID, toolName: toolName, arguments: arguments)
        } else {
            DispatchQueue.main.sync {
                response = dispatchToolCall(id: reqID, toolName: toolName, arguments: arguments)
            }
        }

        if let resp = response {
            sendResponse(resp)
        }

    case "ping":
        if let reqID = id {
            sendResponse(makeResponse(id: reqID, result: [:] as [String: Any]))
        }
        log("Handled ping")

    default:
        if let reqID = id {
            sendResponse(makeError(id: reqID, code: -32601, message: "Method not found: \(method)"))
        }
        log("Unknown method: \(method)")
    }
}

func dispatchToolCall(id: Any, toolName: String, arguments: [String: Any]) -> [String: Any] {
    switch toolName {
    case "check_accessibility":
        return handleCheckAccessibility(id: id)
    case "list_running_apps":
        return handleListRunningApps(id: id)
    case "list_menu_items":
        return handleListMenuItems(id: id, arguments: arguments)
    case "click_menu_item":
        return handleClickMenuItem(id: id, arguments: arguments)
    case "read_status_bar":
        return handleReadStatusBar(id: id, arguments: arguments)
    case "screenshot_window":
        return handleScreenshotWindow(id: id, arguments: arguments)
    case "screenshot_status_bar_menu":
        return handleScreenshotStatusBarMenu(id: id, arguments: arguments)
    case "send_keypress":
        return handleSendKeypress(id: id, arguments: arguments)
    default:
        return makeToolResult(id: id, text: "Unknown tool: \(toolName). Available tools: check_accessibility, list_running_apps, list_menu_items, click_menu_item, read_status_bar, screenshot_window, screenshot_status_bar_menu, send_keypress", isError: true)
    }
}

// MARK: - CLI Argument Parsing

func parseArguments() {
    let args = CommandLine.arguments
    var i = 1
    while i < args.count {
        if args[i] == "--bundle-id" && i + 1 < args.count {
            configuredBundleID = args[i + 1]
            i += 2
        } else if args[i].starts(with: "--bundle-id=") {
            configuredBundleID = String(args[i].dropFirst("--bundle-id=".count))
            i += 1
        } else if args[i] == "--help" || args[i] == "-h" {
            FileHandle.standardError.write(Data("""
            mcpproxy-ui-test: MCP server for macOS Accessibility API testing

            Usage: mcpproxy-ui-test [--bundle-id <id>]

            Options:
              --bundle-id <id>  Target app bundle ID (default: \(defaultBundleID))
              --help, -h        Show this help message

            This server communicates via MCP JSON-RPC 2.0 over stdio.
            Send JSON-RPC requests to stdin, receive responses on stdout.

            Tools:
              check_accessibility       Check if Accessibility API access is granted
              list_running_apps         List running macOS applications
              list_menu_items           List status bar menu items for an app
              click_menu_item           Click a menu item by path
              read_status_bar           Read status bar item info
              screenshot_window         Take a screenshot of an app window or full screen
              screenshot_status_bar_menu  Screenshot the status bar menu (opens and captures)
              send_keypress             Send keyboard shortcut to an app (e.g. cmd+=, cmd+-, cmd+0)

            """.utf8))
            exit(0)
        } else {
            FileHandle.standardError.write(Data("Unknown argument: \(args[i])\n".utf8))
            exit(1)
        }
    }
}

// MARK: - Main Entry Point

parseArguments()
log("Starting mcpproxy-ui-test MCP server (bundle_id: \(configuredBundleID))")

// Pending request counter for graceful shutdown
let pendingGroup = DispatchGroup()

// Start reading stdin on a background thread
let stdinQueue = DispatchQueue(label: "stdin-reader", qos: .userInitiated)
stdinQueue.async {
    let handle = FileHandle.standardInput
    var buffer = Data()

    while true {
        let chunk = handle.availableData
        if chunk.isEmpty {
            // EOF — stdin closed. Wait for all pending requests to finish.
            log("stdin EOF, waiting for pending requests...")
            pendingGroup.wait()
            log("All requests done, exiting")
            DispatchQueue.main.async {
                exit(0)
            }
            // Keep this thread alive while main processes exit
            Thread.sleep(forTimeInterval: 5.0)
            return
        }

        buffer.append(chunk)

        // Process complete lines
        while let newlineRange = buffer.range(of: Data("\n".utf8)) {
            let lineData = buffer.subdata(in: buffer.startIndex..<newlineRange.lowerBound)
            buffer.removeSubrange(buffer.startIndex...newlineRange.lowerBound)

            guard let line = String(data: lineData, encoding: .utf8)?.trimmingCharacters(in: .whitespaces),
                  !line.isEmpty else {
                continue
            }

            guard let request = parseJSON(line) else {
                log("Failed to parse JSON: \(line.prefix(200))")
                let errorResp: [String: Any] = [
                    "jsonrpc": "2.0",
                    "id": NSNull(),
                    "error": [
                        "code": -32700,
                        "message": "Parse error"
                    ]
                ]
                sendResponse(errorResp)
                continue
            }

            // Dispatch to main thread for AX API calls
            pendingGroup.enter()
            DispatchQueue.main.async {
                handleRequest(request)
                pendingGroup.leave()
            }
        }
    }
}

// Run the main run loop to keep the process alive and handle AX callbacks
RunLoop.main.run()
