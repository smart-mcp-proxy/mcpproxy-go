package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 116 PR2: the removal purge set (data-model.md §PurgeSet).
//
// A server's security state is keyed by its name, so a server removed and
// later re-added under the same name would inherit it: approved tool and
// prompt hashes (a rug-pull baseline the new server never earned), OAuth
// tokens and DCR client credentials, a clean scan verdict and the integrity
// baseline that lets scanner approval unquarantine it (#1562). The config
// commit therefore deletes all of it in the same bbolt transaction as the
// upstream row: a removal either commits together with its purge or not at
// all, and a later re-add starts with nothing.
//
// Not purged (A5): activity records (audit history), agent tokens, client
// credentials and toolstats (aggregate counters, not trust), and the Spec 101
// TPA bundle state (TPABundleStateBucket): it is installation-wide anti-downgrade
// state keyed by signing authority, not by server, so removing a server must
// never reset a watermark, pin or deny-list.

// Purge-set report keys that are not bucket names.
const (
	// PurgeKeyToolCallBuckets counts the per-identity tool-call buckets
	// dropped for the server.
	PurgeKeyToolCallBuckets = "tool_call_buckets"
	// PurgeKeyServerIdentities counts the server identities dropped.
	PurgeKeyServerIdentities = serverIdentitiesBucket
)

// PurgeReport lists what one server's purge deleted, per bucket.
type PurgeReport struct {
	Server  string         `json:"server"`
	Deleted map[string]int `json:"deleted"`
}

// Total returns the number of keys the purge deleted.
func (r PurgeReport) Total() int {
	n := 0
	for _, c := range r.Deleted {
		n += c
	}
	return n
}

// purgeServerTx deletes the whole purge set of name inside the caller's
// transaction. Every key match is exact for the bucket's key format:
//
//   - upstreams, integrity_baselines: key == name
//   - tool_approvals, prompt_approvals, toolhash: key prefix "name:" (server
//     names cannot contain ':', config validation; toolhash keys are
//     "server:tool", T046)
//   - oauth_tokens: key == name (legacy) or name + "_" + 16 lowercase hex
//     (oauth.GenerateServerKey); never another server whose name merely
//     starts with "name_" (B2)
//   - oauth_completion, security_reports, security_scan_jobs (+ index): the
//     decoded record's server name (their keys are not name-prefixed
//     safely: "name_<unix>" and "scan-name-<nanos>")
//   - server identities whose name is name, and the tool-call bucket of each
//     (plus the name-keyed bucket of a caller that recorded without an
//     identity)
func purgeServerTx(tx *bbolt.Tx, name string) (PurgeReport, error) {
	rep := PurgeReport{Server: name, Deleted: map[string]int{}}
	note := func(bucket string, n int) {
		if n > 0 {
			rep.Deleted[bucket] += n
		}
	}

	if b := tx.Bucket([]byte(UpstreamsBucket)); b != nil && b.Get([]byte(name)) != nil {
		if err := b.Delete([]byte(name)); err != nil {
			return rep, err
		}
		note(UpstreamsBucket, 1)
	}

	identities, buckets, err := purgeToolCallHistoryTx(tx, name)
	if err != nil {
		return rep, fmt.Errorf("tool-call history: %w", err)
	}
	note(PurgeKeyServerIdentities, identities)
	note(PurgeKeyToolCallBuckets, buckets)

	prefix := []byte(name + ":")
	for _, bucket := range []string{ToolApprovalBucket, PromptApprovalBucket, ToolHashBucket} {
		n, err := deleteKeysTx(tx, bucket, func(k, _ []byte) bool { return bytes.HasPrefix(k, prefix) })
		if err != nil {
			return rep, fmt.Errorf("%s: %w", bucket, err)
		}
		note(bucket, n)
	}

	// Spec 117 PR-1: the durable tool baseline marker goes with the records
	// it describes, so a re-added server starts as new.
	if b := tx.Bucket([]byte(ToolBaselineDecisionsBucket)); b != nil && b.Get([]byte(name)) != nil {
		if err := deleteToolBaselineDecisionsTx(tx, []string{name}, nil); err != nil {
			return rep, fmt.Errorf("%s: %w", ToolBaselineDecisionsBucket, err)
		}
		note(ToolBaselineDecisionsBucket, 1)
	}

	if b := tx.Bucket([]byte(OAuthTokenBucket)); b != nil {
		n, err := clearOAuthStateTx(b, name)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", OAuthTokenBucket, err)
		}
		note(OAuthTokenBucket, n)
	}

	n, err := deleteKeysTx(tx, OAuthCompletionBucket, func(_, v []byte) bool {
		var ev OAuthCompletionEvent
		return json.Unmarshal(v, &ev) == nil && ev.ServerName == name
	})
	if err != nil {
		return rep, fmt.Errorf("%s: %w", OAuthCompletionBucket, err)
	}
	note(OAuthCompletionBucket, n)

	if b := tx.Bucket([]byte(IntegrityBaselinesBucket)); b != nil && b.Get([]byte(name)) != nil {
		if err := b.Delete([]byte(name)); err != nil {
			return rep, err
		}
		note(IntegrityBaselinesBucket, 1)
	}

	n, err = deleteKeysTx(tx, ScanReportsBucket, func(_, v []byte) bool {
		var r scanner.ScanReport
		return json.Unmarshal(v, &r) == nil && r.ServerName == name
	})
	if err != nil {
		return rep, fmt.Errorf("%s: %w", ScanReportsBucket, err)
	}
	note(ScanReportsBucket, n)

	var jobIDs [][]byte
	n, err = deleteKeysTx(tx, ScanJobsBucket, func(k, v []byte) bool {
		var j scanner.ScanJob
		if json.Unmarshal(v, &j) == nil && j.ServerName == name {
			jobIDs = append(jobIDs, append([]byte(nil), k...))
			return true
		}
		return false
	})
	if err != nil {
		return rep, fmt.Errorf("%s: %w", ScanJobsBucket, err)
	}
	note(ScanJobsBucket, n)
	// The index is keyed by job id; it also drops entries whose job record
	// is already gone but whose meta names the server.
	n, err = deleteKeysTx(tx, ScanJobIndexBucket, func(k, v []byte) bool {
		for _, id := range jobIDs {
			if bytes.Equal(k, id) {
				return true
			}
		}
		var m scanner.ScanJobMeta
		return json.Unmarshal(v, &m) == nil && m.ServerName == name
	})
	if err != nil {
		return rep, fmt.Errorf("%s: %w", ScanJobIndexBucket, err)
	}
	note(ScanJobIndexBucket, n)

	return rep, nil
}

// deleteKeysTx deletes every key of bucket for which match is true. The keys
// are collected before any delete (bbolt cursors are invalidated by mutation
// during iteration). A missing bucket deletes nothing.
func deleteKeysTx(tx *bbolt.Tx, bucket string, match func(k, v []byte) bool) (int, error) {
	b := tx.Bucket([]byte(bucket))
	if b == nil {
		return 0, nil
	}
	var keys [][]byte
	if err := b.ForEach(func(k, v []byte) error {
		if v != nil && match(k, v) {
			keys = append(keys, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

// purgeToolCallHistoryTx drops every server identity whose name is name, the
// tool-call bucket of each, and the name-keyed bucket of a caller that
// recorded without an identity. It returns the identities and buckets
// removed.
func purgeToolCallHistoryTx(tx *bbolt.Tx, name string) (identities, buckets int, err error) {
	ids := []string{name}
	var identityKeys [][]byte
	if b := tx.Bucket([]byte(serverIdentitiesBucket)); b != nil {
		if err := b.ForEach(func(k, v []byte) error {
			var identity ServerIdentity
			if json.Unmarshal(v, &identity) != nil {
				return nil // unreadable: cannot prove it is this server's
			}
			if identity.ServerName == name {
				ids = append(ids, identity.ID)
				identityKeys = append(identityKeys, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return 0, 0, err
		}
		for _, k := range identityKeys {
			if err := b.Delete(k); err != nil {
				return 0, 0, err
			}
		}
	}
	for _, id := range ids {
		dropped, err := deleteToolCallBucketTx(tx, id)
		if err != nil {
			return len(identityKeys), buckets, err
		}
		if dropped {
			buckets++
		}
	}
	return len(identityKeys), buckets, nil
}

// PurgeSetCounts reports, per purge-set bucket, how many keys still belong to
// name, with the same key matching as the purge. It is read-only; the debug
// purge-report command and the live checks use it (quickstart L2).
func (m *Manager) PurgeSetCounts(name string) (map[string]int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]int{}
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		// A rolled-back purge in a read-only view would fail on the first
		// delete, so count with the same predicates instead.
		count := func(bucket string, match func(k, v []byte) bool) {
			b := tx.Bucket([]byte(bucket))
			if b == nil {
				return
			}
			_ = b.ForEach(func(k, v []byte) error {
				if v != nil && match(k, v) {
					out[bucket]++
				}
				return nil
			})
		}
		exact := func(k, _ []byte) bool { return string(k) == name }
		prefix := []byte(name + ":")
		hasPrefix := func(k, _ []byte) bool { return bytes.HasPrefix(k, prefix) }
		count(UpstreamsBucket, exact)
		count(IntegrityBaselinesBucket, exact)
		count(ToolBaselineDecisionsBucket, exact)
		count(ToolApprovalBucket, hasPrefix)
		count(PromptApprovalBucket, hasPrefix)
		count(ToolHashBucket, hasPrefix)
		count(OAuthTokenBucket, func(k, _ []byte) bool { return oauthKeyBelongsTo(string(k), name) })
		count(OAuthCompletionBucket, func(_, v []byte) bool {
			var ev OAuthCompletionEvent
			return json.Unmarshal(v, &ev) == nil && ev.ServerName == name
		})
		count(ScanReportsBucket, func(_, v []byte) bool {
			var r scanner.ScanReport
			return json.Unmarshal(v, &r) == nil && r.ServerName == name
		})
		count(ScanJobsBucket, func(_, v []byte) bool {
			var j scanner.ScanJob
			return json.Unmarshal(v, &j) == nil && j.ServerName == name
		})
		count(ScanJobIndexBucket, func(_, v []byte) bool {
			var meta scanner.ScanJobMeta
			return json.Unmarshal(v, &meta) == nil && meta.ServerName == name
		})
		ids := []string{name}
		count(serverIdentitiesBucket, func(_, v []byte) bool {
			var identity ServerIdentity
			if json.Unmarshal(v, &identity) == nil && identity.ServerName == name {
				ids = append(ids, identity.ID)
				return true
			}
			return false
		})
		for _, id := range ids {
			if tx.Bucket([]byte(toolCallsBucketName(id))) != nil {
				out[PurgeKeyToolCallBuckets]++
			}
		}
		return nil
	})
	return out, err
}

// sortedPurgeNames returns the deduplicated, sorted names a batch purges.
func sortedPurgeNames(b *ServerCommitBatch) []string {
	seen := make(map[string]bool, len(b.Removes)+len(b.Replaces))
	var out []string
	for _, list := range [][]string{b.Removes, b.Replaces} {
		for _, n := range list {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}
