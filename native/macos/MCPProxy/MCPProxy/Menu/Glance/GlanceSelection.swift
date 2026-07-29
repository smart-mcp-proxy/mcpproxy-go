// GlanceSelection.swift
// MCPProxy
//
// Display rules for the tray glance section: which activity records qualify
// as rows, how duplicates collapse, and which sessions count as clients.
// Pure functions over ActivityEntry / APIClient.MCPSession — no AppKit.

import Foundation

/// Presentation policy for the glance section. Pure and synchronous.
enum GlanceSelection {

    /// Proxy administration built-ins. Never shown, whatever their status.
    static let managementBuiltIns: Set<String> = ["upstream_servers", "quarantine_security"]

    /// Discovery/execution built-ins that are worth a row even on success.
    static let glanceInternalTools: Set<String> = ["retrieve_tools", "code_execution", "describe_tool"]

    // MARK: - Rules 1-3

    /// Whether a single record qualifies for a glance row.
    static func qualifies(_ entry: ActivityEntry) -> Bool {
        let tool = entry.toolName ?? ""

        // Rule 1 — management built-ins are excluded, whatever the status.
        if managementBuiltIns.contains(tool) { return false }

        // Rule 2 — every real upstream call.
        if entry.type == "tool_call" { return true }

        // Rule 3 — discovery/execution built-ins, plus any internal failure
        // (a wrapper that died before dispatch has no upstream record).
        if entry.type == "internal_tool_call" {
            return glanceInternalTools.contains(tool) || entry.status != "success"
        }

        return false
    }
}
