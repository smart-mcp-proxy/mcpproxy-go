package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

var ErrReviewServerNotFound = errors.New("review server not found")

// ReviewQueue is the composed, server-level review list shared by REST, CLI,
// MCP and native clients.
type ReviewQueue struct {
	Count   int              `json:"count"`
	Servers []ReviewQueueRow `json:"servers"`
}

type ReviewQueueRow struct {
	Server        string                 `json:"server"`
	Kind          string                 `json:"kind"`
	Quarantined   bool                   `json:"quarantined"`
	ToolsCaptured int                    `json:"tools_captured,omitempty"`
	TierCounts    map[contracts.Tier]int `json:"tier_counts,omitempty"`
	Pending       int                    `json:"pending,omitempty"`
	Changed       int                    `json:"changed,omitempty"`
	Scan          *ReviewScan            `json:"scan,omitempty"`
	Since         *time.Time             `json:"since,omitempty"`
}

// Review scan coverage values (ReviewScan.Coverage). They say whether the
// latest scan verdict describes the definitions an operator is looking at.
// Precedence when several apply: not_captured > scanning > none >
// tools_not_scanned > stale > current.
const (
	// ReviewScanCoverageCurrent: the latest completed scan analysed every
	// captured definition as it is now.
	ReviewScanCoverageCurrent = "current"
	// ReviewScanCoverageStale: at least one captured definition was added or
	// changed after that scan.
	ReviewScanCoverageStale = "stale"
	// ReviewScanCoverageNotCaptured: no definitions are captured for review.
	ReviewScanCoverageNotCaptured = "not_captured"
	// ReviewScanCoverageToolsNotScanned: the scan completed but exported no
	// tool definitions (a source-only or URL scan).
	ReviewScanCoverageToolsNotScanned = "tools_not_scanned"
	// ReviewScanCoverageScanning: the newest scan job is pending or running.
	ReviewScanCoverageScanning = "scanning"
	// ReviewScanCoverageNone: there is no completed scan (never scanned, or
	// the newest job failed or was cancelled).
	ReviewScanCoverageNone = "none"
)

type ReviewScan struct {
	Verdict   string     `json:"verdict"`
	RiskScore int        `json:"risk_score"`
	ReportID  string     `json:"report_id,omitempty"`
	ScannedAt *time.Time `json:"scanned_at,omitempty"`
	// Coverage is always present; see the ReviewScanCoverage constants.
	Coverage string `json:"coverage"`
	// ToolsScanned is the number of tool definitions the covering job exported.
	ToolsScanned int `json:"tools_scanned,omitempty"`
	// UnscannedTools lists, sorted, the captured tools whose current
	// definition the scan did not cover. Set only when Coverage is "stale".
	UnscannedTools []string `json:"unscanned_tools,omitempty"`
}

type ServerReview struct {
	Server ReviewServer `json:"server"`
	Tools  []ReviewTool `json:"tools"`
}

type ReviewServer struct {
	Name                     string            `json:"name"`
	Transport                string            `json:"transport"`
	Command                  string            `json:"command,omitempty"`
	Args                     []string          `json:"args,omitempty"`
	WorkingDir               string            `json:"working_dir,omitempty"`
	URL                      string            `json:"url,omitempty"`
	Env                      map[string]string `json:"env,omitempty"`
	Headers                  map[string]string `json:"headers,omitempty"`
	Quarantined              bool              `json:"quarantined"`
	TrustMode                string            `json:"trust_mode"`
	SourceRegistryID         string            `json:"source_registry_id,omitempty"`
	SourceRegistryProvenance string            `json:"source_registry_provenance,omitempty"`
	Scan                     *ReviewScan       `json:"scan,omitempty"`
	DefinitionsCaptured      bool              `json:"definitions_captured"`
}

type ReviewTool struct {
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	InputSchema    json.RawMessage         `json:"input_schema"`
	OutputSchema   json.RawMessage         `json:"output_schema"`
	Annotations    *config.ToolAnnotations `json:"annotations"`
	Tier           contracts.Tier          `json:"tier"`
	ApprovalStatus string                  `json:"approval_status"`
	Disabled       bool                    `json:"disabled"`
	ScanVerdict    string                  `json:"scan_verdict"`
	HeldReason     string                  `json:"held_reason"`
	HeldSignals    []string                `json:"held_signals"`
	// CurrentHash identifies the definition shown above (the approval
	// record's current_hash). Clients send it back as expected_hashes so an
	// approval applies only to the definition the operator reviewed (UX-02).
	CurrentHash string `json:"current_hash,omitempty"`
	// DefaultAllowed is the review screens' fail-closed default selection
	// (D43). Always serialised, so an older core (field absent) reads as false.
	DefaultAllowed bool                `json:"default_allowed"`
	Previous       *ReviewToolPrevious `json:"previous"`
	Diff           *ReviewToolDiff     `json:"diff,omitempty"`
}

type ReviewToolPrevious struct {
	Description  string                  `json:"description"`
	InputSchema  json.RawMessage         `json:"input_schema"`
	OutputSchema json.RawMessage         `json:"output_schema"`
	Annotations  *config.ToolAnnotations `json:"annotations"`
}

type ReviewToolDiff struct {
	Description  string `json:"description"`
	InputSchema  string `json:"input_schema"`
	OutputSchema string `json:"output_schema"`
	Annotations  string `json:"annotations"`
}

// GetReviewQueue composes one row for each quarantined server and each
// trusted server with pending or changed tool definitions.
func (r *Runtime) GetReviewQueue(ctx context.Context) (*ReviewQueue, error) {
	cfg, err := r.GetConfig()
	if err != nil {
		return nil, err
	}
	queue := &ReviewQueue{Servers: make([]ReviewQueueRow, 0)}
	for i := range cfg.Servers {
		server := cfg.Servers[i]
		if server == nil {
			continue
		}
		records, err := r.storageManager.ListToolApprovals(server.Name)
		if err != nil {
			return nil, fmt.Errorf("list tool reviews for %q: %w", server.Name, err)
		}
		row := ReviewQueueRow{Server: server.Name, Quarantined: server.Quarantined}
		if server.Quarantined {
			row.ToolsCaptured = len(records)
		}
		row.TierCounts = make(map[contracts.Tier]int, 5)
		for _, tier := range []contracts.Tier{contracts.TierRead, contracts.TierWrite, contracts.TierDestructive, contracts.TierUnannotated, contracts.TierUnknown} {
			row.TierCounts[tier] = 0
		}
		for _, record := range records {
			row.TierCounts[reviewTier(record.CurrentAnnotations)]++
			switch record.Status {
			case storage.ToolApprovalStatusPending:
				row.Pending++
			case storage.ToolApprovalStatusChanged:
				row.Changed++
			}
			if (record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged) && !record.ApprovedAt.IsZero() && (row.Since == nil || record.ApprovedAt.Before(*row.Since)) {
				since := record.ApprovedAt
				row.Since = &since
			}
		}
		if !server.Quarantined && row.Pending+row.Changed == 0 {
			continue
		}
		if server.Quarantined {
			row.Kind = "server_review"
		} else {
			row.Kind = "tool_review"
			row.TierCounts = nil
		}
		if server.Quarantined {
			row.Scan, _, _ = r.reviewScanFor(ctx, server.Name, true, records)
		}
		queue.Servers = append(queue.Servers, row)
	}
	sort.Slice(queue.Servers, func(i, j int) bool { return queue.Servers[i].Server < queue.Servers[j].Server })
	queue.Count = len(queue.Servers)
	return queue, nil
}

// GetServerReview returns a caller-independent review view. Server command,
// URL, environment, header, and argument values are redacted unconditionally.
func (r *Runtime) GetServerReview(ctx context.Context, serverName string) (*ServerReview, error) {
	cfg, err := r.GetConfig()
	if err != nil {
		return nil, err
	}
	var server *config.ServerConfig
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == serverName {
			server = cfg.Servers[i]
			break
		}
	}
	if server == nil {
		return nil, fmt.Errorf("%w: %q", ErrReviewServerNotFound, serverName)
	}
	contractServer := contracts.Server{
		Name: server.Name, Protocol: server.Protocol, Command: server.Command,
		Args: append([]string(nil), server.Args...), WorkingDir: server.WorkingDir,
		URL: server.URL, Env: copyStringMap(server.Env), Headers: copyStringMap(server.Headers),
	}
	oauth.RedactServerSecretFields(&contractServer)
	reviewServer := ReviewServer{
		Name: contractServer.Name, Transport: reviewTransport(server.Protocol),
		Command: contractServer.Command, Args: contractServer.Args, WorkingDir: contractServer.WorkingDir,
		URL: contractServer.URL, Env: contractServer.Env, Headers: contractServer.Headers,
		Quarantined: server.Quarantined, TrustMode: string(server.EffectiveTrustMode()),
		SourceRegistryID: server.SourceRegistryID, SourceRegistryProvenance: server.SourceRegistryProvenance,
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return nil, fmt.Errorf("list tool reviews for %q: %w", serverName, err)
	}
	reviewServer.DefinitionsCaptured = len(records) > 0
	reviewScan, scanFindings, covered := r.reviewScanFor(ctx, serverName, server.Quarantined, records)
	reviewServer.Scan = reviewScan
	result := &ServerReview{Server: reviewServer, Tools: make([]ReviewTool, 0, len(records))}
	if !reviewServer.DefinitionsCaptured {
		return result, nil
	}
	for _, record := range records {
		tool := ReviewTool{
			Name: record.ToolName, Description: record.CurrentDescription,
			InputSchema: rawSchema(record.CurrentSchema), OutputSchema: rawSchema(record.CurrentOutputSchema),
			Annotations: cloneToolAnnotations(record.CurrentAnnotations), Tier: reviewTier(record.CurrentAnnotations),
			ApprovalStatus: record.Status, Disabled: record.Disabled,
			ScanVerdict: reviewToolScanVerdict(scanFindings, serverName, record, covered[record.ToolName]),
			HeldReason:  record.HeldReason, HeldSignals: append([]string(nil), record.HeldSignals...),
			CurrentHash: record.CurrentHash,
		}
		tool.DefaultAllowed = reviewDefaultAllowed(tool)
		if record.PreviousDescription != "" || record.PreviousSchema != "" || record.PreviousOutputSchema != "" || record.PreviousAnnotations != nil {
			tool.Previous = &ReviewToolPrevious{
				Description: record.PreviousDescription, InputSchema: rawSchema(record.PreviousSchema),
				OutputSchema: rawSchema(record.PreviousOutputSchema), Annotations: cloneToolAnnotations(record.PreviousAnnotations),
			}
		}
		if tool.Previous != nil {
			tool.Diff = buildReviewDiff(*tool.Previous, tool)
		}
		result.Tools = append(result.Tools, tool)
	}
	return result, nil
}

// reviewDefaultAllowed is the default selection of the review screens (D43.2).
// An already blocked tool stays blocked; an approved tool stays allowed; a
// pending or changed tool starts allowed only when it is read-only, the scan
// verified its current definition as clean and nothing holds it. Everything
// else (write, destructive, unannotated, unknown, not scanned, warnings,
// dangerous, held) starts unchecked.
func reviewDefaultAllowed(tool ReviewTool) bool {
	if tool.Disabled {
		return false
	}
	if tool.ApprovalStatus == storage.ToolApprovalStatusApproved {
		return true
	}
	return tool.Tier == contracts.TierRead && tool.ScanVerdict == "clean" && tool.HeldReason == ""
}

func reviewTier(annotations *config.ToolAnnotations) contracts.Tier {
	if annotations == nil {
		return contracts.TierUnknown
	}
	return contracts.AnnotationTier(annotations)
}

func reviewTransport(protocol string) string {
	if strings.EqualFold(protocol, "stdio") || protocol == "" {
		return "stdio"
	}
	return "http"
}

func rawSchema(value string) json.RawMessage {
	if value == "" || !json.Valid([]byte(value)) {
		return nil
	}
	return json.RawMessage(value)
}

func copyStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	copy := make(map[string]string, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}

// reviewScanFor composes the scan summary for a server's review: the verdict
// of the newest baseline job, its coverage of the captured definitions, the
// findings, and a per-tool map of which records that scan covers.
func (r *Runtime) reviewScanFor(ctx context.Context, serverName string, quarantined bool, records []*storage.ToolApprovalRecord) (*ReviewScan, []scanner.ScanFinding, map[string]bool) {
	scan, findings, job := r.reviewScanAndFindings(ctx, serverName)
	covered := make(map[string]bool, len(records))
	for _, record := range records {
		covered[record.ToolName] = reviewToolCovered(job, quarantined, serverName, record)
	}
	scan.Coverage, scan.ToolsScanned, scan.UnscannedTools = reviewCoverage(job, records, covered)
	return scan, findings, covered
}

// reviewCoverage derives the coverage value for the newest baseline job.
func reviewCoverage(job *scanner.ScanJob, records []*storage.ToolApprovalRecord, covered map[string]bool) (string, int, []string) {
	toolsScanned := 0
	if job != nil && job.ScanContext != nil {
		toolsScanned = job.ScanContext.ToolsExported
	}
	switch {
	case len(records) == 0:
		return ReviewScanCoverageNotCaptured, 0, nil
	case job != nil && (job.Status == scanner.ScanJobStatusPending || job.Status == scanner.ScanJobStatusRunning):
		return ReviewScanCoverageScanning, 0, nil
	case job == nil || job.Status != scanner.ScanJobStatusCompleted:
		return ReviewScanCoverageNone, 0, nil
	case toolsScanned == 0:
		return ReviewScanCoverageToolsNotScanned, 0, nil
	}
	var unscanned []string
	for _, record := range records {
		if !covered[record.ToolName] {
			unscanned = append(unscanned, record.ToolName)
		}
	}
	if len(unscanned) == 0 {
		return ReviewScanCoverageCurrent, toolsScanned, nil
	}
	sort.Strings(unscanned)
	return ReviewScanCoverageStale, toolsScanned, unscanned
}

// reviewToolCovered reports whether a completed scan analysed the tool's
// CURRENT definition. A wrong "covered" is the dangerous direction for a
// security banner, so every unknown resolves to not covered.
//
//   - A scan that exported no definitions covers nothing.
//   - A definition added or changed after the scan read its definitions
//     (DefinitionChangedAt after ScanContext.ToolsExportedAt, or StartedAt
//     for a scan that recorded no export time) is not covered.
//   - A scan that recorded per-tool definition digests (ToolHashes) covers a
//     tool only when its digest equals the digest of the record's current
//     definition, so a definition swapped inside the timing window is not
//     covered. Scans without digests (legacy) use the rules below unchanged.
//   - A scan that recorded its tool names covers exactly those tools.
//   - A legacy scan (no recorded names, ToolsExported > 0) covers approved
//     records, and pending records of a quarantined server (its whole toolset
//     was listed at admission). It does not cover a pending record of a
//     trusted server (a tool added after the baseline) or a changed record
//     with no change stamp (the change time is unknown).
func reviewToolCovered(job *scanner.ScanJob, quarantined bool, serverName string, record *storage.ToolApprovalRecord) bool {
	if job == nil || job.Status != scanner.ScanJobStatusCompleted || job.ScanContext == nil || job.ScanContext.ToolsExported == 0 {
		return false
	}
	// The scan analysed the definitions as exported, which can be well before
	// the engine stamps StartedAt (scanner resolution, image checks). Legacy
	// jobs carry no export time and fall back to StartedAt.
	analysedAt := job.StartedAt
	if !job.ScanContext.ToolsExportedAt.IsZero() {
		analysedAt = job.ScanContext.ToolsExportedAt
	}
	if !record.DefinitionChangedAt.IsZero() && record.DefinitionChangedAt.After(analysedAt) {
		return false
	}
	if len(job.ScanContext.ToolHashes) > 0 {
		// Hash-bound scan: the analysed definition must be the record's
		// CURRENT one. A record with no stored definition cannot be compared,
		// so it is not covered.
		scanned, ok := job.ScanContext.ToolHashes[record.ToolName]
		if !ok {
			scanned, ok = job.ScanContext.ToolHashes[serverName+":"+record.ToolName]
		}
		if !ok || (record.CurrentDescription == "" && record.CurrentSchema == "") {
			return false
		}
		return scanned == hash.ToolDefinitionDigest(record.CurrentDescription, record.CurrentSchema)
	}
	if len(job.ScanContext.ToolNames) > 0 {
		for _, name := range job.ScanContext.ToolNames {
			if name == record.ToolName || name == serverName+":"+record.ToolName {
				return true
			}
		}
		return false
	}
	switch record.Status {
	case storage.ToolApprovalStatusApproved:
		return true
	case storage.ToolApprovalStatusPending:
		return quarantined
	case storage.ToolApprovalStatusChanged:
		return !record.DefinitionChangedAt.IsZero()
	}
	return false
}

// reviewScanAndFindings returns the newest baseline scan, its findings, and
// the job that supplied them (nil when there is none).
func (r *Runtime) reviewScanAndFindings(ctx context.Context, serverName string) (*ReviewScan, []scanner.ScanFinding, *scanner.ScanJob) {
	metas, err := r.storageManager.ListScanJobMetas(serverName)
	if err != nil {
		return &ReviewScan{Verdict: "not_scanned"}, nil, nil
	}
	var latest, latestPass2 *scanner.ScanJobMeta
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		switch meta.ScanPass {
		case scanner.ScanPassSupplyChainAudit:
			if latestPass2 == nil || meta.StartedAt.After(latestPass2.StartedAt) {
				latestPass2 = meta
			}
		case scanner.ScanPassSecurityScan, 0: // zero is a legacy Pass-1 record
			if latest == nil || meta.StartedAt.After(latest.StartedAt) {
				latest = meta
			}
		}
	}
	if latest == nil && latestPass2 == nil {
		return &ReviewScan{Verdict: "not_scanned"}, nil, nil
	}
	if latest == nil {
		latest = latestPass2
	}
	job, err := r.storageManager.GetScanJob(latest.ID)
	if err != nil || job == nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: latest.ID}, nil, nil
	}
	reports, err := r.storageManager.ListScanReportsByJob(job.ID)
	if err != nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: job.ID}, nil, job
	}
	primaryPass := scanner.ScanPassSecurityScan
	if latest == latestPass2 {
		primaryPass = scanner.ScanPassSupplyChainAudit
	}
	for _, report := range reports {
		for i := range report.Findings {
			report.Findings[i].ScanPass = primaryPass
		}
	}
	// The approval gate merges the newest completed supply-chain pass with the
	// baseline pass. Review must show the same findings and risk score rather
	// than presenting a clean baseline while the gate sees Pass 2 warnings.
	if latestPass2 != nil && latestPass2 != latest {
		if pass2, pass2Err := r.storageManager.GetScanJob(latestPass2.ID); pass2Err == nil && pass2 != nil && pass2.Status == scanner.ScanJobStatusCompleted {
			if pass2Reports, reportsErr := r.storageManager.ListScanReportsByJob(pass2.ID); reportsErr == nil {
				for _, report := range pass2Reports {
					for i := range report.Findings {
						report.Findings[i].ScanPass = scanner.ScanPassSupplyChainAudit
					}
				}
				reports = append(reports, pass2Reports...)
			}
		}
	}
	reports = deduplicateReviewPass2Findings(reports)
	aggregated := scanner.AggregateReportsWithJobStatus(job.ID, serverName, reports, job)
	if aggregated == nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: job.ID, ScannedAt: reviewTimestamp(job.CompletedAt)}, nil, job
	}
	verdict := aggregated.Verdict
	if verdict == "" {
		verdict = "not_scanned"
	}
	return &ReviewScan{Verdict: verdict, RiskScore: aggregated.RiskScore, ReportID: job.ID, ScannedAt: reviewTimestamp(aggregated.ScannedAt)}, aggregated.Findings, job
}

func deduplicateReviewPass2Findings(reports []*scanner.ScanReport) []*scanner.ScanReport {
	pass1 := make(map[string]struct{})
	for _, report := range reports {
		for _, finding := range report.Findings {
			if finding.ScanPass == scanner.ScanPassSecurityScan {
				pass1[finding.Scanner+"|"+finding.RuleID+"|"+finding.Title] = struct{}{}
			}
		}
	}
	for _, report := range reports {
		filtered := report.Findings[:0]
		for _, finding := range report.Findings {
			if finding.ScanPass == scanner.ScanPassSupplyChainAudit {
				if _, duplicate := pass1[finding.Scanner+"|"+finding.RuleID+"|"+finding.Title]; duplicate {
					continue
				}
			}
			filtered = append(filtered, finding)
		}
		report.Findings = filtered
	}
	return reports
}

func reviewTimestamp(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// reviewToolScanVerdict is the per-tool scan verdict. covered says the latest
// scan analysed this tool's CURRENT definition: only then do its findings
// apply, and absence of findings means "clean". A tool the scan did not cover
// shows its held verdict (the Spec 086 in-process check of the current
// definition) or "not_scanned"; findings of an older scan describe an older
// definition and are not applied.
func reviewToolScanVerdict(findings []scanner.ScanFinding, serverName string, record *storage.ToolApprovalRecord, covered bool) string {
	verdict := ""
	if covered {
		for _, finding := range findings {
			if !reviewFindingMatchesTool(finding.Location, serverName, record.ToolName) {
				continue
			}
			if finding.ThreatLevel == scanner.ThreatLevelDangerous {
				return "dangerous"
			}
			if finding.ThreatLevel == scanner.ThreatLevelWarning {
				verdict = "warnings"
			}
		}
	}
	if verdict == "" {
		verdict = record.HeldVerdict
	}
	if verdict == "" {
		if covered {
			return "clean"
		}
		return "not_scanned"
	}
	return verdict
}

func reviewFindingMatchesTool(location, serverName, toolName string) bool {
	return location == "tool:"+toolName || location == serverName+":"+toolName
}
