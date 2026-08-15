package server

import (
	"sort"
	"time"
)

// Issue #969 (Phase 0) — preflight BASELINE counters.
//
// These ship one release AHEAD of the required-tools-preflight feature so a
// real live before/after window exists: without it, "did preflight reduce
// silent unavailability?" can only be argued from anecdotes. Everything here is
// counts-only — no tool name, server name, query, session id, or free text ever
// reaches telemetry (see internal/telemetry/preflight_counters.go for the
// enforced contract).

// recordFilterDiagnosticsEmitted counts one retrieve_tools response that
// attached a spec-094 filter_diagnostics block, summing the block's per-reason
// classes across every filter it blamed. The counts are already computed by
// filterByAnnotationsWithDiagnostics — this only adds them up. nil-safe.
func (p *MCPProxyServer) recordFilterDiagnosticsEmitted(diag *filterDiagnostics) {
	if diag == nil || p.mainServer == nil || p.mainServer.runtime == nil {
		return
	}
	missing, explicit := 0, 0
	for _, counts := range diag.OmittedByFilter {
		missing += counts.MissingAnnotation
		explicit += counts.Explicit
	}
	p.mainServer.runtime.RecordFilterDiagnosticsEmitted(missing, explicit)
}

// recordDiscoveryOmission counts one retrieve_tools response that withheld
// locked/quarantined matches the caller could not see. nil-safe.
func (p *MCPProxyServer) recordDiscoveryOmission() {
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return
	}
	p.mainServer.runtime.RecordDiscoveryOmission()
}

// recordFilterDiagnosticsFollowed counts one diagnostics block the agent acted
// on. nil-safe.
func (p *MCPProxyServer) recordFilterDiagnosticsFollowed() {
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return
	}
	p.mainServer.runtime.RecordFilterDiagnosticsFollowed()
}

// recordAvailabilityBlock counts one policy block under its structured reason
// key. nil-safe.
func (p *MCPProxyServer) recordAvailabilityBlock(reason string) {
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return
	}
	p.mainServer.runtime.RecordAvailabilityBlock(reason)
}

// --- filter-diagnostics follow-through, per session ---

// filterDiagNote remembers the filters a diagnostics block blamed on the last
// retrieve_tools call of one MCP session. It deliberately carries nothing else:
// no query, no tool ids, no counts — just the closed set of filter parameter
// names the response already told the agent about, plus when it was written so
// stale notes expire.
type filterDiagNote struct {
	// filters are the filter keys the block blamed (filterKeyReadOnlyOnly &c),
	// sorted so comparisons are order-independent.
	filters []string
	at      time.Time
}

const (
	// filterDiagNoteTTL bounds how long a note stays eligible. A follow-up an
	// hour later is a new task, not a reaction to the diagnostics block.
	filterDiagNoteTTL = 15 * time.Minute

	// maxFilterDiagNotes bounds the in-memory note map. Sessions are pruned
	// oldest-first past this; a proxy serving thousands of sessions must not
	// grow an unbounded map for a telemetry counter.
	maxFilterDiagNotes = 256
)

// activeFilterKeys renders the filter parameters active on THIS call as the
// same key strings the diagnostics block uses.
func activeFilterKeys(readOnlyOnly, excludeDestructive, excludeOpenWorld bool) []string {
	keys := make([]string, 0, 3)
	if readOnlyOnly {
		keys = append(keys, filterKeyReadOnlyOnly)
	}
	if excludeDestructive {
		keys = append(keys, filterKeyExcludeDestruct)
	}
	if excludeOpenWorld {
		keys = append(keys, filterKeyExcludeOpenWorld)
	}
	return keys
}

// noteFilterDiagnostics remembers that this session was just handed a
// diagnostics block blaming `diag`'s filters. Sessions without an id (stdio
// transports that do not mint one) are skipped rather than pooled under "",
// which would let one client's call count as another's follow-up.
func (p *MCPProxyServer) noteFilterDiagnostics(sessionID string, diag *filterDiagnostics, at time.Time) {
	if sessionID == "" || diag == nil || len(diag.OmittedByFilter) == 0 {
		return
	}
	filters := make([]string, 0, len(diag.OmittedByFilter))
	for key := range diag.OmittedByFilter {
		filters = append(filters, key)
	}
	sort.Strings(filters)

	p.filterDiagMu.Lock()
	defer p.filterDiagMu.Unlock()
	if p.filterDiagNotes == nil {
		p.filterDiagNotes = make(map[string]filterDiagNote)
	}
	p.pruneFilterDiagNotesLocked(at)
	p.filterDiagNotes[sessionID] = filterDiagNote{filters: filters, at: at}
}

// consumeFilterDiagFollowUp reports whether THIS call is a follow-up that
// relaxed or dropped at least one filter the previous call's diagnostics block
// blamed. The note is consumed either way: a block gets exactly one chance to
// be followed, so a session that keeps re-running the same filtered query can
// never inflate the counter.
func (p *MCPProxyServer) consumeFilterDiagFollowUp(sessionID string, activeKeys []string, at time.Time) bool {
	if sessionID == "" {
		return false
	}

	p.filterDiagMu.Lock()
	defer p.filterDiagMu.Unlock()
	note, ok := p.filterDiagNotes[sessionID]
	if !ok {
		return false
	}
	delete(p.filterDiagNotes, sessionID)
	if at.Sub(note.at) > filterDiagNoteTTL {
		return false
	}

	active := make(map[string]struct{}, len(activeKeys))
	for _, k := range activeKeys {
		active[k] = struct{}{}
	}
	for _, blamed := range note.filters {
		if _, still := active[blamed]; !still {
			return true // a blamed filter was dropped → the agent acted on it
		}
	}
	return false
}

// pruneFilterDiagNotesLocked drops expired notes and, if the map is still at
// capacity, the oldest entries. Caller holds filterDiagMu.
func (p *MCPProxyServer) pruneFilterDiagNotesLocked(now time.Time) {
	for sid, note := range p.filterDiagNotes {
		if now.Sub(note.at) > filterDiagNoteTTL {
			delete(p.filterDiagNotes, sid)
		}
	}
	if len(p.filterDiagNotes) < maxFilterDiagNotes {
		return
	}
	type entry struct {
		sid string
		at  time.Time
	}
	entries := make([]entry, 0, len(p.filterDiagNotes))
	for sid, note := range p.filterDiagNotes {
		entries = append(entries, entry{sid, note.at})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	// Evict down to capacity-1 so the caller's insert stays within the bound.
	for i := 0; i <= len(entries)-maxFilterDiagNotes; i++ {
		delete(p.filterDiagNotes, entries[i].sid)
	}
}
