// ToolLabels.swift
// MCPProxy
//
// Spec 109 FR-027 / FR-028: one vocabulary for tool review states and tool
// tier, shared by every view that shows either — so the terminology table in
// specs/109-ux-navigation-consistency/spec.md never drifts between
// ServerDetailView, ToolsView and any review surface. Pinned by
// MCPProxyTests/ToolLabelsTests.swift.
//
// Neither function computes anything: `approval_status` and `tier` are both
// server-resolved fields (the tier by the one Go function
// `contracts.AnnotationTier`, FR-028/X11) — these are display labels only.

import Foundation

enum ToolLabels {
    /// `approval_status` -> display label (FR-027). The old "Awaiting
    /// approval" / "Pending Approval" wording is gone — `pending` and
    /// `changed` both read as "<state>, needs review" now.
    static func approvalStatusLabel(_ status: String?) -> String {
        switch status {
        case "approved": return "Approved"
        case "pending": return "New, needs review"
        case "changed": return "Changed, needs review"
        default: return status?.capitalized ?? "Unknown"
        }
    }

    /// `tier` -> display label (FR-028). `unknown` only ever appears in the
    /// review payload, for a tool captured before this spec stored
    /// annotations — everywhere else the backend sends `unannotated` for a
    /// tool with no hints.
    static func tierLabel(_ tier: String?) -> String {
        switch tier {
        case "read": return "Read"
        case "write": return "Write"
        case "destructive": return "Destructive"
        case "unannotated": return "Unannotated"
        case "unknown": return "Unknown"
        default: return "Unannotated"
        }
    }
}
