# Quickstart: Sensitive Data Detection

**Phase**: 1 - Design
**Date**: 2026-01-31

## Overview

This guide provides a rapid path to implementing and testing the sensitive data detection feature. It covers the core detector implementation, Activity Log integration, and basic testing.

## Prerequisites

- Go 1.24+
- MCPProxy codebase cloned
- Familiarity with `internal/runtime/activity_service.go`
- Understanding of `internal/config/config.go` patterns

## Step 1: Create the Detection Package

```bash
mkdir -p internal/security
```

### Core Detector Interface

```go
// internal/security/detector.go
package security

import (
    "regexp"
    "sync"
    "time"
)

// Detector scans data for sensitive information
type Detector struct {
    patterns       []*compiledPattern
    filePatterns   []*FilePathPattern
    config         *DetectionConfig
    mu             sync.RWMutex
}

// DetectionConfig holds runtime configuration
type DetectionConfig struct {
    Enabled          bool
    ScanRequests     bool
    ScanResponses    bool
    MaxPayloadSize   int
    EntropyThreshold float64
    EnabledCategories map[string]bool
}

type compiledPattern struct {
    name     string
    regex    *regexp.Regexp
    category string
    severity string
    validate func(string) bool
    examples []string
}

// NewDetector creates a detector with default patterns
func NewDetector(config *DetectionConfig) *Detector {
    d := &Detector{
        config: config,
    }
    d.loadBuiltinPatterns()
    return d
}

// Scan checks data for sensitive information
func (d *Detector) Scan(arguments, response string) *Result {
    if !d.config.Enabled {
        return &Result{Detected: false}
    }

    start := time.Now()
    result := &Result{
        Detections: make([]Detection, 0),
    }

    // Scan arguments
    if d.config.ScanRequests && arguments != "" {
        d.scanContent(arguments, "arguments", result)
    }

    // Scan response
    if d.config.ScanResponses && response != "" {
        d.scanContent(response, "response", result)
    }

    result.Detected = len(result.Detections) > 0
    result.ScanDurationMs = time.Since(start).Milliseconds()
    return result
}

func (d *Detector) scanContent(content, location string, result *Result) {
    // Truncate if needed
    if len(content) > d.config.MaxPayloadSize {
        content = content[:d.config.MaxPayloadSize]
        result.Truncated = true
    }

    // Check each pattern
    for _, p := range d.patterns {
        if !d.config.EnabledCategories[p.category] {
            continue
        }

        matches := p.regex.FindAllString(content, -1)
        for _, match := range matches {
            // Validate if validator exists
            if p.validate != nil && !p.validate(match) {
                continue
            }

            detection := Detection{
                Type:            p.name,
                Category:        p.category,
                Severity:        p.severity,
                Location:        location,
                IsLikelyExample: d.isKnownExample(match, p.examples),
            }
            result.Detections = append(result.Detections, detection)
        }
    }

    // Check file paths
    d.scanFilePaths(content, location, result)

    // Check entropy
    if d.config.EnabledCategories["high_entropy"] {
        d.scanHighEntropy(content, location, result)
    }
}
```

## Step 2: Add Built-in Patterns

```go
// internal/security/patterns.go
package security

import "regexp"

func (d *Detector) loadBuiltinPatterns() {
    d.patterns = []*compiledPattern{
        // Tier 1 - Critical: Cloud Credentials
        {
            name:     "aws_access_key",
            regex:    regexp.MustCompile(`(A3T[A-Z0-9]|AKIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[A-Z0-9]{16}`),
            category: "cloud_credentials",
            severity: "critical",
            examples: []string{"AKIAIOSFODNN7EXAMPLE"},
        },
        {
            name:     "gcp_api_key",
            regex:    regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),
            category: "cloud_credentials",
            severity: "critical",
        },

        // Tier 1 - Critical: Private Keys
        {
            name:     "rsa_private_key",
            regex:    regexp.MustCompile(`-----BEGIN RSA PRIVATE KEY-----`),
            category: "private_key",
            severity: "critical",
        },
        {
            name:     "openssh_private_key",
            regex:    regexp.MustCompile(`-----BEGIN OPENSSH PRIVATE KEY-----`),
            category: "private_key",
            severity: "critical",
        },
        {
            name:     "generic_private_key",
            regex:    regexp.MustCompile(`-----BEGIN PRIVATE KEY-----`),
            category: "private_key",
            severity: "critical",
        },

        // Tier 2 - High: API Tokens
        {
            name:     "github_pat",
            regex:    regexp.MustCompile(`ghp_[0-9a-zA-Z]{36}`),
            category: "api_token",
            severity: "high",
        },
        {
            name:     "gitlab_pat",
            regex:    regexp.MustCompile(`glpat-[0-9a-zA-Z\-_]{20}`),
            category: "api_token",
            severity: "high",
        },
        {
            name:     "stripe_live_key",
            regex:    regexp.MustCompile(`sk_live_[0-9a-zA-Z]{24,}`),
            category: "api_token",
            severity: "high",
        },
        {
            name:     "slack_token",
            regex:    regexp.MustCompile(`xox[bpras]-[0-9]{10,13}-[0-9]{10,13}-[a-zA-Z0-9]{24}`),
            category: "api_token",
            severity: "high",
        },

        // Tier 3 - Medium: Credit Cards (with Luhn validation)
        {
            name:     "credit_card",
            regex:    regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`),
            category: "credit_card",
            severity: "medium",
            validate: LuhnValid,
            examples: []string{"4111111111111111", "4242424242424242"},
        },

        // JWT Tokens
        {
            name:     "jwt_token",
            regex:    regexp.MustCompile(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*`),
            category: "auth_token",
            severity: "high",
        },
    }
}
```

## Step 3: Add Luhn Validation

```go
// internal/security/luhn.go
package security

import "regexp"

var nonDigit = regexp.MustCompile(`\D`)

// LuhnValid validates credit card numbers using Luhn algorithm
func LuhnValid(number string) bool {
    digits := nonDigit.ReplaceAllString(number, "")
    if len(digits) < 13 || len(digits) > 19 {
        return false
    }

    sum := 0
    alt := false
    for i := len(digits) - 1; i >= 0; i-- {
        n := int(digits[i] - '0')
        if alt {
            n *= 2
            if n > 9 {
                n -= 9
            }
        }
        sum += n
        alt = !alt
    }
    return sum%10 == 0
}
```

## Step 4: Add Entropy Detection

```go
// internal/security/entropy.go
package security

import (
    "math"
    "regexp"
)

var highEntropyCandidate = regexp.MustCompile(`[a-zA-Z0-9+/=_-]{20,}`)

// ShannonEntropy calculates the Shannon entropy of a string
func ShannonEntropy(s string) float64 {
    if len(s) == 0 {
        return 0
    }

    freq := make(map[rune]int)
    for _, r := range s {
        freq[r]++
    }

    var entropy float64
    length := float64(len(s))
    for _, count := range freq {
        p := float64(count) / length
        entropy -= p * math.Log2(p)
    }
    return entropy
}

func (d *Detector) scanHighEntropy(content, location string, result *Result) {
    matches := highEntropyCandidate.FindAllString(content, 10)
    for _, match := range matches {
        entropy := ShannonEntropy(match)
        if entropy > d.config.EntropyThreshold {
            result.Detections = append(result.Detections, Detection{
                Type:     "high_entropy_string",
                Category: "high_entropy",
                Severity: "medium",
                Location: location,
            })
        }
    }
}
```

## Step 5: Add File Path Detection

```go
// internal/security/paths.go
package security

import (
    "path/filepath"
    "runtime"
    "strings"
)

var sensitiveFilePaths = []struct {
    pattern  string
    category string
    severity string
    platform string // "all", "linux", "darwin", "windows"
}{
    // SSH keys
    {"*/.ssh/id_*", "ssh", "critical", "all"},
    {"*/.ssh/authorized_keys", "ssh", "high", "all"},
    {"*.pem", "ssh", "critical", "all"},
    {"*.ppk", "ssh", "critical", "all"},

    // Cloud credentials
    {"*/.aws/credentials", "cloud", "critical", "all"},
    {"*/.config/gcloud/*", "cloud", "critical", "linux"},
    {"*/Library/Application Support/gcloud/*", "cloud", "critical", "darwin"},

    // Environment files
    {".env", "env", "high", "all"},
    {".env.*", "env", "high", "all"},
    {"secrets.json", "env", "high", "all"},

    // System files
    {"/etc/shadow", "system", "critical", "linux"},
    {"/etc/passwd", "system", "high", "linux"},
}

func (d *Detector) scanFilePaths(content, location string, result *Result) {
    currentOS := runtime.GOOS

    for _, fp := range sensitiveFilePaths {
        if fp.platform != "all" && fp.platform != currentOS {
            continue
        }

        // Check if content contains the path pattern
        if matchesPathPattern(content, fp.pattern) {
            result.Detections = append(result.Detections, Detection{
                Type:     "sensitive_file_path",
                Category: "sensitive_file",
                Severity: fp.severity,
                Location: location,
            })
        }
    }
}

func matchesPathPattern(content, pattern string) bool {
    // Expand ~ and environment variables
    pattern = expandPath(pattern)

    // Check if any word in content matches the glob pattern
    words := strings.Fields(content)
    for _, word := range words {
        word = strings.Trim(word, `"'`)
        matched, _ := filepath.Match(pattern, word)
        if matched {
            return true
        }
        // Also check if word contains the pattern
        if strings.Contains(word, strings.TrimPrefix(pattern, "*")) {
            return true
        }
    }
    return false
}
```

## Step 6: Integrate with ActivityService

```go
// internal/runtime/activity_service.go (modification)

// Add to ActivityService struct
type ActivityService struct {
    // ... existing fields ...
    detector *security.Detector
}

// Add to handleToolCallCompleted
func (s *ActivityService) handleToolCallCompleted(event ToolCallCompletedEvent) {
    record := &storage.ActivityRecord{
        ID:        uuid.New().String(),
        Type:      "tool_call",
        Server:    event.ServerName,
        Tool:      event.ToolName,
        Status:    "success",
        Timestamp: event.Timestamp,
        Duration:  event.Duration,
        Metadata:  make(map[string]interface{}),
    }

    // Store arguments/response
    record.Metadata["arguments"] = event.Arguments
    record.Metadata["response"] = event.Response

    // Save record first
    s.storage.SaveActivity(record)

    // Run sensitive data detection asynchronously
    if s.detector != nil {
        go func() {
            result := s.detector.Scan(event.Arguments, event.Response)
            if result.Detected {
                s.updateActivityMetadata(record.ID, "sensitive_data_detection", result)
                // Emit event for real-time updates
                s.eventBus.Publish(Event{
                    Type: "sensitive_data.detected",
                    Data: map[string]interface{}{
                        "activity_id": record.ID,
                        "detections":  result.Detections,
                    },
                })
            }
        }()
    }
}
```

## Step 7: Add Configuration

```go
// internal/config/config.go (addition)

type SensitiveDataDetectionConfig struct {
    Enabled           bool              `json:"enabled"`
    ScanRequests      bool              `json:"scan_requests"`
    ScanResponses     bool              `json:"scan_responses"`
    MaxPayloadSizeKB  int               `json:"max_payload_size_kb"`
    EntropyThreshold  float64           `json:"entropy_threshold"`
    Categories        map[string]bool   `json:"categories"`
    CustomPatterns    []CustomPattern   `json:"custom_patterns,omitempty"`
    SensitiveKeywords []string          `json:"sensitive_keywords,omitempty"`
}

type CustomPattern struct {
    Name     string   `json:"name"`
    Regex    string   `json:"regex,omitempty"`
    Keywords []string `json:"keywords,omitempty"`
    Severity string   `json:"severity"`
    Category string   `json:"category,omitempty"`
}

// Add to Config struct
type Config struct {
    // ... existing fields ...
    SensitiveDataDetection *SensitiveDataDetectionConfig `json:"sensitive_data_detection,omitempty"`
}

// Add default
func defaultSensitiveDataConfig() *SensitiveDataDetectionConfig {
    return &SensitiveDataDetectionConfig{
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
    }
}
```

## Step 8: Write Basic Tests

```go
// internal/security/detector_test.go
package security

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestDetector_AWSKey(t *testing.T) {
    d := NewDetector(&DetectionConfig{
        Enabled:          true,
        ScanRequests:     true,
        MaxPayloadSize:   1024 * 1024,
        EnabledCategories: map[string]bool{"cloud_credentials": true},
    })

    result := d.Scan(`{"key": "AKIAIOSFODNN7EXAMPLE"}`, "")

    assert.True(t, result.Detected)
    assert.Len(t, result.Detections, 1)
    assert.Equal(t, "aws_access_key", result.Detections[0].Type)
    assert.Equal(t, "critical", result.Detections[0].Severity)
    assert.True(t, result.Detections[0].IsLikelyExample)
}

func TestDetector_PrivateKey(t *testing.T) {
    d := NewDetector(&DetectionConfig{
        Enabled:          true,
        ScanRequests:     true,
        MaxPayloadSize:   1024 * 1024,
        EnabledCategories: map[string]bool{"private_key": true},
    })

    result := d.Scan("-----BEGIN RSA PRIVATE KEY-----\nMIIE...", "")

    assert.True(t, result.Detected)
    assert.Equal(t, "rsa_private_key", result.Detections[0].Type)
    assert.Equal(t, "critical", result.Detections[0].Severity)
}

func TestLuhnValid(t *testing.T) {
    tests := []struct {
        number string
        valid  bool
    }{
        {"4111111111111111", true},
        {"4242424242424242", true},
        {"5555555555554444", true},
        {"1234567890123456", false},
        {"not a number", false},
    }

    for _, tc := range tests {
        t.Run(tc.number, func(t *testing.T) {
            assert.Equal(t, tc.valid, LuhnValid(tc.number))
        })
    }
}

func TestShannonEntropy(t *testing.T) {
    // Low entropy (repeated chars)
    low := ShannonEntropy("aaaaaaaaaa")
    assert.Less(t, low, 1.0)

    // High entropy (random-like)
    high := ShannonEntropy("aB3cD4eF5gH6iJ7kL8mN9oP0")
    assert.Greater(t, high, 4.0)
}
```

## Step 9: Test Manually

```bash
# Build MCPProxy
make build

# Start server
./mcpproxy serve --log-level=debug

# Make a test tool call (via curl or MCP client)
# The activity log should show sensitive_data_detection in metadata

# Check activity log
./mcpproxy activity list
./mcpproxy activity show <activity-id>
```

## Next Steps

1. Add CLI filter flags (`--sensitive-data`, `--severity`)
2. Add REST API query parameters
3. Add Web UI detection indicators
4. Add custom pattern loading
5. Add comprehensive cross-platform file path detection

## References

- [spec.md](./spec.md) - Full feature specification
- [research.md](./research.md) - Pattern sources and tool analysis
- [data-model.md](./data-model.md) - Complete type definitions
