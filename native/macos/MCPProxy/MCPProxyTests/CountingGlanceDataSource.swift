// CountingGlanceDataSource.swift
// MCPProxyTests
//
// A GlanceDataSource that performs no I/O and counts calls. Used to pin the
// spec-048 invariant: building the tray menu issues zero requests.

import Foundation
@testable import MCPProxy

final class CountingGlanceDataSource: GlanceDataSource {

    private(set) var usageCallCount = 0
    private(set) var activityCallCount = 0
    private(set) var sessionCallCount = 0

    /// Total requests this data source was asked to make.
    var totalCallCount: Int { usageCallCount + activityCallCount + sessionCallCount }

    var usageToReturn = UsageAggregateResponse(
        window: "24h",
        tokenSource: "bytes",
        tokensSaved: 0,
        tokensSavedPercentage: 0,
        timeline: []
    )
    var activityToReturn: [ActivityEntry] = []
    var sessionsToReturn: [APIClient.MCPSession] = []

    func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse {
        usageCallCount += 1
        return usageToReturn
    }

    func glanceActivity(limit: Int) async throws -> [ActivityEntry] {
        activityCallCount += 1
        return activityToReturn
    }

    func activeSessions(limit: Int) async throws -> [APIClient.MCPSession] {
        sessionCallCount += 1
        return sessionsToReturn
    }
}
