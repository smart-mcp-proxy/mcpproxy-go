// Package purgetest seeds and inspects the Spec 116 removal purge set
// (data-model.md §PurgeSet) for tests. It reads bbolt directly with its own
// key-match rules, independent of the production purge, so a test that uses
// it checks the purge rather than restating it.
package purgetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Bucket labels used in Counts.
const (
	Upstream        = "upstreams"
	Identities      = "server_identities"
	ToolCalls       = "tool_call_buckets"
	ToolApprovals   = "tool_approvals"
	PromptApprovals = "prompt_approvals"
	OAuthTokens     = "oauth_tokens"
	OAuthCompletion = "oauth_completion"
	Baselines       = "integrity_baselines"
	ScanReports     = "security_reports"
	ScanJobs        = "security_scan_jobs"
	ScanJobIndex    = "security_scan_job_index"
	ToolHash        = "toolhash"
	ToolBaseline    = "tool_baseline_decisions"
)

// OAuthKey returns a token key in the oauth.GenerateServerKey format
// (name + "_" + 16 lowercase hex) without importing the oauth package.
func OAuthKey(name string) string {
	return name + "_0123456789abcdef"
}

// Seed writes one record of every purge-set kind for name (not the upstream
// row, which the commit owns). Every record identifies the server only by
// name, as production does.
func Seed(t *testing.T, mgr *storage.Manager, name string) {
	t.Helper()
	now := time.Now().UTC()
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s for %s: %v", what, name, err)
		}
	}
	must(mgr.MarkToolBaselineDecided(name, toolreview.BaselineDecision{Source: toolreview.BaselineSourceServerApproval, By: "seed"}), "tool baseline marker")
	must(mgr.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: name, ToolName: "echo", Status: storage.ToolApprovalStatusApproved,
		ApprovedHash: "h1", CurrentHash: "h1", ApprovedAt: now, ApprovedBy: "seed",
	}), "tool approval")
	must(mgr.SavePromptApproval(&storage.PromptApprovalRecord{
		ServerName: name, PromptName: "greet", Status: "approved",
		ApprovedHash: "p1", CurrentHash: "p1", ApprovedAt: now, ApprovedBy: "seed",
	}), "prompt approval")
	must(mgr.SavePromptApproval(&storage.PromptApprovalRecord{
		ServerName: name, PromptName: "drift", Status: "changed",
		ApprovedHash: "p1", CurrentHash: "p2", ApprovedAt: now, ApprovedBy: "seed",
	}), "changed prompt approval")
	db := mgr.GetBoltDB()
	must(db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: OAuthKey(name), DisplayName: name, AccessToken: "at-" + name,
		RefreshToken: "rt-" + name, TokenType: "Bearer", ClientID: "cid-" + name,
		ClientSecret: "secret-" + name, Created: now, Updated: now,
	}), "oauth token")
	must(db.SaveOAuthCompletionEvent(&storage.OAuthCompletionEvent{ServerName: name, CompletedAt: now}), "oauth completion")
	must(mgr.SaveIntegrityBaseline(&scanner.IntegrityBaseline{ServerName: name, ApprovedAt: now, ApprovedBy: "seed"}), "baseline")
	jobID := fmt.Sprintf("scan-%s-%d", name, now.UnixNano())
	must(mgr.SaveScanJob(&scanner.ScanJob{ID: jobID, ServerName: name, Status: scanner.ScanJobStatusCompleted, ScanPass: 1, StartedAt: now, CompletedAt: now}), "scan job")
	must(mgr.SaveScanReport(&scanner.ScanReport{ID: "report-" + jobID, JobID: jobID, ServerName: name, ScannerID: "tpa-descriptions", ScannedAt: now}), "scan report")
	must(mgr.SaveToolHash(name+":echo", "h1"), "tool hash")
	identity, err := mgr.RegisterServerIdentity(&config.ServerConfig{Name: name, Protocol: "stdio", Command: "echo-" + name}, "/tmp/seed.json")
	must(err, "identity")
	must(mgr.RecordToolCall(&storage.ToolCallRecord{
		ID: "call-" + name, ServerID: identity.ID, ServerName: name, ToolName: "echo", Timestamp: now,
	}), "tool call")
}

// errStopScan ends a bucket scan early once a match is found.
var errStopScan = errors.New("stop scan")

var oauthSuffix = regexp.MustCompile(`^[0-9a-f]{16}$`)

func oauthKeyIsFor(key, name string) bool {
	if key == name {
		return true
	}
	rest, ok := strings.CutPrefix(key, name+"_")
	return ok && oauthSuffix.MatchString(rest)
}

// Counts returns, per purge-set bucket, how many keys belong to name.
func Counts(t *testing.T, mgr *storage.Manager, name string) map[string]int {
	t.Helper()
	out := map[string]int{}
	prefix := []byte(name + ":")
	err := mgr.GetDB().View(func(tx *bbolt.Tx) error {
		each := func(bucket string, fn func(k, v []byte)) {
			if b := tx.Bucket([]byte(bucket)); b != nil {
				_ = b.ForEach(func(k, v []byte) error { fn(k, v); return nil })
			}
		}
		each(storage.UpstreamsBucket, func(k, _ []byte) {
			if string(k) == name {
				out[Upstream]++
			}
		})
		ids := []string{name}
		each("server_identities", func(_, v []byte) {
			var id storage.ServerIdentity
			if json.Unmarshal(v, &id) == nil && id.ServerName == name {
				out[Identities]++
				ids = append(ids, id.ID)
			}
		})
		// History buckets are hash-keyed (server_<identity id>_tool_calls).
		// They are found independently of the surviving identity records
		// (Spec 116 PR2 review round 8): a removal that deleted the identity
		// but left its history bucket must still be reported. A bucket is
		// the server's when a surviving identity of name keys it, or when
		// any record in it names the server.
		idSet := map[string]bool{}
		for _, id := range ids {
			idSet[fmt.Sprintf("server_%s_tool_calls", id)] = true
		}
		_ = tx.ForEach(func(bucketName []byte, b *bbolt.Bucket) error {
			bn := string(bucketName)
			if !strings.HasPrefix(bn, "server_") || !strings.HasSuffix(bn, "_tool_calls") {
				return nil
			}
			if idSet[bn] {
				out[ToolCalls]++
				return nil
			}
			_ = b.ForEach(func(_, v []byte) error {
				var rec storage.ToolCallRecord
				if json.Unmarshal(v, &rec) == nil && rec.ServerName == name {
					out[ToolCalls]++
					return errStopScan
				}
				return nil
			})
			return nil
		})
		each(storage.ToolApprovalBucket, func(k, _ []byte) {
			if bytes.HasPrefix(k, prefix) {
				out[ToolApprovals]++
			}
		})
		each(storage.PromptApprovalBucket, func(k, _ []byte) {
			if bytes.HasPrefix(k, prefix) {
				out[PromptApprovals]++
			}
		})
		each(storage.OAuthTokenBucket, func(k, _ []byte) {
			if oauthKeyIsFor(string(k), name) {
				out[OAuthTokens]++
			}
		})
		each(storage.OAuthCompletionBucket, func(_, v []byte) {
			var ev storage.OAuthCompletionEvent
			if json.Unmarshal(v, &ev) == nil && ev.ServerName == name {
				out[OAuthCompletion]++
			}
		})
		each(storage.IntegrityBaselinesBucket, func(k, _ []byte) {
			if string(k) == name {
				out[Baselines]++
			}
		})
		each(storage.ScanReportsBucket, func(_, v []byte) {
			var r scanner.ScanReport
			if json.Unmarshal(v, &r) == nil && r.ServerName == name {
				out[ScanReports]++
			}
		})
		each(storage.ScanJobsBucket, func(_, v []byte) {
			var j scanner.ScanJob
			if json.Unmarshal(v, &j) == nil && j.ServerName == name {
				out[ScanJobs]++
			}
		})
		each(storage.ScanJobIndexBucket, func(_, v []byte) {
			var m scanner.ScanJobMeta
			if json.Unmarshal(v, &m) == nil && m.ServerName == name {
				out[ScanJobIndex]++
			}
		})
		each(storage.ToolHashBucket, func(k, _ []byte) {
			if bytes.HasPrefix(k, prefix) {
				out[ToolHash]++
			}
		})
		each(storage.ToolBaselineDecisionsBucket, func(k, _ []byte) {
			if string(k) == name {
				out[ToolBaseline]++
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("count purge set for %s: %v", name, err)
	}
	return out
}

// AssertPurged fails the test for every purge-set bucket that still holds a
// key of name.
func AssertPurged(t *testing.T, mgr *storage.Manager, name, path string) {
	t.Helper()
	for bucket, n := range Counts(t, mgr, name) {
		if n != 0 {
			t.Errorf("%s: %d key(s) of %q left in %s after removal", path, n, name, bucket)
		}
	}
}

// AssertSeeded fails the test unless every seeded bucket holds at least one
// key of name (the fixture precondition).
func AssertSeeded(t *testing.T, mgr *storage.Manager, name string) {
	t.Helper()
	c := Counts(t, mgr, name)
	for _, bucket := range []string{Identities, ToolCalls, ToolApprovals, PromptApprovals, OAuthTokens,
		OAuthCompletion, Baselines, ScanReports, ScanJobs, ScanJobIndex, ToolHash, ToolBaseline} {
		if c[bucket] == 0 {
			t.Fatalf("fixture: %s has no key of %q", bucket, name)
		}
	}
}
