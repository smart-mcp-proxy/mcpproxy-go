package security

import (
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/patterns"
)

const (
	// MaxDetectionsPerScan limits the number of detections per scan
	MaxDetectionsPerScan = 50
)

// Detector scans data for sensitive information
type Detector struct {
	patterns      []*Pattern
	filePatterns  []*FilePathPattern
	customPatterns []*Pattern
	config        *config.SensitiveDataDetectionConfig
	mu            sync.RWMutex
}

// NewDetector creates a new detector with the given configuration
func NewDetector(cfg *config.SensitiveDataDetectionConfig) *Detector {
	if cfg == nil {
		cfg = config.DefaultSensitiveDataDetectionConfig()
	}

	d := &Detector{
		config: cfg,
	}
	d.loadBuiltinPatterns()
	d.loadFilePathPatterns()
	d.loadCustomPatterns()
	return d
}

// Scan checks data for sensitive information
func (d *Detector) Scan(arguments, response string) *Result {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.config.IsEnabled() {
		return &Result{Detected: false}
	}

	start := time.Now()
	result := NewResult()

	// Scan arguments
	if d.config.ScanRequests && arguments != "" {
		d.scanContent(arguments, "arguments", result)
	}

	// Scan response
	if d.config.ScanResponses && response != "" {
		d.scanContent(response, "response", result)
	}

	result.ScanDurationMs = time.Since(start).Milliseconds()
	return result
}

// scanContent scans content for sensitive data
func (d *Detector) scanContent(content, location string, result *Result) {
	// Truncate if needed
	maxSize := d.config.GetMaxPayloadSize()
	if len(content) > maxSize {
		content = content[:maxSize]
		result.Truncated = true
	}

	// Check regex patterns
	d.scanPatterns(content, location, result)

	// Check file paths
	d.scanFilePaths(content, location, result)

	// Check high-entropy strings
	if d.config.IsCategoryEnabled("high_entropy") {
		d.scanHighEntropy(content, location, result)
	}
}

// scanPatterns checks content against all regex patterns
func (d *Detector) scanPatterns(content, location string, result *Result) {
	allPatterns := append(d.patterns, d.customPatterns...)

	for _, pattern := range allPatterns {
		if len(result.Detections) >= MaxDetectionsPerScan {
			break
		}

		// Check if category is enabled
		if !d.config.IsCategoryEnabled(string(pattern.Category)) {
			continue
		}

		matches := pattern.Match(content)
		for _, match := range matches {
			if len(result.Detections) >= MaxDetectionsPerScan {
				break
			}

			// Validate if validator exists
			if !pattern.IsValid(match) {
				continue
			}

			detection := Detection{
				Type:            pattern.Name,
				Category:        string(pattern.Category),
				Severity:        string(pattern.Severity),
				Location:        location,
				IsLikelyExample: pattern.IsKnownExample(match),
			}
			result.AddDetection(detection)
		}
	}
}

// scanFilePaths checks for sensitive file path access
func (d *Detector) scanFilePaths(content, location string, result *Result) {
	if !d.config.IsCategoryEnabled("sensitive_file") {
		return
	}

	for _, fp := range d.filePatterns {
		if len(result.Detections) >= MaxDetectionsPerScan {
			break
		}

		// Check platform compatibility
		if !IsPlatformMatch(fp.Platform) {
			continue
		}

		// Check each pattern
		for _, pattern := range fp.Patterns {
			if MatchesPathPattern(content, pattern) {
				detection := Detection{
					Type:     fp.Name,
					Category: "sensitive_file",
					Severity: string(fp.Severity),
					Location: location,
				}
				result.AddDetection(detection)
				break // One match per file pattern is enough
			}
		}
	}
}

// scanHighEntropy checks for high-entropy strings
func (d *Detector) scanHighEntropy(content, location string, result *Result) {
	threshold := d.config.GetEntropyThreshold()
	matches := FindHighEntropyStrings(content, threshold, 5)

	for _, match := range matches {
		if len(result.Detections) >= MaxDetectionsPerScan {
			break
		}

		// Skip if it looks like a known pattern (already detected)
		if d.isAlreadyDetected(match, result) {
			continue
		}

		detection := Detection{
			Type:     "high_entropy_string",
			Category: "high_entropy",
			Severity: string(SeverityMedium),
			Location: location,
		}
		result.AddDetection(detection)
	}
}

// isAlreadyDetected checks if a string was already detected by another pattern
func (d *Detector) isAlreadyDetected(s string, result *Result) bool {
	for _, pattern := range d.patterns {
		matches := pattern.Match(s)
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

// ReloadConfig reloads the detector configuration
func (d *Detector) ReloadConfig(cfg *config.SensitiveDataDetectionConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if cfg == nil {
		cfg = config.DefaultSensitiveDataDetectionConfig()
	}

	d.config = cfg
	d.loadCustomPatterns() // Reload custom patterns
}

// loadBuiltinPatterns loads all built-in detection patterns
func (d *Detector) loadBuiltinPatterns() {
	d.patterns = make([]*Pattern, 0)

	// Load patterns from subpackages and convert to security.Pattern
	d.patterns = append(d.patterns, convertPatterns(patterns.GetCloudPatterns())...)
	d.patterns = append(d.patterns, convertPatterns(patterns.GetKeyPatterns())...)
	d.patterns = append(d.patterns, convertPatterns(patterns.GetTokenPatterns())...)
	d.patterns = append(d.patterns, convertPatterns(patterns.GetDatabasePatterns())...)
	d.patterns = append(d.patterns, convertPatterns(patterns.GetCreditCardPatterns())...)
}

// convertPatterns converts patterns.Pattern slice to security.Pattern slice
func convertPatterns(pats []*patterns.Pattern) []*Pattern {
	result := make([]*Pattern, len(pats))
	for i, p := range pats {
		result[i] = convertPattern(p)
	}
	return result
}

// convertPattern converts a patterns.Pattern to a security.Pattern
func convertPattern(p *patterns.Pattern) *Pattern {
	return &Pattern{
		Name:        p.Name,
		Description: p.Description,
		Category:    Category(p.Category),
		Severity:    Severity(p.Severity),
		// Delegate Match() and IsKnownExample() to the original patterns.Pattern
		// which already handles validator filtering and normalization
		delegate: p,
	}
}

// loadFilePathPatterns loads file path detection patterns
func (d *Detector) loadFilePathPatterns() {
	d.filePatterns = GetFilePathPatterns()
}

// loadCustomPatterns loads user-defined patterns from config
func (d *Detector) loadCustomPatterns() {
	d.customPatterns = make([]*Pattern, 0)

	if d.config == nil || len(d.config.CustomPatterns) == 0 {
		return
	}

	for _, cp := range d.config.CustomPatterns {
		pattern := buildCustomPattern(cp)
		if pattern != nil {
			d.customPatterns = append(d.customPatterns, pattern)
		}
	}

	// Also add keyword patterns
	if len(d.config.SensitiveKeywords) > 0 {
		keywordPattern := NewPattern("sensitive_keyword").
			WithKeywords(d.config.SensitiveKeywords...).
			WithCategory(CategoryCustom).
			WithSeverity(SeverityLow).
			Build()
		d.customPatterns = append(d.customPatterns, keywordPattern)
	}
}

// buildCustomPattern builds a Pattern from a CustomPattern config
func buildCustomPattern(cp config.CustomPattern) *Pattern {
	if cp.Name == "" {
		return nil
	}

	builder := NewPattern(cp.Name)

	// Set pattern (regex or keywords)
	if cp.Regex != "" {
		builder.WithRegex(cp.Regex)
	} else if len(cp.Keywords) > 0 {
		builder.WithKeywords(cp.Keywords...)
	} else {
		return nil // No pattern defined
	}

	// Set category
	category := CategoryCustom
	if cp.Category != "" {
		category = Category(cp.Category)
	}
	builder.WithCategory(category)

	// Set severity
	severity := SeverityMedium
	switch strings.ToLower(cp.Severity) {
	case "critical":
		severity = SeverityCritical
	case "high":
		severity = SeverityHigh
	case "medium":
		severity = SeverityMedium
	case "low":
		severity = SeverityLow
	}
	builder.WithSeverity(severity)

	return builder.Build()
}

