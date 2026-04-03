package scanner

import (
	"encoding/json"
	"time"
)

// Scanner status constants
const (
	ScannerStatusAvailable  = "available"
	ScannerStatusInstalled  = "installed"
	ScannerStatusConfigured = "configured"
	ScannerStatusError      = "error"
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
	LastUsedAt    time.Time         `json:"last_used_at,omitempty"`
	ErrorMsg      string            `json:"error_message,omitempty"`
	Custom        bool              `json:"custom,omitempty"` // User-added (not from registry)
}

// ScanJob represents a scan execution job
type ScanJob struct {
	ID          string    `json:"id"`
	ServerName  string    `json:"server_name"`
	Status      string    `json:"status"` // pending, running, completed, failed, cancelled
	Scanners    []string  `json:"scanners"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	DryRun      bool      `json:"dry_run,omitempty"`
	// Per-scanner status
	ScannerStatuses []ScannerJobStatus `json:"scanner_statuses"`
}

// ScannerJobStatus tracks a single scanner's execution within a scan job
type ScannerJobStatus struct {
	ScannerID     string    `json:"scanner_id"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	Error         string    `json:"error,omitempty"`
	FindingsCount int       `json:"findings_count"`
}

// ScanFinding represents an individual security finding
type ScanFinding struct {
	RuleID      string `json:"rule_id"`
	Severity    string `json:"severity"` // critical, high, medium, low, info
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location,omitempty"`
	Scanner     string `json:"scanner"`
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
	JobID      string        `json:"job_id"`
	ServerName string        `json:"server_name"`
	Findings   []ScanFinding `json:"findings"`
	RiskScore  int           `json:"risk_score"`
	Summary    ReportSummary `json:"summary"`
	ScannedAt  time.Time     `json:"scanned_at"`
	Reports    []ScanReport  `json:"reports"`
}

// ReportSummary provides counts by severity
type ReportSummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
	Total    int `json:"total"`
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
	ServersScanned     int           `json:"servers_scanned"`
	LastScanAt         time.Time     `json:"last_scan_at,omitempty"`
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
