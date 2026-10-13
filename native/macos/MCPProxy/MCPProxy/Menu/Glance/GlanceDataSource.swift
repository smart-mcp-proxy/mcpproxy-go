// GlanceDataSource.swift
// MCPProxy
//
// The narrow read surface the tray glance section depends on.

import Foundation

/// The three reads that feed the tray glance section.
///
/// The glance component depends on this protocol rather than on the concrete
/// `APIClient` actor so a counting stub can be injected in tests. That is what
/// makes the spec-048 invariant testable: opening the menu must perform zero
/// network requests, which can only be asserted if the requests are countable.
protocol GlanceDataSource {
    /// `GET /api/v1/activity/usage?window=<window>&top=<top>`
    func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse

    /// `GET /api/v1/activity?type=tool_call,internal_tool_call,policy_decision&limit=<limit>`
    func glanceActivity(limit: Int) async throws -> [ActivityEntry]

    /// `GET /api/v1/sessions?limit=<limit>` — unfiltered by status, because the
    /// Clients section shows presence (active / idle / seen) over every retained
    /// session, not just the ones that happen to be open.
    func recentSessions(limit: Int) async throws -> [APIClient.MCPSession]
}

/// The production data source. `APIClient` already has all three methods with
/// matching signatures, so the conformance is declaration-only.
extension APIClient: GlanceDataSource {}
