// GlanceLinks.swift
// MCPProxy
//
// Web UI deep links opened from glance rows.

import Foundation

/// Build the Web UI activity-log URL, optionally filtered by session.
///
/// `?session=` is the query parameter the Activity view reads on mount
/// (frontend/src/views/Activity.vue:1334, `route.query.session`), and the Web
/// UI router is history-based (createWebHistory over `base: '/ui/'`), so
/// `/ui/activity` is a real path rather than a fragment. `apikey` travels as a
/// query parameter because a browser cannot send the `X-API-Key` header —
/// `/ui/` is the one surface that accepts it, and the Web UI strips only
/// `apikey` from the address bar on load (services/api.ts:69-80), keeping
/// `session`.
func activityURLString(baseURL: String, apiKey: String, sessionID: String?) -> String {
    let path = baseURL + "/ui/activity"
    var query: [URLQueryItem] = []
    if let sessionID, !sessionID.isEmpty {
        query.append(URLQueryItem(name: "session", value: sessionID))
    }
    if !apiKey.isEmpty {
        query.append(URLQueryItem(name: "apikey", value: apiKey))
    }
    guard var components = URLComponents(string: path) else { return path }
    components.queryItems = query.isEmpty ? nil : query
    return components.string ?? path
}
