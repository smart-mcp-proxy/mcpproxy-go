# Data Model: Sensitive Data Detection

**Phase**: 1 - Design
**Date**: 2026-01-31
**Status**: Complete

## Entity Relationship

```
┌─────────────────────────────┐     ┌──────────────────────────────┐
│    DetectionPattern         │     │   SensitiveDataConfig        │
├─────────────────────────────┤     ├──────────────────────────────┤
│ - Name: string              │     │ - Enabled: bool              │
│ - Regex: string (optional)  │     │ - ScanRequests: bool         │
│ - Keywords: []string        │     │ - ScanResponses: bool        │
│ - Severity: Severity        │     │ - MaxPayloadSizeKB: int      │
│ - Category: Category        │     │ - EntropyThreshold: float64  │
│ - Validate: func (optional) │     │ - Categories: map[string]bool│
│ - Description: string       │     │ - CustomPatterns: []Pattern  │
└─────────────────────────────┘     │ - SensitiveKeywords: []string│
                                    └──────────────────────────────┘
         │
         │ matches
         ▼
┌─────────────────────────────┐     ┌──────────────────────────────┐
│    Detection                │◄────│  SensitiveDataResult         │
├─────────────────────────────┤     ├──────────────────────────────┤
│ - Type: string              │     │ - Detected: bool             │
│ - Category: string          │     │ - Detections: []Detection    │
│ - Severity: string          │     │ - ScanDurationMs: int64      │
│ - Location: string          │     │ - Truncated: bool            │
│ - IsLikelyExample: bool     │     └──────────────────────────────┘
└─────────────────────────────┘
         │
         │ stored in
         ▼
┌─────────────────────────────────────────────────────────────────┐
│    ActivityRecord.Metadata["sensitive_data_detection"]          │
├─────────────────────────────────────────────────────────────────┤
│ JSON blob containing SensitiveDataResult                        │
└─────────────────────────────────────────────────────────────────┘
```

## Core Types

### Severity Enum

```go
type Severity string

const (
    SeverityCritical Severity = "critical"  // Private keys, cloud credentials
    SeverityHigh     Severity = "high"      // API tokens, database credentials
    SeverityMedium   Severity = "medium"    // Credit cards, high entropy
    SeverityLow      Severity = "low"       // Custom patterns, keywords
)
```

### Category Enum

```go
type Category string

const (
    CategoryCloudCredentials  Category = "cloud_credentials"
    CategoryPrivateKey        Category = "private_key"
    CategoryAPIToken          Category = "api_token"
    CategoryAuthToken         Category = "auth_token"
    CategorySensitiveFile     Category = "sensitive_file"
    CategoryDatabaseCredential Category = "database_credential"
    CategoryHighEntropy       Category = "high_entropy"
    CategoryCreditCard        Category = "credit_card"
    CategoryCustom            Category = "custom"
)
```

### DetectionPattern

```go
// DetectionPattern defines a pattern for detecting sensitive data
type DetectionPattern struct {
    // Name is the unique identifier for this pattern (e.g., "aws_access_key")
    Name string `json:"name"`

    // Description is human-readable explanation
    Description string `json:"description,omitempty"`

    // Regex is the pattern to match (mutually exclusive with Keywords)
    Regex string `json:"regex,omitempty"`

    // Keywords are exact strings to match (mutually exclusive with Regex)
    Keywords []string `json:"keywords,omitempty"`

    // Category groups related patterns
    Category Category `json:"category"`

    // Severity indicates the risk level
    Severity Severity `json:"severity"`

    // Validate is an optional function for additional validation (e.g., Luhn)
    Validate func(match string) bool `json:"-"`

    // KnownExamples are test/example values to flag as is_likely_example
    KnownExamples []string `json:"known_examples,omitempty"`
}
```

### Detection

```go
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
```

### SensitiveDataResult

```go
// SensitiveDataResult is the complete detection result stored in Activity metadata
type SensitiveDataResult struct {
    // Detected is true if any sensitive data was found
    Detected bool `json:"detected"`

    // Detections is the list of findings
    Detections []Detection `json:"detections,omitempty"`

    // ScanDurationMs is the time taken to scan
    ScanDurationMs int64 `json:"scan_duration_ms"`

    // Truncated is true if payload exceeded max size
    Truncated bool `json:"truncated,omitempty"`
}
```

### SensitiveDataConfig

```go
// SensitiveDataConfig defines user-configurable detection settings
type SensitiveDataConfig struct {
    // Enabled turns detection on/off (default: true)
    Enabled bool `json:"enabled"`

    // ScanRequests enables scanning tool call arguments (default: true)
    ScanRequests bool `json:"scan_requests"`

    // ScanResponses enables scanning tool responses (default: true)
    ScanResponses bool `json:"scan_responses"`

    // MaxPayloadSizeKB is the max size to scan before truncating (default: 1024)
    MaxPayloadSizeKB int `json:"max_payload_size_kb"`

    // EntropyThreshold for high-entropy string detection (default: 4.5)
    EntropyThreshold float64 `json:"entropy_threshold"`

    // Categories enables/disables specific detection categories
    Categories map[string]bool `json:"categories"`

    // CustomPatterns are user-defined patterns
    CustomPatterns []CustomPatternConfig `json:"custom_patterns,omitempty"`

    // SensitiveKeywords are exact strings to flag
    SensitiveKeywords []string `json:"sensitive_keywords,omitempty"`
}

// CustomPatternConfig is a user-defined detection pattern
type CustomPatternConfig struct {
    Name     string   `json:"name"`
    Regex    string   `json:"regex,omitempty"`
    Keywords []string `json:"keywords,omitempty"`
    Severity string   `json:"severity"` // critical, high, medium, low
    Category string   `json:"category,omitempty"` // defaults to "custom"
}
```

## Default Configuration

```go
func DefaultSensitiveDataConfig() *SensitiveDataConfig {
    return &SensitiveDataConfig{
        Enabled:          true,
        ScanRequests:     true,
        ScanResponses:    true,
        MaxPayloadSizeKB: 1024,
        EntropyThreshold: 4.5,
        Categories: map[string]bool{
            "cloud_credentials":   true,
            "private_key":         true,
            "api_token":           true,
            "auth_token":          true,
            "sensitive_file":      true,
            "database_credential": true,
            "high_entropy":        true,
            "credit_card":         true,
        },
        CustomPatterns:    nil,
        SensitiveKeywords: nil,
    }
}
```

## File Path Pattern Model

```go
// FilePathPattern defines a sensitive file path pattern
type FilePathPattern struct {
    // Name identifies the pattern
    Name string `json:"name"`

    // Category for grouping (e.g., "ssh", "cloud", "env")
    Category string `json:"category"`

    // Severity for this path type
    Severity Severity `json:"severity"`

    // LinuxPatterns are glob patterns for Linux
    LinuxPatterns []string `json:"linux_patterns,omitempty"`

    // MacOSPatterns are glob patterns for macOS (if different from Linux)
    MacOSPatterns []string `json:"macos_patterns,omitempty"`

    // WindowsPatterns are glob patterns for Windows
    WindowsPatterns []string `json:"windows_patterns,omitempty"`

    // UniversalPatterns apply to all platforms
    UniversalPatterns []string `json:"universal_patterns,omitempty"`
}
```

## Storage Schema

### BBolt Bucket Structure

No new buckets required. Detection results stored in existing `activities` bucket as part of `ActivityRecord.Metadata`.

```
activities/
  └── {activity_id} → ActivityRecord JSON
                        └── metadata
                              └── sensitive_data_detection → SensitiveDataResult JSON
```

### JSON Storage Example

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "type": "tool_call",
  "server": "github-server",
  "tool": "create_secret",
  "status": "success",
  "timestamp": "2026-01-31T10:30:00Z",
  "duration": 150000000,
  "metadata": {
    "arguments": "{\"key_name\": \"API_KEY\", \"value\": \"sk_live_xxxx\"}",
    "response": "{\"created\": true}",
    "sensitive_data_detection": {
      "detected": true,
      "detections": [
        {
          "type": "stripe_key",
          "category": "api_token",
          "severity": "high",
          "location": "arguments.value",
          "is_likely_example": false
        }
      ],
      "scan_duration_ms": 8,
      "truncated": false
    }
  }
}
```

## API Query Model

### Filter Parameters

```go
// ActivityQueryParams for REST API and CLI
type ActivityQueryParams struct {
    // Existing parameters...
    Type      string `query:"type"`
    Status    string `query:"status"`
    Server    string `query:"server"`
    RequestID string `query:"request_id"`

    // NEW: Sensitive data filters
    SensitiveData  *bool  `query:"sensitive_data"`   // true = only with detections
    DetectionType  string `query:"detection_type"`   // e.g., "aws_access_key"
    Severity       string `query:"severity"`         // critical, high, medium, low
}
```

### Response Extension

```go
// ActivityResponse includes detection summary
type ActivityResponse struct {
    ID        string                 `json:"id"`
    Type      string                 `json:"type"`
    Server    string                 `json:"server,omitempty"`
    Tool      string                 `json:"tool,omitempty"`
    Status    string                 `json:"status"`
    Timestamp time.Time              `json:"timestamp"`
    Duration  int64                  `json:"duration_ms,omitempty"`
    Metadata  map[string]interface{} `json:"metadata,omitempty"`

    // NEW: Detection summary for list view
    HasSensitiveData bool     `json:"has_sensitive_data"`
    DetectionTypes   []string `json:"detection_types,omitempty"`
    MaxSeverity      string   `json:"max_severity,omitempty"`
}
```

## Event Model

```go
// SensitiveDataDetectedEvent emitted to event bus for real-time updates
type SensitiveDataDetectedEvent struct {
    ActivityID     string      `json:"activity_id"`
    Server         string      `json:"server"`
    Tool           string      `json:"tool"`
    Detections     []Detection `json:"detections"`
    Timestamp      time.Time   `json:"timestamp"`
}
```

## Validation Rules

1. **Pattern Name**: Must be unique, lowercase with underscores
2. **Regex**: Must compile without error
3. **Severity**: Must be one of: critical, high, medium, low
4. **Category**: Must be one of defined categories or "custom"
5. **EntropyThreshold**: Must be between 0.0 and 8.0
6. **MaxPayloadSizeKB**: Must be between 1 and 10240 (10MB max)

## Migration Notes

- No database migration required (uses existing Metadata field)
- Configuration migration: add `sensitive_data_detection` section with defaults
- Backward compatible: old records without detection metadata treated as "not scanned"
