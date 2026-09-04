package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.etcd.io/bbolt"
)

const (
	// DefaultToolCallMaxResponseSize bounds the marshalled response persisted
	// on a single per-server tool-call record, in bytes. It matches the
	// activity log's DefaultMaxResponseSize so the two record types that
	// mirror the same call are bounded the same way.
	DefaultToolCallMaxResponseSize = 64 * 1024

	// DefaultToolCallMaxRecordsPerServer bounds how many call records a single
	// server_<id>_tool_calls bucket retains. The store is a debugging history,
	// not an audit log — the activity log is the durable record — so it keeps
	// a recent window rather than everything.
	DefaultToolCallMaxRecordsPerServer = 1000

	// toolCallPreviewBytes is how much of an oversized response survives as a
	// readable head on the stored record.
	toolCallPreviewBytes = 2048

	// serverIdentitiesBucket holds the ServerIdentity records that map a
	// server NAME to the SHA-256 id its tool-call bucket is keyed by.
	serverIdentitiesBucket = "server_identities"

	// codeExecutionServerID is the synthetic server id under which the
	// code_execution built-in records its parent call. It has a real bucket
	// but no entry in server_identities, so every sweep keyed on configured
	// servers must whitelist it or it deletes the parent of every recorded
	// script run.
	codeExecutionServerID = "code_execution"
)

// toolCallsBucketName is the single place the per-server bucket name is built.
func toolCallsBucketName(serverID string) string {
	return fmt.Sprintf("server_%s_tool_calls", serverID)
}

// SetToolCallLimits configures the per-record response cap (in bytes of
// marshalled JSON) and the per-server record count for the tool-call history.
//
// Non-positive values are ignored, leaving the current limit in place, so a
// config that omits the keys cannot silently restore the unbounded growth of
// #1176. This mirrors ActivityService.SetMaxResponseSize.
func (m *Manager) SetToolCallLimits(maxResponseBytes, maxRecordsPerServer int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if maxResponseBytes > 0 {
		m.toolCallMaxResponseBytes = maxResponseBytes
	}
	if maxRecordsPerServer > 0 {
		m.toolCallMaxRecords = maxRecordsPerServer
	}
}

// toolCallLimits reads the effective limits, falling back to the documented
// defaults for a Manager built before SetToolCallLimits ran (or by a test).
func (m *Manager) toolCallLimits() (maxResponseBytes, maxRecords int) {
	maxResponseBytes = m.toolCallMaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultToolCallMaxResponseSize
	}
	maxRecords = m.toolCallMaxRecords
	if maxRecords <= 0 {
		maxRecords = DefaultToolCallMaxRecordsPerServer
	}
	return maxResponseBytes, maxRecords
}

// boundToolCallResponse returns a copy of record whose Response is bounded to
// maxBytes of marshalled JSON, or record itself when it already fits.
//
// The caller keeps its own record after RecordToolCall returns — mcp.go reuses
// it for token accounting and code_execution links children to the parent — so
// this never writes through the pointer it was given.
//
// Response stays a JSON OBJECT. contracts.ToolCallRecord.Response is a
// documented REST payload on four endpoints (GET /api/v1/tool-calls and
// friends) and httpapi.maskToolCallRecord walks it as structured JSON, so
// replacing it with a bare string would be a breaking API change.
func boundToolCallResponse(record *ToolCallRecord, maxBytes int) *ToolCallRecord {
	if record == nil {
		return record
	}

	bounded := record
	if args := boundToolCallArguments(record.Arguments, maxBytes); args != nil {
		copied := *record
		copied.Arguments = args
		bounded = &copied
	}
	if record.Response == nil {
		return bounded
	}

	encoded, err := json.Marshal(record.Response)
	if err != nil {
		// An unmarshallable response would fail the record write below anyway;
		// leave it for that path to report rather than guessing at its size.
		return bounded
	}
	if len(encoded) <= maxBytes {
		return bounded
	}

	if bounded == record {
		copied := *record
		bounded = &copied
	}
	bounded.Response = map[string]interface{}{
		"truncated":      true,
		"original_bytes": len(encoded),
		"preview":        truncateUTF8(string(encoded), toolCallPreviewBytes),
		"note": "response omitted: exceeded tool_call_max_response_size. " +
			"The full response was delivered to the caller; only this stored copy is shortened.",
	}
	bounded.ResponseTruncated = true
	bounded.ResponseBytes = int64(len(encoded))
	return bounded
}

// boundToolCallArguments caps the stored arguments the same way, returning nil
// when they already fit.
//
// A tool called with a megabyte of input persists that megabyte too — the
// echo-shaped case makes the record twice as big as the response cap allows.
// Unlike the activity log this store feeds no detector (sensitive-data
// scanning runs on the activity path, which keeps its own copy), so shortening
// here narrows nothing.
func boundToolCallArguments(args map[string]interface{}, maxBytes int) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) <= maxBytes {
		return nil
	}
	return map[string]interface{}{
		"truncated":      true,
		"original_bytes": len(encoded),
		"preview":        truncateUTF8(string(encoded), toolCallPreviewBytes),
		"note": "arguments omitted: exceeded tool_call_max_response_size. " +
			"The full arguments were sent to the upstream server; only this stored copy is shortened.",
	}
}

// truncateUTF8 cuts s to at most maxBytes without splitting a rune, so the
// preview stays valid JSON text.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[truncated]"
}

// pruneToolCallBucket evicts the oldest records until at most maxRecords
// remain. Keys are "<unix nanos>_<id>", so cursor order is chronological and
// the oldest are at the front.
//
// It walks BACKWARDS from the newest key and stops after maxRecords+1 steps,
// so the cost of the common case (a bucket already at its cap) is bounded by
// the cap rather than by the bucket size. Bucket.Stats().KeyN is deliberately
// not used: inside the same write transaction it does not count the Put that
// just happened, which leaves the bucket one record over the cap forever.
func pruneToolCallBucket(bucket *bbolt.Bucket, maxRecords int) error {
	cursor := bucket.Cursor()

	kept := 0
	k, _ := cursor.Last()
	for k != nil && kept < maxRecords {
		kept++
		k, _ = cursor.Prev()
	}
	if k == nil {
		return nil
	}

	// Everything from here to the front is excess. Collect first, then delete:
	// mutating through the cursor while stepping it is not safe in bbolt.
	var doomed [][]byte
	for ; k != nil; k, _ = cursor.Prev() {
		doomed = append(doomed, append([]byte(nil), k...))
	}
	for _, key := range doomed {
		if err := bucket.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

// deleteToolCallBucketTx drops a server's whole call history inside tx.
// Reports whether a bucket was actually removed.
func deleteToolCallBucketTx(tx *bbolt.Tx, serverID string) (bool, error) {
	name := []byte(toolCallsBucketName(serverID))
	if tx.Bucket(name) == nil {
		return false, nil
	}
	if err := tx.DeleteBucket(name); err != nil {
		return false, err
	}
	return true, nil
}

// PruneOrphanToolCalls drops per-server tool-call history for servers that are
// no longer configured, mirroring PruneOrphanToolApprovals. It takes server
// NAMES, as its sibling does, and returns how many server histories it removed.
//
// The translation in the middle is the whole point. These buckets are keyed by
// SERVER IDENTITY — the SHA-256 GenerateServerID computes from a server's
// stable attributes (mcp.go passes serverID, not serverName) — so comparing a
// bucket's key against configured NAMES marks every live history an orphan and
// deletes the lot. The names are resolved through server_identities first.
//
// Two guards on top of that:
//   - the synthetic code_execution bucket is never an orphan; it holds the
//     parent record of every code_execution run and has no identity at all;
//   - an empty server_identities bucket aborts the sweep. That state means the
//     identity store has not been populated yet, not that every server is gone.
func (m *Manager) PruneOrphanToolCalls(configuredServers []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	configured := make(map[string]bool, len(configuredServers))
	for _, name := range configuredServers {
		configured[name] = true
	}

	removed := 0
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		keep, identitiesKnown, err := liveToolCallOwners(tx, configured)
		if err != nil {
			return err
		}
		if !identitiesKnown {
			// No identities recorded: we cannot tell a live history from a
			// dead one, and guessing deletes real data.
			return nil
		}

		var orphans [][]byte
		if err := tx.ForEach(func(name []byte, _ *bbolt.Bucket) error {
			serverID, ok := serverIDFromToolCallsBucket(string(name))
			if !ok || keep[serverID] {
				return nil
			}
			orphans = append(orphans, append([]byte(nil), name...))
			return nil
		}); err != nil {
			return err
		}

		for _, name := range orphans {
			if err := tx.DeleteBucket(name); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to prune orphan tool calls: %w", err)
	}
	return removed, nil
}

// liveToolCallOwners returns the set of bucket keys whose history must be kept,
// and whether the identity store had anything to say. A configured server is
// kept under every identity it has ever had (its attributes change over time,
// and each variant hashes to a different id) and under its bare name, which is
// what a caller that never registered an identity would have written.
func liveToolCallOwners(tx *bbolt.Tx, configured map[string]bool) (keep map[string]bool, identitiesKnown bool, err error) {
	keep = map[string]bool{codeExecutionServerID: true}
	for name := range configured {
		keep[name] = true
	}

	bucket := tx.Bucket([]byte(serverIdentitiesBucket))
	if bucket == nil {
		return keep, false, nil
	}

	err = bucket.ForEach(func(_, value []byte) error {
		var identity ServerIdentity
		if uerr := json.Unmarshal(value, &identity); uerr != nil {
			// An unreadable identity is a reason to keep data, not delete it.
			identitiesKnown = true
			return nil
		}
		identitiesKnown = true
		if configured[identity.ServerName] {
			keep[identity.ID] = true
		}
		return nil
	})
	return keep, identitiesKnown, err
}

// serverIDFromToolCallsBucket extracts the server id from a
// "server_<id>_tool_calls" bucket name. Server ids may themselves contain
// underscores, so the match is on the fixed prefix and suffix rather than a
// split.
func serverIDFromToolCallsBucket(name string) (string, bool) {
	const prefix = "server_"
	const suffix = "_tool_calls"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	id := name[len(prefix) : len(name)-len(suffix)]
	if id == "" {
		return "", false
	}
	return id, true
}

// deleteToolCallHistoryForName drops every tool-call bucket belonging to a
// server name: one per identity the name has held, plus a name-keyed bucket
// for any caller that recorded without registering an identity.
func (m *Manager) deleteToolCallHistoryForName(name string) error {
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		ids := []string{name}

		if bucket := tx.Bucket([]byte(serverIdentitiesBucket)); bucket != nil {
			if err := bucket.ForEach(func(_, value []byte) error {
				var identity ServerIdentity
				if err := json.Unmarshal(value, &identity); err != nil {
					return nil //nolint:nilerr // an unreadable identity is not a reason to fail a delete
				}
				if identity.ServerName == name {
					ids = append(ids, identity.ID)
				}
				return nil
			}); err != nil {
				return err
			}
		}

		for _, id := range ids {
			if _, err := deleteToolCallBucketTx(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
