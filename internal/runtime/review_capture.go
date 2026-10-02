package runtime

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// ShouldCaptureReviewDefinitionsAfterScan reports whether a just-settled scan
// should be followed by an automatic capture of the server's tool definitions
// for review (Spec 109 fix-review-screen, D-4). It is true only when the server
// is still quarantined, has no approval records yet, and its newest baseline
// job completed after exporting at least one tool definition.
//
// The tool-export gate is the safety property: it means MCPProxy already
// started the quarantined upstream and listed its tools for that scan, so the
// follow-up capture (the inspection-only RefreshServerTools path: no indexing,
// no tool routing) adds no new trust exposure. With automatic baseline scans
// off and no manual scan, no job exists and nothing is started.
func (r *Runtime) ShouldCaptureReviewDefinitionsAfterScan(serverName string) bool {
	if serverName == "" || r.storageManager == nil {
		return false
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil || len(records) > 0 {
		return false
	}
	metas, err := r.storageManager.ListScanJobMetas(serverName)
	if err != nil {
		return false
	}
	var newest *scanner.ScanJobMeta
	var newestStart time.Time
	for _, meta := range metas {
		if meta == nil || (meta.ScanPass != scanner.ScanPassSecurityScan && meta.ScanPass != 0) {
			continue
		}
		if newest == nil || meta.StartedAt.After(newestStart) {
			newest, newestStart = meta, meta.StartedAt
		}
	}
	if newest == nil || newest.Status != scanner.ScanJobStatusCompleted {
		return false
	}
	job, err := r.storageManager.GetScanJob(newest.ID)
	if err != nil || job == nil || job.Status != scanner.ScanJobStatusCompleted {
		return false
	}
	if job.ScanContext == nil || job.ScanContext.ToolsExported == 0 {
		return false
	}
	// The configuration read comes last: it is the one check that depends on
	// the live snapshot, and the cheap storage gates above reject almost every
	// settle event first.
	return r.serverIsQuarantined(serverName)
}
