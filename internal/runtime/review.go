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

type ReviewScan struct {
	Verdict   string     `json:"verdict"`
	RiskScore int        `json:"risk_score"`
	ReportID  string     `json:"report_id,omitempty"`
	ScannedAt *time.Time `json:"scanned_at,omitempty"`
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
	Previous       *ReviewToolPrevious     `json:"previous"`
	Diff           *ReviewToolDiff         `json:"diff,omitempty"`
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
			row.Scan = r.reviewScan(ctx, server.Name, nil)
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
	reviewScan, scanFindings := r.reviewScanAndFindings(ctx, serverName)
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
			ScanVerdict: reviewToolScanVerdict(scanFindings, serverName, record),
			HeldReason:  record.HeldReason, HeldSignals: append([]string(nil), record.HeldSignals...),
		}
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

func (r *Runtime) reviewScan(ctx context.Context, serverName string, record *storage.ToolApprovalRecord) *ReviewScan {
	result, _ := r.reviewScanAndFindings(ctx, serverName)
	if record != nil {
		_, findings := r.reviewScanAndFindings(ctx, serverName)
		result.Verdict = reviewToolScanVerdict(findings, serverName, record)
	}
	return result
}

func (r *Runtime) reviewScanAndFindings(ctx context.Context, serverName string) (*ReviewScan, []scanner.ScanFinding) {
	metas, err := r.storageManager.ListScanJobMetas(serverName)
	if err != nil {
		return &ReviewScan{Verdict: "not_scanned"}, nil
	}
	var latest, latestPass2 *scanner.ScanJobMeta
	for _, meta := range metas {
		if meta == nil || meta.ScanPass != scanner.ScanPassSecurityScan {
			if meta != nil && meta.ScanPass == scanner.ScanPassSupplyChainAudit && (latestPass2 == nil || meta.StartedAt.After(latestPass2.StartedAt)) {
				latestPass2 = meta
			}
			continue
		}
		if latest == nil || meta.StartedAt.After(latest.StartedAt) {
			latest = meta
		}
	}
	if latest == nil {
		return &ReviewScan{Verdict: "not_scanned"}, nil
	}
	job, err := r.storageManager.GetScanJob(latest.ID)
	if err != nil || job == nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: latest.ID}, nil
	}
	reports, err := r.storageManager.ListScanReportsByJob(job.ID)
	if err != nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: job.ID}, nil
	}
	for _, report := range reports {
		for i := range report.Findings {
			report.Findings[i].ScanPass = scanner.ScanPassSecurityScan
		}
	}
	// The approval gate merges the newest completed supply-chain pass with the
	// baseline pass. Review must show the same findings and risk score rather
	// than presenting a clean baseline while the gate sees Pass 2 warnings.
	if latestPass2 != nil {
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
	aggregated := scanner.AggregateReportsWithJobStatus(job.ID, serverName, reports, job)
	if aggregated == nil {
		return &ReviewScan{Verdict: "not_scanned", ReportID: job.ID, ScannedAt: reviewTimestamp(job.CompletedAt)}, nil
	}
	verdict := aggregated.Verdict
	if verdict == "" {
		verdict = "not_scanned"
	}
	return &ReviewScan{Verdict: verdict, RiskScore: aggregated.RiskScore, ReportID: job.ID, ScannedAt: reviewTimestamp(aggregated.ScannedAt)}, aggregated.Findings
}

func reviewTimestamp(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func reviewToolScanVerdict(findings []scanner.ScanFinding, serverName string, record *storage.ToolApprovalRecord) string {
	verdict := ""
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
	if verdict == "" {
		verdict = record.HeldVerdict
	}
	if verdict == "" {
		verdict = "not_scanned"
	}
	return verdict
}

func reviewFindingMatchesTool(location, serverName, toolName string) bool {
	return location == "tool:"+toolName || location == serverName+":"+toolName
}
