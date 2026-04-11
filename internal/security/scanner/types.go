package scanner

import (
	"encoding/json"
	"time"
)

// Scanner status constants
const (
	// ScannerStatusAvailable means the scanner is known to mcpproxy but
	// not enabled. Toggling it on moves it into ScannerStatusPulling.
	ScannerStatusAvailable = "available"
	// ScannerStatusPulling means the Docker image is currently being
	// downloaded in the background. UI should show a spinner.
	ScannerStatusPulling = "pulling"
	// ScannerStatusInstalled means the image is present locally and the
	// scanner is ready to run. No required API keys are configured yet.
	ScannerStatusInstalled = "installed"
	// ScannerStatusConfigured means the image is present AND user-supplied
	// env (e.g. API keys) have been stored.
	ScannerStatusConfigured = "configured"
	// ScannerStatusError means the last operation (typically a pull) failed.
	// ErrorMsg carries the reason. The UI should offer a "Retry" button.
	ScannerStatusError = "error"
)

// Scan job status constants
const (
	ScanJobStatusPending   = "pending"
	ScanJobStatusRunning   = "running"
	ScanJobStatusCompleted = "completed"
	ScanJobStatusFailed    = "failed"
	ScanJobStatusCancelled = "cancelled"
)

// Scan finding severity constants
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// EnvRequirement represents a required or optional environment variable for a scanner
type EnvRequirement struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// ScannerPlugin represents a security scanner plugin
type ScannerPlugin struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Vendor      string           `json:"vendor"`
	Description string           `json:"description"`
	License     string           `json:"license"`
	Homepage    string           `json:"homepage"`
	DockerImage string           `json:"docker_image"`
	Inputs      []string         `json:"inputs"`  // "source", "mcp_connection", "container_image"
	Outputs     []string         `json:"outputs"` // "sarif"
	RequiredEnv []EnvRequirement `json:"required_env"`
	OptionalEnv []EnvRequirement `json:"optional_env"`
	Command     []string         `json:"command"`
	Timeout     string           `json:"timeout"`
	NetworkReq  bool             `json:"network_required"`
	// Runtime state (not in registry)
	Status        string            `json:"status"` // available, installed, configured, error
	InstalledAt   time.Time         `json:"installed_at,omitempty"`
	ConfiguredEnv map[string]string `json:"configured_env,omitempty"` // Set env values (secrets redacted in API)
	ImageOverride string            `json:"image_override,omitempty"` // User override for DockerImage
	LastUsedAt    time.Time         `json:"last_used_at,omitempty"`
	ErrorMsg      string            `json:"error_message,omitempty"`
	Custom        bool              `json:"custom,omitempty"` // User-added (not from registry)
}

// EffectiveImage returns ImageOverride if set, otherwise DockerImage.
func (s *ScannerPlugin) EffectiveImage() string {
	if s.ImageOverride != "" {
		return s.ImageOverride
	}
	return s.DockerImage
}

// Scan pass constants
const (
	ScanPassSecurityScan     = 1 // Fast pass: source code + lockfile scanning
	ScanPassSupplyChainAudit = 2 // Background pass: full filesystem CVE analysis
)

// Scan context limits
const (
	MaxScanLogLines   = 5000   // Max lines of stdout/stderr per scanner
	MaxScannedFiles   = 10000  // Max file entries in scanned_files list
	MaxScansPerServer = 20     // Keep last N scans per server
	MaxLogBytes       = 500000 // ~500KB max per log field
)

// ScanJob represents a scan execution job
type ScanJob struct {
	ID          string    `json:"id"`
	ServerName  string    `json:"server_name"`
	Status      string    `json:"status"`    // pending, running, completed, failed, cancelled
	ScanPass    int       `json:"scan_pass"` // 1 = security scan (fast), 2 = supply chain audit (background)
	Scanners    []string  `json:"scanners"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	DryRun      bool      `json:"dry_run,omitempty"`
	// Per-scanner status
	ScannerStatuses []ScannerJobStatus `json:"scanner_statuses"`
	// Scan context — what was scanned and how
	ScanContext *ScanContext `json:"scan_context,omitempty"`
}

// ScanJobSummary is a lightweight view of a scan job for history listing
type ScanJobSummary struct {
	ID            string    `json:"id"`
	ServerName    string    `json:"server_name"`
	Status        string    `json:"status"`
	ScanPass      int       `json:"scan_pass"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	FindingsCount int       `json:"findings_count"`
	RiskScore     int       `json:"risk_score"`
	Scanners      []string  `json:"scanners"`
}

// ScanContext describes what was scanned and how the source was resolved.
// This gives users full transparency into what the scanners actually checked.
type ScanContext struct {
	SourceMethod    string   `json:"source_method"`             // "docker_extract", "working_dir", "local_path", "url", "none"
	SourcePath      string   `json:"source_path"`               // Actual path/URL that was scanned
	DockerIsolation bool     `json:"docker_isolation"`          // Whether server runs in Docker
	ContainerID     string   `json:"container_id,omitempty"`    // Docker container ID (if applicable)
	ContainerImage  string   `json:"container_image,omitempty"` // Docker image used
	ServerProtocol  string   `json:"server_protocol"`           // stdio, http, sse
	ServerCommand   string   `json:"server_command,omitempty"`  // Command used to start server
	ToolsExported   int      `json:"tools_exported,omitempty"`  // Number of tool definitions exported for scanning
	ScannedFiles    []string `json:"scanned_files,omitempty"`   // List of files that were scanned (capped at MaxScannedFiles)
	TotalFiles      int      `json:"total_files"`               // Total file count (may be > len(ScannedFiles) if capped)
	TotalSizeBytes  int64    `json:"total_size_bytes"`          // Total size of scanned source
}

// ScannerJobStatus tracks a single scanner's execution within a scan job
type ScannerJobStatus struct {
	ScannerID     string    `json:"scanner_id"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	Error         string    `json:"error,omitempty"`
	FindingsCount int       `json:"findings_count"`
	Stdout        string    `json:"stdout,omitempty"` // Scanner stdout (for log viewing)
	Stderr        string    `json:"stderr,omitempty"` // Scanner stderr (for log viewing)
	ExitCode      int       `json:"exit_code"`
}

// User-facing threat category constants
const (
	ThreatToolPoisoning   = "tool_poisoning"   // Hidden instructions in tool descriptions
	ThreatPromptInjection = "prompt_injection" // Malicious payloads in tool responses
	ThreatRugPull         = "rug_pull"         // Tool definitions changed after approval
	ThreatSupplyChain     = "supply_chain"     // Known CVEs in dependencies
	ThreatMaliciousCode   = "malicious_code"   // Malware, backdoors, suspicious code
	ThreatUncategorized   = "uncategorized"    // Other findings
)

// User-facing severity levels (simpler than CVSS)
const (
	ThreatLevelDangerous = "dangerous" // Blocks approval: tool poisoning, active injection
	ThreatLevelWarning   = "warning"   // Rug pull, high CVEs
	ThreatLevelInfo      = "info"      // Low CVEs, informational
)

// ScanFinding represents an individual security finding
type ScanFinding struct {
	RuleID           string  `json:"rule_id"`
	Severity         string  `json:"severity"`     // critical, high, medium, low, info
	Category         string  `json:"category"`     // SARIF category
	ThreatType       string  `json:"threat_type"`  // User-facing: tool_poisoning, prompt_injection, rug_pull, supply_chain
	ThreatLevel      string  `json:"threat_level"` // User-facing: dangerous, warning, info
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	Location         string  `json:"location,omitempty"`
	Scanner          string  `json:"scanner"`
	HelpURI          string  `json:"help_uri,omitempty"`          // Link to CVE/advisory details
	CVSSScore        float64 `json:"cvss_score,omitempty"`        // CVSS severity score (0-10)
	PackageName      string  `json:"package_name,omitempty"`      // Affected package
	InstalledVersion string  `json:"installed_version,omitempty"` // Current version
	FixedVersion     string  `json:"fixed_version,omitempty"`     // Version with fix
	ScanPass         int     `json:"scan_pass,omitempty"`         // 1 = security scan, 2 = supply chain audit
	Evidence         string  `json:"evidence,omitempty"`          // The text/content that triggered the finding
	// SupplyChainAudit marks findings that belong in the "Supply Chain Audit (CVEs)"
	// UI section regardless of which pass produced them. True only for real CVE/package
	// vulnerabilities (CVE-prefixed rule ID or a populated PackageName). AI scanner and
	// other non-package findings stay false so the UI can route them to their proper
	// threat_type group instead of the CVE section.
	SupplyChainAudit bool `json:"supply_chain_audit,omitempty"`
}

// ScanReport represents aggregated scan results for a server
type ScanReport struct {
	ID         string          `json:"id"`
	JobID      string          `json:"job_id"`
	ServerName string          `json:"server_name"`
	ScannerID  string          `json:"scanner_id"`
	Findings   []ScanFinding   `json:"findings"`
	RiskScore  int             `json:"risk_score"` // 0-100
	SarifRaw   json.RawMessage `json:"sarif_raw,omitempty"`
	ScannedAt  time.Time       `json:"scanned_at"`
}

// AggregatedReport combines results from all scanners for a single scan job
type AggregatedReport struct {
	JobID          string        `json:"job_id"`
	ServerName     string        `json:"server_name"`
	Findings       []ScanFinding `json:"findings"`
	RiskScore      int           `json:"risk_score"`
	Summary        ReportSummary `json:"summary"`
	ScannedAt      time.Time     `json:"scanned_at"`
	Reports        []ScanReport  `json:"reports"`
	ScannersRun    int           `json:"scanners_run"`    // How many scanners actually produced results
	ScannersFailed int           `json:"scanners_failed"` // How many scanners failed
	ScannersTotal  int           `json:"scanners_total"`  // Total scanners attempted
	ScanComplete   bool          `json:"scan_complete"`   // True only if at least one scanner succeeded
	EmptyScan      bool          `json:"empty_scan"`      // True when scanners ran but had no files to analyze
	// Two-pass scan tracking
	Pass1Complete bool `json:"pass1_complete"` // Security scan (fast) done
	Pass2Complete bool `json:"pass2_complete"` // Supply chain audit done
	Pass2Running  bool `json:"pass2_running"`  // Supply chain audit in progress
	// Scan context from the primary job (for report page display)
	ScanContext     *ScanContext       `json:"scan_context,omitempty"`
	ScannerStatuses []ScannerJobStatus `json:"scanner_statuses,omitempty"` // Per-scanner execution logs
}

// ReportSummary provides counts by severity and threat level
type ReportSummary struct {
	Critical  int `json:"critical"`
	High      int `json:"high"`
	Medium    int `json:"medium"`
	Low       int `json:"low"`
	Info      int `json:"info"`
	Total     int `json:"total"`
	Dangerous int `json:"dangerous"`  // Threat level: tool poisoning, prompt injection
	Warnings  int `json:"warnings"`   // Threat level: rug pull, high CVEs
	InfoLevel int `json:"info_level"` // Threat level: low CVEs, informational
}

// IntegrityBaseline represents the approved integrity state for a server
type IntegrityBaseline struct {
	ServerName    string            `json:"server_name"`
	ImageDigest   string            `json:"image_digest"`
	SourceHash    string            `json:"source_hash"`
	LockfileHash  string            `json:"lockfile_hash"`
	DiffManifest  []string          `json:"diff_manifest,omitempty"`
	ToolHashes    map[string]string `json:"tool_hashes,omitempty"`
	ScanReportIDs []string          `json:"scan_report_ids,omitempty"`
	ApprovedAt    time.Time         `json:"approved_at"`
	ApprovedBy    string            `json:"approved_by"`
}

// SecurityOverview provides dashboard aggregate stats
type SecurityOverview struct {
	TotalScans         int           `json:"total_scans"`
	ActiveScans        int           `json:"active_scans"`
	FindingsBySeverity ReportSummary `json:"findings_by_severity"`
	ScannersInstalled  int           `json:"scanners_installed"`
	ScannersEnabled    int           `json:"scanners_enabled"` // Subset of installed that the engine will run (status installed or configured)
	ServersScanned     int           `json:"servers_scanned"`
	LastScanAt         time.Time     `json:"last_scan_at,omitempty"`
	DockerAvailable    bool          `json:"docker_available"`
}

// MarshalBinary implements encoding.BinaryMarshaler
func (s *ScannerPlugin) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (s *ScannerPlugin) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (j *ScanJob) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (j *ScanJob) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, j)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (r *ScanReport) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (r *ScanReport) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, r)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (b *IntegrityBaseline) MarshalBinary() ([]byte, error) {
	return json.Marshal(b)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (b *IntegrityBaseline) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, b)
}
