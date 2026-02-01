// Package security provides sensitive data detection for MCP tool calls.
// It scans tool call arguments and responses for secrets, credentials,
// sensitive file paths, and other potentially exposed data.
package security

// Severity represents the risk level of a detection
type Severity string

const (
	SeverityCritical Severity = "critical" // Private keys, cloud credentials
	SeverityHigh     Severity = "high"     // API tokens, database credentials
	SeverityMedium   Severity = "medium"   // Credit cards, high entropy strings
	SeverityLow      Severity = "low"      // Custom patterns, keywords
)

// Category groups related detection patterns
type Category string

const (
	CategoryCloudCredentials   Category = "cloud_credentials"
	CategoryPrivateKey         Category = "private_key"
	CategoryAPIToken           Category = "api_token"
	CategoryAuthToken          Category = "auth_token"
	CategorySensitiveFile      Category = "sensitive_file"
	CategoryDatabaseCredential Category = "database_credential"
	CategoryHighEntropy        Category = "high_entropy"
	CategoryCreditCard         Category = "credit_card"
	CategoryCustom             Category = "custom"
)

// Detection represents a single sensitive data finding
type Detection struct {
	// Type is the pattern name that matched (e.g., "aws_access_key")
	Type string `json:"type"`

	// Category is the pattern category (e.g., "cloud_credentials")
	Category string `json:"category"`

	// Severity is the risk level (critical, high, medium, low)
	Severity string `json:"severity"`

	// Location is the JSON path where the match was found (e.g., "arguments.api_key")
	Location string `json:"location"`

	// IsLikelyExample indicates if the match is a known test/example value
	IsLikelyExample bool `json:"is_likely_example"`
}

// Result is the complete detection result stored in Activity metadata
type Result struct {
	// Detected is true if any sensitive data was found
	Detected bool `json:"detected"`

	// Detections is the list of findings
	Detections []Detection `json:"detections,omitempty"`

	// ScanDurationMs is the time taken to scan in milliseconds
	ScanDurationMs int64 `json:"scan_duration_ms"`

	// Truncated is true if payload exceeded max size and was truncated
	Truncated bool `json:"truncated,omitempty"`
}

// NewResult creates a new empty Result
func NewResult() *Result {
	return &Result{
		Detected:   false,
		Detections: make([]Detection, 0),
	}
}

// AddDetection adds a detection to the result
func (r *Result) AddDetection(d Detection) {
	r.Detections = append(r.Detections, d)
	r.Detected = true
}

// MaxSeverity returns the highest severity level in the result
func (r *Result) MaxSeverity() string {
	if !r.Detected || len(r.Detections) == 0 {
		return ""
	}

	severityOrder := map[string]int{
		string(SeverityCritical): 4,
		string(SeverityHigh):     3,
		string(SeverityMedium):   2,
		string(SeverityLow):      1,
	}

	maxSev := ""
	maxOrder := 0
	for _, d := range r.Detections {
		if order, ok := severityOrder[d.Severity]; ok && order > maxOrder {
			maxOrder = order
			maxSev = d.Severity
		}
	}
	return maxSev
}

// DetectionTypes returns a unique list of detection types found
func (r *Result) DetectionTypes() []string {
	if !r.Detected || len(r.Detections) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var types []string
	for _, d := range r.Detections {
		if !seen[d.Type] {
			seen[d.Type] = true
			types = append(types, d.Type)
		}
	}
	return types
}
