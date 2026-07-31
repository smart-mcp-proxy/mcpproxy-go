// GlanceSelection.swift
// MCPProxy
//
// Display rules for the tray glance section: which activity records qualify
// as rows, how duplicates collapse, and which sessions count as clients.
// Pure functions over ActivityEntry / APIClient.MCPSession — no AppKit.

import Foundation

/// One rendered activity row: a maximal run of *consecutive* qualifying records
/// that share a group key (server, tool, outcome class) — spec 090 FR-001…004.
///
/// Consecutive-only is the whole point: the glance is a timeline, and clustering
/// non-adjacent calls would claim an ordering that never happened. A burst of 19
/// `jira_get_issue` calls becomes one row; the same tool called again after
/// something else stays a second row.
///
/// The run is a *view* over the records, not a summary of them: every derived
/// value names the record it came from, so a row can always be traced back.
struct GlanceRun: Equatable {

    /// What makes two adjacent records "the same thing happening again".
    ///
    /// Outcome class is in the key so a policy block never merges into a run of
    /// calls to the same tool (FR-002): "27 blocked attempts" and "27 calls" are
    /// different stories, and blocks are the ones worth reading.
    struct Key: Equatable {
        let server: String
        let tool: String
        let outcomeClass: OutcomeClass

        init(for entry: ActivityEntry) {
            self.server = entry.serverName ?? ""
            self.tool = entry.toolName ?? ""
            self.outcomeClass = entry.outcomeClass
        }
    }

    let key: Key

    /// The run's records, newest first (the feed's own order), never empty.
    let records: [ActivityEntry]

    init(key: Key, records: [ActivityEntry]) {
        precondition(!records.isEmpty, "a run is a run of at least one record")
        self.key = key
        self.records = records
    }

    /// Repeat count — rendered as a `×N` suffix only when > 1 (FR-003).
    ///
    /// It counts what the fetched page holds and nothing more: a run longer than
    /// the 100-record poll window reads ×100 (spec Edge Cases).
    var count: Int { records.count }

    /// The record whose clock the row shows.
    var newest: ActivityEntry { records[0] }

    /// The record the row is *identified* by — see `identity`.
    var oldest: ActivityEntry { records[records.count - 1] }

    /// Stable identity for in-place updates (FR-024).
    ///
    /// The oldest record, not the newest: a run grows at the head, so keying on
    /// the newest would make every additional call in a burst look like a
    /// brand-new row to `GlanceSection.updateInPlace` and rewrite the whole row
    /// identity — icon, tooltip and click payload — on every tick.
    var identity: String { GlanceSelection.recordKey(for: oldest) }

    /// The newest failing record in the run, if any. `records` is newest-first,
    /// so `first` is the newest.
    var newestErroring: ActivityEntry? { records.first { $0.status == "error" } }

    /// Worst outcome in the run, error beating success (FR-004). A run with no
    /// failure reports the newest record's own status, so a call still running
    /// is never announced as a failure.
    var worstStatus: String { newestErroring?.status ?? newest.status }

    /// The error clause to show, from the NEWEST erroring record (FR-004).
    var errorMessage: String? { newestErroring?.errorMessage }

    /// The reason to show: the newest record in the run that has one (FR-004) —
    /// a later call that omitted its intent must not blank out the row.
    var displayReason: String? { records.lazy.compactMap(\.reason).first }

    /// The row's clock: the age of the newest record (FR-004).
    var timestamp: String { newest.timestamp }
}

/// Presentation policy for the glance section. Pure and synchronous.
enum GlanceSelection {

    /// Proxy administration built-ins. Never shown, whatever their status.
    static let managementBuiltIns: Set<String> = ["upstream_servers", "quarantine_security"]

    /// Discovery/execution built-ins that are worth a row even on success.
    static let glanceInternalTools: Set<String> = ["retrieve_tools", "code_execution", "describe_tool"]

    /// How many rows each list shows.
    static let rowLimit = 5

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

    // MARK: - Rule 4

    /// Collapse records sharing a `request_id`, keeping the `tool_call` one.
    ///
    /// The surviving record is emitted at the position of the first record of
    /// its group so recency ordering is preserved. Records with no request id
    /// are never collapsed. When a group holds several `tool_call` records the
    /// first one encountered wins — later ones are dropped, not merged.
    ///
    /// Why this is safe today: the core mints a request id per dispatch
    /// (`internal/server/mcp.go`, both `requestID := fmt.Sprintf("%d-%s-%s", …)`
    /// sites under "Generate requestID for activity tracking"), so two distinct
    /// upstream calls carry distinct ids structurally. A group is therefore a
    /// wrapper plus its upstream partner, never a fan-out.
    ///
    /// What would invalidate that: `internal/server/mcp_code_execution.go` sets
    /// `RequestID: u.executionID` ("Use execution ID as request ID to link
    /// nested calls") on every nested call a `code_execution` script makes, so
    /// all of them share ONE id. Those records are currently invisible here —
    /// the nested caller only writes to the legacy history via
    /// `storage.RecordToolCall` and emits no activity event. If nested calls
    /// are ever wired into the activity stream, this function would collapse an
    /// entire multi-tool script down to a single row. At that point rule 4 must
    /// narrow to "collapse a wrapper with its upstream partner" rather than
    /// deduplicating a whole request id.
    static func collapseByRequestID(_ entries: [ActivityEntry]) -> [ActivityEntry] {
        var winners: [String: ActivityEntry] = [:]
        for entry in entries {
            guard let rid = entry.requestId, !rid.isEmpty else { continue }
            guard let existing = winners[rid] else {
                winners[rid] = entry
                continue
            }
            if existing.type != "tool_call" && entry.type == "tool_call" {
                winners[rid] = entry
            }
        }

        var emitted = Set<String>()
        var result: [ActivityEntry] = []
        for entry in entries {
            guard let rid = entry.requestId, !rid.isEmpty else {
                result.append(entry)
                continue
            }
            if emitted.contains(rid) { continue }
            emitted.insert(rid)
            result.append(winners[rid] ?? entry)
        }
        return result
    }

    // MARK: - Record identity

    /// Identity of one call: its `requestId`, never its `id`.
    ///
    /// A row rendered from a live SSE event carries a provisional id of the
    /// form `"<request_id>:<type>"`, which the reconciling poll replaces with
    /// the storage-assigned ULID for the very same call — so `id` reports a
    /// wholesale turnover on every poll while `requestId` is identical on both
    /// sides. It is what rule 4 collapses on, what `AppState`'s merge keys on,
    /// and what `GlanceSection` diffs rows by. Records with no request id are
    /// never collapsed, so their `id` is a safe fallback.
    static func recordKey(for entry: ActivityEntry) -> String {
        if let requestId = entry.requestId, !requestId.isEmpty { return requestId }
        return entry.id
    }

    // MARK: - Rule 5

    /// Fold each maximal stretch of consecutive records sharing a group key into
    /// one `GlanceRun`, preserving feed order (spec 090 FR-001 step 3).
    ///
    /// This runs *after* qualification and collapse, and that order is the
    /// requirement, not an implementation detail: a record that never renders —
    /// a management built-in, a wrapper folded into its upstream partner — must
    /// not split the run around it (US1 scenario 6). Filtering afterwards would
    /// leave two adjacent identical rows wherever noise happened to land between
    /// two calls to the same tool.
    static func groupConsecutive(_ entries: [ActivityEntry]) -> [GlanceRun] {
        var runs: [GlanceRun] = []
        var currentKey: GlanceRun.Key?
        var current: [ActivityEntry] = []

        for entry in entries {
            let key = GlanceRun.Key(for: entry)
            if key == currentKey {
                current.append(entry)
                continue
            }
            if let currentKey, !current.isEmpty {
                runs.append(GlanceRun(key: currentKey, records: current))
            }
            currentKey = key
            current = [entry]
        }
        if let currentKey, !current.isEmpty {
            runs.append(GlanceRun(key: currentKey, records: current))
        }
        return runs
    }

    // MARK: - Public entry points

    /// The row pipeline, in the one order the spec fixes (FR-001):
    /// qualify → collapse by request id → group consecutive runs → take `limit`.
    ///
    /// Every step is a narrowing, and each one must see the output of the one
    /// before it: grouping before collapsing would count a wrapper and its
    /// upstream partner as two calls, and taking the first five before grouping
    /// would hand the whole menu to a single burst.
    static func activityRows(from entries: [ActivityEntry], limit: Int = rowLimit) -> [GlanceRun] {
        let qualified = entries.filter(qualifies)
        let collapsed = collapseByRequestID(qualified)
        return Array(groupConsecutive(collapsed).prefix(limit))
    }

    /// Sessions currently connected, capped at `limit`, input order preserved.
    static func activeClients(
        from sessions: [APIClient.MCPSession],
        limit: Int = rowLimit
    ) -> [APIClient.MCPSession] {
        Array(sessions.filter { $0.status == "active" }.prefix(limit))
    }
}
