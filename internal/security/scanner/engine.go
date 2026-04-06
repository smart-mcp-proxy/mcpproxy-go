package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SecretResolverFunc resolves a secret reference like ${keyring:name} to its value
type SecretResolverFunc func(ctx context.Context, ref string) (string, error)

// Engine orchestrates parallel scanner execution for a server
type Engine struct {
	docker         *DockerRunner
	registry       *Registry
	logger         *zap.Logger
	dataDir        string
	secretResolver SecretResolverFunc

	// Track active scans (one per server)
	mu          sync.Mutex
	activeScans map[string]*ScanJob // keyed by server name
}

// NewEngine creates a new scan orchestration engine
func NewEngine(docker *DockerRunner, registry *Registry, dataDir string, logger *zap.Logger) *Engine {
	return &Engine{
		docker:      docker,
		registry:    registry,
		logger:      logger,
		dataDir:     dataDir,
		activeScans: make(map[string]*ScanJob),
	}
}

// ScanRequest describes a scan to execute
type ScanRequest struct {
	ServerName  string
	SourceDir   string            // Path to server source files (for "source" input)
	DryRun      bool              // If true, don't affect quarantine state
	ScannerIDs  []string          // Specific scanners to use (empty = all installed)
	Env         map[string]string // Additional environment variables
	ScanContext *ScanContext      // Context metadata (set by service)
	ScanPass    int               // 1 = security scan (fast), 2 = supply chain audit (background)
}

// ScanCallback receives scan lifecycle events
type ScanCallback interface {
	OnScanStarted(job *ScanJob)
	OnScannerStarted(job *ScanJob, scannerID string)
	OnScannerCompleted(job *ScanJob, scannerID string, report *ScanReport)
	OnScannerFailed(job *ScanJob, scannerID string, err error)
	OnScanCompleted(job *ScanJob, reports []*ScanReport)
	OnScanFailed(job *ScanJob, err error)
}

// NoopCallback is a no-op implementation of ScanCallback
type NoopCallback struct{}

func (n *NoopCallback) OnScanStarted(_ *ScanJob)                               {}
func (n *NoopCallback) OnScannerStarted(_ *ScanJob, _ string)                  {}
func (n *NoopCallback) OnScannerCompleted(_ *ScanJob, _ string, _ *ScanReport) {}
func (n *NoopCallback) OnScannerFailed(_ *ScanJob, _ string, _ error)          {}
func (n *NoopCallback) OnScanCompleted(_ *ScanJob, _ []*ScanReport)            {}
func (n *NoopCallback) OnScanFailed(_ *ScanJob, _ error)                       {}

// StartScan begins a scan of the specified server
// Returns the scan job immediately; scanning runs in the background
func (e *Engine) StartScan(ctx context.Context, req ScanRequest, callback ScanCallback) (*ScanJob, error) {
	if callback == nil {
		callback = &NoopCallback{}
	}

	// Check for existing scan
	e.mu.Lock()
	if existing, ok := e.activeScans[req.ServerName]; ok {
		e.mu.Unlock()
		return existing, fmt.Errorf("scan already in progress for server %s (job %s)", req.ServerName, existing.ID)
	}

	// Determine which scanners to use
	scanners, err := e.resolveScanners(req.ScannerIDs)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}

	if len(scanners) == 0 {
		e.mu.Unlock()
		return nil, fmt.Errorf("no scanners available; install scanners with 'mcpproxy security install <scanner-id>'")
	}

	// Create job
	scanPass := req.ScanPass
	if scanPass == 0 {
		scanPass = ScanPassSecurityScan // Default to pass 1
	}
	job := &ScanJob{
		ID:          fmt.Sprintf("scan-%s-%d", req.ServerName, time.Now().UnixNano()),
		ServerName:  req.ServerName,
		Status:      ScanJobStatusRunning,
		ScanPass:    scanPass,
		Scanners:    make([]string, len(scanners)),
		StartedAt:   time.Now(),
		DryRun:      req.DryRun,
		ScanContext: req.ScanContext,
	}
	for i, s := range scanners {
		job.Scanners[i] = s.ID
		job.ScannerStatuses = append(job.ScannerStatuses, ScannerJobStatus{
			ScannerID: s.ID,
			Status:    ScanJobStatusPending,
		})
	}

	e.activeScans[req.ServerName] = job
	e.mu.Unlock()

	callback.OnScanStarted(job)

	// Run scanners in background with detached context
	// (the HTTP request context may be cancelled after the response is sent)
	go e.executeScan(context.Background(), job, scanners, req, callback)

	return job, nil
}

// CancelScan cancels a running scan for a server
func (e *Engine) CancelScan(serverName string) error {
	e.mu.Lock()
	job, ok := e.activeScans[serverName]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("no active scan for server %s", serverName)
	}
	job.Status = ScanJobStatusCancelled
	job.CompletedAt = time.Now()
	delete(e.activeScans, serverName)
	e.mu.Unlock()
	return nil
}

// GetActiveJob returns the active scan job for a server, if any
func (e *Engine) GetActiveJob(serverName string) *ScanJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeScans[serverName]
}

// resolveScanners determines which scanners to use
func (e *Engine) resolveScanners(requestedIDs []string) ([]*ScannerPlugin, error) {
	all := e.registry.List()

	if len(requestedIDs) > 0 {
		// Use specifically requested scanners
		var result []*ScannerPlugin
		for _, id := range requestedIDs {
			s, err := e.registry.Get(id)
			if err != nil {
				return nil, fmt.Errorf("scanner %s not found", id)
			}
			if s.Status != ScannerStatusInstalled && s.Status != ScannerStatusConfigured {
				return nil, fmt.Errorf("scanner %s is not installed (status: %s)", id, s.Status)
			}
			result = append(result, s)
		}
		return result, nil
	}

	// Use all installed/configured scanners whose Docker images exist locally
	var result []*ScannerPlugin
	for _, s := range all {
		if s.Status == ScannerStatusInstalled || s.Status == ScannerStatusConfigured {
			// Verify Docker image exists before adding to scan list
			if e.docker != nil && !e.docker.ImageExists(context.Background(), s.DockerImage) {
				e.logger.Warn("Skipping scanner: Docker image not found locally",
					zap.String("scanner", s.ID),
					zap.String("image", s.DockerImage),
				)
				continue
			}
			result = append(result, s)
		}
	}
	return result, nil
}

// executeScan runs all scanners in parallel and collects results
func (e *Engine) executeScan(ctx context.Context, job *ScanJob, scanners []*ScannerPlugin, req ScanRequest, callback ScanCallback) {
	defer func() {
		e.mu.Lock()
		delete(e.activeScans, req.ServerName)
		e.mu.Unlock()
	}()

	var (
		reports []*ScanReport
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	for i, s := range scanners {
		wg.Add(1)
		go func(idx int, scanner *ScannerPlugin) {
			defer wg.Done()

			callback.OnScannerStarted(job, scanner.ID)
			e.updateScannerStatus(job, scanner.ID, ScanJobStatusRunning, time.Now(), time.Time{}, "", 0)

			report, scanLogs, err := e.runSingleScanner(ctx, scanner, req)
			if err != nil {
				e.logger.Warn("Scanner failed",
					zap.String("scanner", scanner.ID),
					zap.String("server", req.ServerName),
					zap.Error(err),
				)
				e.updateScannerStatus(job, scanner.ID, ScanJobStatusFailed, time.Time{}, time.Now(), err.Error(), 0)
				// Store logs even on failure
				e.setScannerLogs(job, scanner.ID, scanLogs)
				callback.OnScannerFailed(job, scanner.ID, err)
				return
			}

			report.JobID = job.ID
			report.ServerName = req.ServerName

			mu.Lock()
			reports = append(reports, report)
			mu.Unlock()

			e.updateScannerStatus(job, scanner.ID, ScanJobStatusCompleted, time.Time{}, time.Now(), "", len(report.Findings))
			e.setScannerLogs(job, scanner.ID, scanLogs)
			callback.OnScannerCompleted(job, scanner.ID, report)
		}(i, s)
	}

	wg.Wait()

	// Check if job was cancelled
	e.mu.Lock()
	if job.Status == ScanJobStatusCancelled {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	// Determine final status
	allFailed := true
	for _, ss := range job.ScannerStatuses {
		if ss.Status == ScanJobStatusCompleted {
			allFailed = false
			break
		}
	}

	if allFailed && len(scanners) > 0 {
		job.Status = ScanJobStatusFailed
		job.Error = "all scanners failed"
		job.CompletedAt = time.Now()
		callback.OnScanFailed(job, fmt.Errorf("all scanners failed"))
		return
	}

	job.Status = ScanJobStatusCompleted
	job.CompletedAt = time.Now()
	callback.OnScanCompleted(job, reports)
}

// runSingleScanner executes one scanner and returns its report plus execution logs
func (e *Engine) runSingleScanner(ctx context.Context, s *ScannerPlugin, req ScanRequest) (*ScanReport, scannerLogs, error) {
	// Parse timeout
	timeout := 120 * time.Second
	if s.Timeout != "" {
		if parsed, err := time.ParseDuration(s.Timeout); err == nil {
			timeout = parsed
		}
	}

	// Prepare report directory
	jobID := fmt.Sprintf("scan-%s-%d", req.ServerName, time.Now().UnixNano())
	reportDir, err := PrepareReportDir(e.dataDir, jobID, s.ID)
	if err != nil {
		return nil, scannerLogs{}, fmt.Errorf("failed to prepare report directory: %w", err)
	}

	// Build env vars: scanner config + request env
	// Resolve ${keyring:...} references if a secret resolver is available
	env := make(map[string]string)
	for k, v := range s.ConfiguredEnv {
		if e.secretResolver != nil && strings.HasPrefix(v, "${keyring:") {
			resolved, err := e.secretResolver(ctx, v)
			if err != nil {
				e.logger.Warn("Failed to resolve secret for scanner env",
					zap.String("key", k), zap.Error(err))
				continue // Skip unresolvable secrets
			}
			env[k] = resolved
		} else {
			env[k] = v
		}
	}
	for k, v := range req.Env {
		env[k] = v
	}

	// Determine network mode
	networkMode := "none"
	if s.NetworkReq {
		networkMode = "bridge"
	}

	// Create per-scanner cache directory (persists DB downloads between runs)
	cacheDir := filepath.Join(e.dataDir, "scanner-cache", s.ID)
	os.MkdirAll(cacheDir, 0755)

	// Run scanner container
	cfg := ScannerRunConfig{
		ContainerName: GenerateContainerName(s.ID, req.ServerName),
		Image:         s.DockerImage,
		Command:       s.Command,
		Env:           env,
		SourceDir:     req.SourceDir,
		ReportDir:     reportDir,
		CacheDir:      cacheDir,
		NetworkMode:   networkMode,
		Timeout:       timeout,
		ReadOnly:      false, // Scanner containers need to write cache/temp files
		MemoryLimit:   "512m",
	}

	stdout, stderr, exitCode, err := e.docker.RunScanner(ctx, cfg)
	logs := scannerLogs{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	if err != nil {
		return nil, logs, fmt.Errorf("scanner %s execution failed: %w", s.ID, err)
	}

	e.logger.Debug("Scanner finished",
		zap.String("scanner", s.ID),
		zap.Int("exit_code", exitCode),
		zap.Int("stdout_len", len(stdout)),
		zap.Int("stderr_len", len(stderr)),
	)

	// Collect results: try file first, then stdout
	var reportData []byte

	// Try reading from report directory
	reportData, err = e.docker.ReadReportFile(reportDir)
	if err != nil {
		// Fall back to stdout
		if len(stdout) > 0 {
			reportData = []byte(stdout)
		} else {
			return nil, logs, fmt.Errorf("scanner %s produced no output (exit code: %d, stderr: %s)", s.ID, exitCode, truncate(stderr, 500))
		}
	}

	// Parse results
	report, parseErr := e.parseResults(reportData, s.ID)
	return report, logs, parseErr
}

// parseResults parses scanner output into a ScanReport
func (e *Engine) parseResults(data []byte, scannerID string) (*ScanReport, error) {
	report := &ScanReport{
		ID:        fmt.Sprintf("report-%s-%d", scannerID, time.Now().UnixNano()),
		ScannerID: scannerID,
		ScannedAt: time.Now(),
	}

	// Try SARIF first
	if IsSARIF(data) {
		sarifReport, err := ParseSARIF(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse SARIF from %s: %w", scannerID, err)
		}
		report.Findings = NormalizeFindings(sarifReport, scannerID)
		ClassifyAllFindings(report.Findings)
		report.SarifRaw = json.RawMessage(data)
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Ramparts JSON format
	if isRampartsOutput(data) {
		findings := parseRampartsOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Snyk Agent Scan JSON format
	if isSnykAgentScanOutput(data) {
		findings := parseSnykAgentScanOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Cisco MCP Scanner raw JSON format
	if isCiscoScannerOutput(data) {
		findings := parseCiscoScannerOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try generic JSON with findings array
	var generic struct {
		Findings []ScanFinding `json:"findings"`
		Results  []ScanFinding `json:"results"`
	}
	if err := json.Unmarshal(data, &generic); err == nil {
		if len(generic.Findings) > 0 {
			report.Findings = generic.Findings
		} else if len(generic.Results) > 0 {
			report.Findings = generic.Results
		}
		if len(report.Findings) > 0 {
			// Ensure scanner is set on all findings
			for i := range report.Findings {
				if report.Findings[i].Scanner == "" {
					report.Findings[i].Scanner = scannerID
				}
			}
			report.RiskScore = CalculateRiskScore(report.Findings)
			return report, nil
		}
	}

	// If we got data but couldn't parse it, treat as no findings
	e.logger.Warn("Scanner output could not be parsed, treating as clean",
		zap.String("scanner", scannerID),
		zap.Int("data_length", len(data)),
	)
	report.Findings = []ScanFinding{}
	report.RiskScore = 0
	return report, nil
}

// scannerLogs captures stdout/stderr from a scanner execution
type scannerLogs struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// setScannerLogs stores stdout/stderr on a scanner's job status
func (e *Engine) setScannerLogs(job *ScanJob, scannerID string, logs scannerLogs) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range job.ScannerStatuses {
		if job.ScannerStatuses[i].ScannerID == scannerID {
			job.ScannerStatuses[i].Stdout = truncate(logs.Stdout, MaxLogBytes)
			job.ScannerStatuses[i].Stderr = truncate(logs.Stderr, MaxLogBytes)
			job.ScannerStatuses[i].ExitCode = logs.ExitCode
			return
		}
	}
}

// updateScannerStatus updates a specific scanner's status within a job
func (e *Engine) updateScannerStatus(job *ScanJob, scannerID, status string, startedAt, completedAt time.Time, errMsg string, findingsCount int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range job.ScannerStatuses {
		if job.ScannerStatuses[i].ScannerID == scannerID {
			job.ScannerStatuses[i].Status = status
			if !startedAt.IsZero() {
				job.ScannerStatuses[i].StartedAt = startedAt
			}
			if !completedAt.IsZero() {
				job.ScannerStatuses[i].CompletedAt = completedAt
			}
			job.ScannerStatuses[i].Error = errMsg
			job.ScannerStatuses[i].FindingsCount = findingsCount
			return
		}
	}
}

// AggregateReports combines multiple scan reports into an aggregated report.
// Note: scannersTotal and scannersFailed should be provided by the caller
// from the ScanJob.ScannerStatuses, since reports only contains successful results.
func AggregateReports(jobID, serverName string, reports []*ScanReport) *AggregatedReport {
	agg := &AggregatedReport{
		JobID:      jobID,
		ServerName: serverName,
		ScannedAt:  time.Now(),
		Reports:    make([]ScanReport, 0, len(reports)),
	}

	for _, r := range reports {
		agg.Findings = append(agg.Findings, r.Findings...)
		agg.Reports = append(agg.Reports, *r)
	}

	// Classify findings that lack threat_type/threat_level (legacy data)
	ClassifyAllFindings(agg.Findings)

	agg.RiskScore = CalculateRiskScore(agg.Findings)
	agg.Summary = SummarizeFindings(agg.Findings)

	// ScannersRun = number of successful reports
	agg.ScannersRun = len(reports)
	// ScanComplete = at least one scanner succeeded
	agg.ScanComplete = len(reports) > 0

	return agg
}

// AggregateReportsWithJobStatus combines reports and enriches with scanner failure info from the job.
func AggregateReportsWithJobStatus(jobID, serverName string, reports []*ScanReport, job *ScanJob) *AggregatedReport {
	agg := AggregateReports(jobID, serverName, reports)

	if job != nil {
		agg.ScannersTotal = len(job.ScannerStatuses)
		failed := 0
		succeeded := 0
		for _, ss := range job.ScannerStatuses {
			if ss.Status == ScanJobStatusFailed {
				failed++
			}
			if ss.Status == ScanJobStatusCompleted {
				succeeded++
			}
		}
		agg.ScannersFailed = failed
		agg.ScannersRun = succeeded
		agg.ScanComplete = succeeded > 0

		// Detect empty scans: scanners "succeeded" but scanned nothing meaningful.
		// This happens when a quarantined/disconnected server has no extractable
		// source files. Without this check, the UI would show a misleading "0/100"
		// risk score that implies the server is safe when nothing was actually analyzed.
		// Exceptions:
		//   - "url": HTTP behavioral scan (no filesystem)
		//   - "tool_definitions_only": Cisco scanner analyzed tool descriptions
		//   - ToolsExported > 0: tool definitions were analyzed even if no source files
		if agg.ScanComplete && len(agg.Findings) == 0 && job.ScanContext != nil {
			if job.ScanContext.TotalFiles == 0 &&
				job.ScanContext.SourceMethod != "url" &&
				job.ScanContext.SourceMethod != "tool_definitions_only" &&
				job.ScanContext.ToolsExported == 0 {
				agg.ScanComplete = false
				agg.EmptyScan = true
			}
		}
	}

	return agg
}

// isCiscoScannerOutput checks if the data looks like Cisco MCP Scanner output
func isCiscoScannerOutput(data []byte) bool {
	var probe struct {
		ScanResults []any `json:"scan_results"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.ScanResults != nil
}

// parseCiscoScannerOutput parses Cisco MCP Scanner's raw JSON format into ScanFindings.
// The Cisco format has: { "scan_results": [ { "tool_name": "x", "is_safe": false, "findings": { "yara_analyzer": { ... } } } ] }
func parseCiscoScannerOutput(data []byte, scannerID string) []ScanFinding {
	var cisco struct {
		ScanResults []struct {
			ToolName        string `json:"tool_name"`
			ToolDescription string `json:"tool_description"`
			IsSafe          bool   `json:"is_safe"`
			Findings        map[string]struct {
				Severity      string   `json:"severity"`
				ThreatNames   []string `json:"threat_names"`
				ThreatSummary string   `json:"threat_summary"`
				TotalFindings int      `json:"total_findings"`
				MCPTaxonomies []struct {
					ScannerCategory string `json:"scanner_category"`
					AITechName      string `json:"aitech_name"`
					AISubtechName   string `json:"aisubtech_name"`
					Description     string `json:"description"`
				} `json:"mcp_taxonomies"`
			} `json:"findings"`
		} `json:"scan_results"`
	}

	if err := json.Unmarshal(data, &cisco); err != nil {
		return nil
	}
	if len(cisco.ScanResults) == 0 {
		return nil
	}

	var findings []ScanFinding
	for _, result := range cisco.ScanResults {
		if result.IsSafe {
			continue // Skip safe tools
		}
		for analyzerName, analyzerResult := range result.Findings {
			if analyzerResult.TotalFindings == 0 {
				continue
			}
			for _, threat := range analyzerResult.ThreatNames {
				// Truncate tool description for evidence (max 500 chars)
				evidence := result.ToolDescription
				if len(evidence) > 500 {
					evidence = evidence[:500] + "..."
				}
				finding := ScanFinding{
					RuleID:      strings.ToLower(strings.ReplaceAll(threat, " ", "_")),
					Title:       threat + " in tool: " + result.ToolName,
					Description: analyzerResult.ThreatSummary,
					Scanner:     scannerID,
					Location:    "tool:" + result.ToolName,
					Evidence:    evidence,
				}

				// Map Cisco severity to our severity
				switch strings.ToUpper(analyzerResult.Severity) {
				case "HIGH":
					finding.Severity = SeverityHigh
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelDangerous
				case "MEDIUM":
					finding.Severity = SeverityMedium
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelWarning
				default:
					finding.Severity = SeverityLow
					finding.ThreatType = ThreatUncategorized
					finding.ThreatLevel = ThreatLevelInfo
				}

				// Classify based on threat name
				threatLower := strings.ToLower(threat)
				if strings.Contains(threatLower, "credential") || strings.Contains(threatLower, "exfiltrat") {
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelDangerous
				} else if strings.Contains(threatLower, "injection") {
					finding.ThreatType = ThreatPromptInjection
					finding.ThreatLevel = ThreatLevelDangerous
				}

				// Add taxonomy description if available
				if len(analyzerResult.MCPTaxonomies) > 0 {
					finding.Description = analyzerResult.MCPTaxonomies[0].Description
					finding.Category = analyzerResult.MCPTaxonomies[0].ScannerCategory
				}

				_ = analyzerName // used for context but not in the finding
				findings = append(findings, finding)
			}
		}
	}

	return findings
}

// isSnykAgentScanOutput checks if the data looks like Snyk Agent Scan output.
// The format is: { "/path/to/config.json": { "servers": [{ "name": "...", "signature": {...}, "issues": [...] }] } }
func isSnykAgentScanOutput(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	for _, v := range probe {
		var entry struct {
			Client  string `json:"client"`
			Servers []any  `json:"servers"`
		}
		if json.Unmarshal(v, &entry) == nil && entry.Servers != nil {
			return true
		}
	}
	return false
}

// parseSnykAgentScanOutput parses Snyk Agent Scan JSON output into ScanFindings.
// Snyk format: { "/path": { "servers": [{ "name": "x", "issues": [{ "code": "E001", "severity": "critical", ... }] }] } }
func parseSnykAgentScanOutput(data []byte, scannerID string) []ScanFinding {
	var snyk map[string]struct {
		Servers []struct {
			Name   string `json:"name"`
			Issues []struct {
				Code        string `json:"code"`
				Severity    string `json:"severity"`
				Title       string `json:"title"`
				Message     string `json:"message"`
				Description string `json:"description"`
				ToolName    string `json:"tool_name"`
				Category    string `json:"category"`
			} `json:"issues"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(data, &snyk); err != nil {
		return nil
	}

	var findings []ScanFinding
	for _, config := range snyk {
		for _, srv := range config.Servers {
			for _, issue := range srv.Issues {
				title := issue.Title
				if title == "" {
					title = issue.Message
				}
				if title == "" {
					title = issue.Code
				}

				desc := issue.Description
				if desc == "" {
					desc = issue.Message
				}

				finding := ScanFinding{
					RuleID:      issue.Code,
					Title:       title,
					Description: desc,
					Scanner:     scannerID,
					Category:    issue.Category,
				}
				if issue.ToolName != "" {
					finding.Location = "tool:" + issue.ToolName
				}

				switch strings.ToLower(issue.Severity) {
				case "critical":
					finding.Severity = SeverityCritical
				case "high":
					finding.Severity = SeverityHigh
				case "medium":
					finding.Severity = SeverityMedium
				case "low":
					finding.Severity = SeverityLow
				default:
					finding.Severity = SeverityMedium
				}

				findings = append(findings, finding)
			}
		}
	}

	return findings
}

// isRampartsOutput checks if the data looks like Ramparts MCP Scanner output
func isRampartsOutput(data []byte) bool {
	var probe struct {
		URL            string `json:"url"`
		SecurityIssues any    `json:"security_issues"`
		YaraResults    []any  `json:"yara_results"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.SecurityIssues != nil && probe.YaraResults != nil
}

// parseRampartsOutput parses Ramparts MCP Scanner's JSON format into ScanFindings.
// Ramparts output has: { "security_issues": { "tool_issues": [...], "prompt_issues": [...] }, "yara_results": [...] }
func parseRampartsOutput(data []byte, scannerID string) []ScanFinding {
	var ramparts struct {
		SecurityIssues struct {
			ToolIssues     []rampartsIssue `json:"tool_issues"`
			PromptIssues   []rampartsIssue `json:"prompt_issues"`
			ResourceIssues []rampartsIssue `json:"resource_issues"`
		} `json:"security_issues"`
		YaraResults []struct {
			TargetType   string `json:"target_type"`
			TargetName   string `json:"target_name"`
			RuleName     string `json:"rule_name"`
			RuleFile     string `json:"rule_file"`
			Context      string `json:"context"`
			Status       string `json:"status"`
			RuleMetadata *struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Severity    string `json:"severity"`
				Category    string `json:"category"`
			} `json:"rule_metadata"`
		} `json:"yara_results"`
	}

	if err := json.Unmarshal(data, &ramparts); err != nil {
		return nil
	}

	var findings []ScanFinding

	// Parse YARA results (individual matches, not summaries)
	for _, yr := range ramparts.YaraResults {
		if yr.TargetType == "summary" {
			continue // Skip pre-scan/post-scan summaries
		}
		if yr.Status != "warning" && yr.Status != "alert" {
			continue
		}

		finding := ScanFinding{
			RuleID:      strings.ToLower(strings.ReplaceAll(yr.RuleName, " ", "_")),
			Title:       yr.RuleName,
			Description: yr.Context,
			Scanner:     scannerID,
			Location:    yr.TargetType + ":" + yr.TargetName,
		}

		if yr.RuleMetadata != nil {
			if yr.RuleMetadata.Name != "" {
				finding.Title = yr.RuleMetadata.Name + " in " + yr.TargetType + ": " + yr.TargetName
			}
			if yr.RuleMetadata.Description != "" {
				finding.Description = yr.RuleMetadata.Description
			}
			finding.Category = yr.RuleFile

			switch strings.ToUpper(yr.RuleMetadata.Severity) {
			case "CRITICAL":
				finding.Severity = SeverityCritical
			case "HIGH":
				finding.Severity = SeverityHigh
			case "MEDIUM":
				finding.Severity = SeverityMedium
			default:
				finding.Severity = SeverityLow
			}
		} else {
			finding.Severity = SeverityMedium
		}

		findings = append(findings, finding)
	}

	// Parse security_issues (tool_issues, prompt_issues, resource_issues)
	parseIssues := func(issues []rampartsIssue, issueType string) {
		for _, issue := range issues {
			finding := ScanFinding{
				RuleID:      strings.ToLower(strings.ReplaceAll(issue.Type, " ", "_")),
				Title:       issue.Type + " in " + issueType + ": " + issue.ToolName,
				Description: issue.Description,
				Scanner:     scannerID,
				Location:    issueType + ":" + issue.ToolName,
			}
			switch strings.ToUpper(issue.Impact) {
			case "CRITICAL":
				finding.Severity = SeverityCritical
			case "HIGH":
				finding.Severity = SeverityHigh
			case "MEDIUM":
				finding.Severity = SeverityMedium
			default:
				finding.Severity = SeverityLow
			}
			findings = append(findings, finding)
		}
	}

	parseIssues(ramparts.SecurityIssues.ToolIssues, "tool")
	parseIssues(ramparts.SecurityIssues.PromptIssues, "prompt")
	parseIssues(ramparts.SecurityIssues.ResourceIssues, "resource")

	return findings
}

// rampartsIssue represents a security issue from Ramparts
type rampartsIssue struct {
	ToolName    string `json:"tool_name"`
	Type        string `json:"type"`
	Impact      string `json:"impact"`
	Description string `json:"description"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
