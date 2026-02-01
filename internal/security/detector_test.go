package security

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDetector(t *testing.T) {
	t.Run("with nil config uses defaults", func(t *testing.T) {
		detector := NewDetector(nil)
		require.NotNil(t, detector)
		assert.NotNil(t, detector.config)
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := &config.SensitiveDataDetectionConfig{
			Enabled:      true,
			ScanRequests: true,
		}
		detector := NewDetector(cfg)
		require.NotNil(t, detector)
		assert.True(t, detector.config.Enabled)
	})
}

func TestDetector_Scan_Disabled(t *testing.T) {
	cfg := &config.SensitiveDataDetectionConfig{
		Enabled: false,
	}
	detector := NewDetector(cfg)

	result := detector.Scan("some arguments", "some response")

	assert.False(t, result.Detected)
	assert.Empty(t, result.Detections)
}

func TestDetector_Scan_EmptyContent(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = true
	detector := NewDetector(cfg)

	result := detector.Scan("", "")

	assert.False(t, result.Detected)
	assert.Empty(t, result.Detections)
}

func TestDetector_Scan_Truncation(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = true
	cfg.ScanRequests = true
	cfg.MaxPayloadSizeKB = 1 // 1KB limit
	detector := NewDetector(cfg)

	// Create content larger than 1KB
	largeContent := make([]byte, 2*1024)
	for i := range largeContent {
		largeContent[i] = 'a'
	}

	result := detector.Scan(string(largeContent), "")

	assert.True(t, result.Truncated)
}

func TestDetector_Scan_DurationTracking(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = true
	detector := NewDetector(cfg)

	result := detector.Scan("test content", "test response")

	assert.GreaterOrEqual(t, result.ScanDurationMs, int64(0))
}

func TestDetector_Scan_MaxDetections(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = true
	detector := NewDetector(cfg)

	// Even with many potential matches, result should be capped at MaxDetectionsPerScan
	result := detector.Scan("test", "test")

	assert.LessOrEqual(t, len(result.Detections), MaxDetectionsPerScan)
}

func TestDetector_ReloadConfig(t *testing.T) {
	detector := NewDetector(nil)

	newCfg := &config.SensitiveDataDetectionConfig{
		Enabled:          true,
		ScanRequests:     true,
		ScanResponses:    false,
		EntropyThreshold: 5.0,
	}

	detector.ReloadConfig(newCfg)

	assert.True(t, detector.config.Enabled)
	assert.True(t, detector.config.ScanRequests)
	assert.False(t, detector.config.ScanResponses)
	assert.Equal(t, 5.0, detector.config.EntropyThreshold)
}

func TestDetector_ReloadConfig_NilConfig(t *testing.T) {
	cfg := &config.SensitiveDataDetectionConfig{
		Enabled: true,
	}
	detector := NewDetector(cfg)

	detector.ReloadConfig(nil)

	// Should use defaults
	assert.NotNil(t, detector.config)
}

func TestResult_AddDetection(t *testing.T) {
	result := NewResult()
	assert.False(t, result.Detected)
	assert.Empty(t, result.Detections)

	detection := Detection{
		Type:     "aws_access_key",
		Category: "cloud_credentials",
		Severity: "critical",
		Location: "arguments",
	}

	result.AddDetection(detection)

	assert.True(t, result.Detected)
	require.Len(t, result.Detections, 1)
	assert.Equal(t, "aws_access_key", result.Detections[0].Type)
}

func TestResult_AddDetection_Multiple(t *testing.T) {
	result := NewResult()

	for i := 0; i < 5; i++ {
		result.AddDetection(Detection{
			Type:     "test_type",
			Category: "test_category",
			Severity: "medium",
			Location: "arguments",
		})
	}

	assert.True(t, result.Detected)
	assert.Len(t, result.Detections, 5)
}

// Integration tests for pattern detection
func TestDetector_PatternDetection(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		wantDetected   bool
		wantType       string
		wantCategory   string
		wantSeverity   string
		disableCategory string
	}{
		{
			name:         "AWS access key",
			content:      `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`,
			wantDetected: true,
			wantType:     "aws_access_key",
			wantCategory: "cloud_credentials",
			wantSeverity: "critical",
		},
		{
			name:         "GitHub PAT classic",
			content:      "Token: ghp_1234567890abcdefghijABCDEFGHIJ123456",
			wantDetected: true,
			wantType:     "github_pat",
			wantCategory: "api_token",
			wantSeverity: "critical",
		},
		{
			name:         "RSA private key",
			content:      "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----",
			wantDetected: true,
			wantType:     "rsa_private_key",
			wantCategory: "private_key",
			wantSeverity: "critical",
		},
		{
			name:         "PostgreSQL connection string",
			content:      "postgresql://user:password123@localhost:5432/mydb",
			wantDetected: true,
			wantType:     "postgres_connection",
			wantCategory: "database_credential",
			wantSeverity: "critical",
		},
		{
			name:         "Credit card (test card)",
			content:      "Card: 4111111111111111",
			wantDetected: true,
			wantType:     "credit_card",
			wantCategory: "credit_card",
			wantSeverity: "critical",
		},
		{
			name:         "JWT token",
			content:      "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			wantDetected: true,
			wantType:     "jwt_token",
			wantCategory: "auth_token",
			wantSeverity: "high",
		},
		{
			name:         "Category disabled",
			content:      `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`,
			wantDetected: false,
			disableCategory: "cloud_credentials",
		},
		{
			name:         "No sensitive data",
			content:      "Hello, this is a normal message with no secrets.",
			wantDetected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultSensitiveDataDetectionConfig()
			cfg.Enabled = true
			cfg.ScanRequests = true
			cfg.ScanResponses = true

			if tt.disableCategory != "" {
				cfg.Categories[tt.disableCategory] = false
			}

			detector := NewDetector(cfg)
			result := detector.Scan(tt.content, "")

			assert.Equal(t, tt.wantDetected, result.Detected, "detection mismatch")

			if tt.wantDetected && len(result.Detections) > 0 {
				found := false
				for _, d := range result.Detections {
					if d.Type == tt.wantType {
						found = true
						assert.Equal(t, tt.wantCategory, d.Category, "category mismatch")
						assert.Equal(t, tt.wantSeverity, d.Severity, "severity mismatch")
						break
					}
				}
				assert.True(t, found, "expected pattern %s not found in detections: %v", tt.wantType, result.Detections)
			}
		})
	}
}

// Table-driven tests for edge cases
func TestDetector_Scan_EdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		arguments     string
		response      string
		scanRequests  bool
		scanResponses bool
		wantDetected  bool
	}{
		{
			name:          "scan both enabled, empty content",
			arguments:     "",
			response:      "",
			scanRequests:  true,
			scanResponses: true,
			wantDetected:  false,
		},
		{
			name:          "only scan requests enabled",
			arguments:     "test content",
			response:      "test content",
			scanRequests:  true,
			scanResponses: false,
			wantDetected:  false, // No patterns loaded yet
		},
		{
			name:          "only scan responses enabled",
			arguments:     "test content",
			response:      "test content",
			scanRequests:  false,
			scanResponses: true,
			wantDetected:  false, // No patterns loaded yet
		},
		{
			name:          "unicode content",
			arguments:     "测试内容 🔑 テスト",
			response:      "Ответ данных",
			scanRequests:  true,
			scanResponses: true,
			wantDetected:  false,
		},
		{
			name:          "null bytes in content",
			arguments:     "test\x00content",
			response:      "test\x00response",
			scanRequests:  true,
			scanResponses: true,
			wantDetected:  false,
		},
		{
			name:          "very long single line",
			arguments:     string(make([]byte, 10000)),
			response:      "",
			scanRequests:  true,
			scanResponses: false,
			wantDetected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultSensitiveDataDetectionConfig()
			cfg.Enabled = true
			cfg.ScanRequests = tt.scanRequests
			cfg.ScanResponses = tt.scanResponses
			detector := NewDetector(cfg)

			result := detector.Scan(tt.arguments, tt.response)

			assert.Equal(t, tt.wantDetected, result.Detected)
		})
	}
}
