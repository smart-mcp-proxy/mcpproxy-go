import SwiftUI

/// Label, icon and colour of an activity row's type, used by ActivityView.
///
/// Every value of the core's `storage.ValidActivityTypes` has an explicit case
/// in each function: the Go test `TestValidActivityTypesHaveASwiftLabel`
/// (internal/storage) reads this file and fails for a type added without one.
/// Labels follow the Web UI (frontend/src/utils/activity.ts) except the short
/// forms the app already used for the original types.
enum ActivityTypePresentation {
    static func label(_ type: String) -> String {
        switch type {
        case "tool_call": return "Tool Call"
        case "internal_tool_call": return "Internal Tool"
        case "tool_quarantine_change": return "Quarantine"
        case "quarantine_change": return "Quarantine Change"
        case "system_start": return "System Start"
        case "system_stop": return "System Stop"
        case "config_change": return "Config Change"
        case "policy_decision": return "Policy"
        case "server_change": return "Server Change"
        case "security_scan": return "Security Scan"
        case "credential_broker": return "Credential Broker"
        case "preflight": return "Preflight"
        case "prompt_get": return "Prompt Fetch"
        case "profile_change": return "Profile Change"
        case "upstream_event": return "Upstream Event"
        // Spec 125: administrator-only skill rows.
        case "skill_quarantine_change": return "Skill Approval Change"
        case "skill_access": return "Skill Access"
        default: return type
        }
    }

    static func icon(_ type: String) -> String {
        switch type {
        case "tool_call": return "wrench.fill"
        case "internal_tool_call": return "gearshape.fill"
        case "tool_quarantine_change": return "shield.fill"
        case "quarantine_change": return "exclamationmark.shield.fill"
        case "system_start": return "play.circle.fill"
        case "system_stop": return "stop.circle.fill"
        case "config_change": return "slider.horizontal.3"
        case "policy_decision": return "hand.raised.fill"
        case "server_change": return "arrow.triangle.2.circlepath"
        case "security_scan": return "magnifyingglass"
        case "credential_broker": return "key.fill"
        case "preflight": return "airplane.departure"
        case "prompt_get": return "text.bubble.fill"
        case "profile_change": return "person.text.rectangle.fill"
        case "upstream_event": return "powerplug.fill"
        case "skill_quarantine_change": return "books.vertical.fill"
        case "skill_access": return "book.fill"
        default: return "circle.fill"
        }
    }

    static func color(_ type: String) -> Color {
        switch type {
        case "tool_call": return .blue
        case "internal_tool_call": return .indigo
        case "tool_quarantine_change": return .orange
        case "quarantine_change": return .orange
        case "system_start": return .green
        case "system_stop": return .red
        case "config_change": return .purple
        case "policy_decision": return .orange
        case "server_change": return .teal
        case "security_scan": return .cyan
        case "credential_broker": return .yellow
        case "preflight": return .mint
        case "prompt_get": return .blue
        case "profile_change": return .purple
        case "upstream_event": return .teal
        case "skill_quarantine_change": return .orange
        case "skill_access": return .brown
        default: return .gray
        }
    }
}
