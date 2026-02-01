package patterns

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test GitHub Token patterns
func TestGitHubTokenPatterns(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		patternName string
	}{
		// GitHub Personal Access Token (classic)
		{
			name:        "GitHub classic PAT",
			input:       "ghp_1234567890abcdefghijABCDEFGHIJ123456",
			wantMatch:   true,
			patternName: "github_pat",
		},
		// GitHub Fine-grained PAT
		{
			name:        "GitHub fine-grained PAT",
			input:       "github_pat_11ABCDEFG_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO",
			wantMatch:   true,
			patternName: "github_pat",
		},
		// GitHub OAuth token
		{
			name:        "GitHub OAuth token",
			input:       "gho_1234567890abcdefghijABCDEFGHIJ123456",
			wantMatch:   true,
			patternName: "github_oauth",
		},
		// GitHub App token
		{
			name:        "GitHub App installation token",
			input:       "ghs_1234567890abcdefghijABCDEFGHIJ123456",
			wantMatch:   true,
			patternName: "github_app",
		},
		// GitHub App refresh token
		{
			name:        "GitHub App refresh token",
			input:       "ghr_1234567890abcdefghijABCDEFGHIJ123456",
			wantMatch:   true,
			patternName: "github_refresh",
		},
		// Invalid tokens
		{
			name:        "too short",
			input:       "ghp_12345",
			wantMatch:   false,
			patternName: "github_pat",
		},
		{
			name:        "wrong prefix",
			input:       "ghx_1234567890abcdefghijABCDEFGHIJ123456",
			wantMatch:   false,
			patternName: "github_pat",
		},
	}

	patterns := GetTokenPatterns()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := findPatternByName(patterns, tt.patternName)
			if pattern == nil {
				t.Skipf("%s pattern not implemented yet", tt.patternName)
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test GitLab Token patterns
func TestGitLabTokenPatterns(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "GitLab personal access token",
			input:     "glpat-xxxxxxxxxxxxxxxxxxxx",
			wantMatch: true,
		},
		{
			name:      "GitLab PAT in config",
			input:     `GITLAB_TOKEN=glpat-xxxxxxxxxxxxxxxxxxxx`,
			wantMatch: true,
		},
		{
			name:      "old format GitLab token (20 chars)",
			input:     "gitlab-token-12345678901234567890",
			wantMatch: false, // Old format not supported
		},
	}

	patterns := GetTokenPatterns()
	gitlabPattern := findPatternByName(patterns, "gitlab_pat")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gitlabPattern == nil {
				t.Skip("GitLab PAT pattern not implemented yet")
				return
			}
			matches := gitlabPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Stripe API Key patterns
// buildStripeTestKey constructs a test Stripe key dynamically to avoid triggering secret scanners
func buildStripeTestKey(prefix, mode string) string {
	// Build: prefix_mode_<24 chars>
	return prefix + "_" + mode + "_" + strings.Repeat("a", 24)
}

func TestStripeAPIKeyPatterns(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		patternName string
	}{
		{
			name:        "Stripe live secret key",
			input:       buildStripeTestKey("sk", "live"),
			wantMatch:   true,
			patternName: "stripe_key",
		},
		{
			name:        "Stripe test secret key",
			input:       buildStripeTestKey("sk", "test"),
			wantMatch:   true,
			patternName: "stripe_key",
		},
		{
			name:        "Stripe live publishable key",
			input:       buildStripeTestKey("pk", "live"),
			wantMatch:   true,
			patternName: "stripe_key",
		},
		{
			name:        "Stripe restricted key",
			input:       buildStripeTestKey("rk", "test"),
			wantMatch:   true,
			patternName: "stripe_key",
		},
		{
			name:        "too short",
			input:       "sk" + "_" + "live" + "_12345",
			wantMatch:   false,
			patternName: "stripe_key",
		},
	}

	patterns := GetTokenPatterns()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := findPatternByName(patterns, tt.patternName)
			if pattern == nil {
				t.Skipf("%s pattern not implemented yet", tt.patternName)
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// buildSlackBotToken constructs a test Slack bot token dynamically
func buildSlackBotToken() string {
	return "xoxb" + "-" + strings.Repeat("9", 12) + "-" + strings.Repeat("9", 13) + "-" + "abcdefghijklmnopqrstuvwx"
}

// buildSlackUserToken constructs a test Slack user token dynamically
func buildSlackUserToken() string {
	return "xoxp" + "-" + strings.Repeat("9", 12) + "-" + strings.Repeat("9", 12) + "-" + strings.Repeat("9", 12) + "-" + "abcdefghijklmnopqrstuvwxyz12"
}

// buildSlackAppToken constructs a test Slack app token dynamically
func buildSlackAppToken() string {
	return "xapp" + "-9-A" + strings.Repeat("9", 10) + "-" + strings.Repeat("9", 13) + "-" + strings.Repeat("abcdefghijkl", 8)
}

// buildSlackWebhookURL constructs a test Slack webhook URL dynamically
func buildSlackWebhookURL() string {
	return "https://hooks.slack.com/services/T" + strings.Repeat("9", 8) + "/B" + strings.Repeat("9", 8) + "/abcdefghijklmnopqrstuvwx"
}

// Test Slack Token patterns
func TestSlackTokenPatterns(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Slack bot token",
			input:     buildSlackBotToken(),
			wantMatch: true,
		},
		{
			name:      "Slack user token",
			input:     buildSlackUserToken(),
			wantMatch: true,
		},
		{
			name:      "Slack app token",
			input:     buildSlackAppToken(),
			wantMatch: true,
		},
		{
			name:      "Slack webhook URL",
			input:     buildSlackWebhookURL(),
			wantMatch: true,
		},
		{
			name:      "invalid prefix",
			input:     "xoxz" + "-123456789012-1234567890123-abcdefghijklmnopqrstuvwx",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	slackPattern := findPatternByName(patterns, "slack_token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if slackPattern == nil {
				t.Skip("Slack token pattern not implemented yet")
				return
			}
			matches := slackPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test SendGrid API Key patterns
func TestSendGridAPIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "SendGrid API key",
			input:     "SG.abcdefghij1234567890.abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGH",
			wantMatch: true,
		},
		{
			name:      "SendGrid key in config",
			input:     `SENDGRID_API_KEY=SG.abcdefghij1234567890.abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGH`,
			wantMatch: true,
		},
		{
			name:      "not a SendGrid key",
			input:     "SG.short",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	sgPattern := findPatternByName(patterns, "sendgrid_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if sgPattern == nil {
				t.Skip("SendGrid API key pattern not implemented yet")
				return
			}
			matches := sgPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test OpenAI API Key pattern
func TestOpenAIAPIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "OpenAI API key",
			input:     "sk-proj-abcdefghij1234567890abcdefghij1234567890abcd",
			wantMatch: true,
		},
		{
			name:      "OpenAI key old format",
			input:     "sk-1234567890abcdefghijklmnopqrstuvwxyz12345678",
			wantMatch: true,
		},
		{
			name:      "OpenAI key in env",
			input:     "OPENAI_API_KEY=sk-proj-abcdefghij1234567890abcdefghij1234567890abcd",
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "sk-12345",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	openaiPattern := findPatternByName(patterns, "openai_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if openaiPattern == nil {
				t.Skip("OpenAI API key pattern not implemented yet")
				return
			}
			matches := openaiPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Anthropic API Key pattern
func TestAnthropicAPIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Anthropic API key",
			input:     "sk-ant-api03-abcdefghij1234567890abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJ-abcdefghij",
			wantMatch: true,
		},
		{
			name:      "Anthropic key in env",
			input:     "ANTHROPIC_API_KEY=sk-ant-api03-abcdefghij1234567890abcdefghij",
			wantMatch: true,
		},
		{
			name:      "not Anthropic key",
			input:     "sk-ant-12345",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	anthropicPattern := findPatternByName(patterns, "anthropic_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if anthropicPattern == nil {
				t.Skip("Anthropic API key pattern not implemented yet")
				return
			}
			matches := anthropicPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test JWT Token pattern
func TestJWTTokenPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "valid JWT",
			input:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			wantMatch: true,
		},
		{
			name:      "JWT in Authorization header",
			input:     "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			wantMatch: true,
		},
		{
			name:      "not a JWT (missing parts)",
			input:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			wantMatch: false,
		},
		{
			name:      "not a JWT (random string)",
			input:     "abc.def.ghi",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	jwtPattern := findPatternByName(patterns, "jwt_token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if jwtPattern == nil {
				t.Skip("JWT token pattern not implemented yet")
				return
			}
			matches := jwtPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Bearer Token pattern (generic)
func TestBearerTokenPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Bearer token in header",
			input:     "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456",
			wantMatch: true,
		},
		{
			name:      "bearer lowercase",
			input:     "authorization: bearer abcdefghijklmnopqrstuvwxyz123456",
			wantMatch: true,
		},
		{
			name:      "no bearer keyword",
			input:     "Authorization: Basic dXNlcjpwYXNz",
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	bearerPattern := findPatternByName(patterns, "bearer_token")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bearerPattern == nil {
				t.Skip("Bearer token pattern not implemented yet")
				return
			}
			matches := bearerPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Helper functions for building test keys dynamically to avoid secret scanners

// buildGoogleAIKey constructs a test Google AI key dynamically
func buildGoogleAIKey() string {
	return "AIzaSy" + strings.Repeat("a", 33)
}

// buildXAIKey constructs a test xAI key dynamically
func buildXAIKey() string {
	return "xai-" + strings.Repeat("a", 48)
}

// buildGroqKey constructs a test Groq key dynamically
func buildGroqKey() string {
	return "gsk_" + strings.Repeat("a", 48)
}

// buildHuggingFaceToken constructs a test Hugging Face token dynamically
func buildHuggingFaceToken() string {
	return "hf_" + strings.Repeat("a", 34)
}

// buildHuggingFaceOrgToken constructs a test Hugging Face org token dynamically
func buildHuggingFaceOrgToken() string {
	return "api_org_" + strings.Repeat("a", 34)
}

// buildReplicateKey constructs a test Replicate key dynamically
func buildReplicateKey() string {
	return "r8_" + strings.Repeat("a", 37)
}

// buildPerplexityKey constructs a test Perplexity key dynamically
func buildPerplexityKey() string {
	return "pplx-" + strings.Repeat("a", 48)
}

// buildFireworksKey constructs a test Fireworks key dynamically
func buildFireworksKey() string {
	return "fw_" + strings.Repeat("a", 24)
}

// buildAnyscaleKey constructs a test Anyscale key dynamically
func buildAnyscaleKey() string {
	return "esecret_" + strings.Repeat("a", 24)
}

// Test Google AI / Gemini API Key pattern
func TestGoogleAIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Google AI API key",
			input:     buildGoogleAIKey(),
			wantMatch: true,
		},
		{
			name:      "Google AI key in config",
			input:     "GOOGLE_API_KEY=" + buildGoogleAIKey(),
			wantMatch: true,
		},
		{
			name:      "wrong prefix",
			input:     "AIzaXy" + strings.Repeat("a", 33),
			wantMatch: false,
		},
		{
			name:      "too short",
			input:     "AIzaSy" + strings.Repeat("a", 10),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "google_ai_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Google AI key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test xAI / Grok API Key pattern
func TestXAIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "xAI API key",
			input:     buildXAIKey(),
			wantMatch: true,
		},
		{
			name:      "xAI key in env",
			input:     "XAI_API_KEY=" + buildXAIKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "xai-" + strings.Repeat("a", 20),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "xai_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("xAI key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Groq API Key pattern
func TestGroqKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Groq API key",
			input:     buildGroqKey(),
			wantMatch: true,
		},
		{
			name:      "Groq key in env",
			input:     "GROQ_API_KEY=" + buildGroqKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "gsk_" + strings.Repeat("a", 20),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "groq_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Groq key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Hugging Face Token patterns
func TestHuggingFaceTokenPatterns(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		patternName string
	}{
		{
			name:        "Hugging Face user token",
			input:       buildHuggingFaceToken(),
			wantMatch:   true,
			patternName: "huggingface_token",
		},
		{
			name:        "Hugging Face org token",
			input:       buildHuggingFaceOrgToken(),
			wantMatch:   true,
			patternName: "huggingface_org_token",
		},
		{
			name:        "HF token in env",
			input:       "HF_TOKEN=" + buildHuggingFaceToken(),
			wantMatch:   true,
			patternName: "huggingface_token",
		},
		{
			name:        "too short user token",
			input:       "hf_" + strings.Repeat("a", 10),
			wantMatch:   false,
			patternName: "huggingface_token",
		},
	}

	patterns := GetTokenPatterns()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := findPatternByName(patterns, tt.patternName)
			if pattern == nil {
				t.Skipf("%s pattern not implemented yet", tt.patternName)
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Replicate API Key pattern
func TestReplicateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Replicate API key",
			input:     buildReplicateKey(),
			wantMatch: true,
		},
		{
			name:      "Replicate key in env",
			input:     "REPLICATE_API_TOKEN=" + buildReplicateKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "r8_" + strings.Repeat("a", 10),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "replicate_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Replicate key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Perplexity API Key pattern
func TestPerplexityKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Perplexity API key",
			input:     buildPerplexityKey(),
			wantMatch: true,
		},
		{
			name:      "Perplexity key in env",
			input:     "PERPLEXITY_API_KEY=" + buildPerplexityKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "pplx-" + strings.Repeat("a", 20),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "perplexity_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Perplexity key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Fireworks AI API Key pattern
func TestFireworksKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Fireworks API key",
			input:     buildFireworksKey(),
			wantMatch: true,
		},
		{
			name:      "Fireworks key in env",
			input:     "FIREWORKS_API_KEY=" + buildFireworksKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "fw_" + strings.Repeat("a", 10),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "fireworks_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Fireworks key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Anyscale API Key pattern
func TestAnyscaleKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Anyscale API key",
			input:     buildAnyscaleKey(),
			wantMatch: true,
		},
		{
			name:      "Anyscale key in env",
			input:     "ANYSCALE_API_KEY=" + buildAnyscaleKey(),
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "esecret_" + strings.Repeat("a", 10),
			wantMatch: false,
		},
	}

	patterns := GetTokenPatterns()
	pattern := findPatternByName(patterns, "anyscale_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pattern == nil {
				t.Skip("Anyscale key pattern not implemented yet")
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test keyword-context based patterns (Mistral, Cohere, DeepSeek, Together)
func TestKeywordContextPatterns(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		patternName string
	}{
		// Mistral AI
		{
			name:        "Mistral key in env",
			input:       "MISTRAL_API_KEY=" + strings.Repeat("a", 32),
			wantMatch:   true,
			patternName: "mistral_key",
		},
		{
			name:        "Mistral key in JSON",
			input:       `"mistral": "` + strings.Repeat("a", 32) + `"`,
			wantMatch:   true,
			patternName: "mistral_key",
		},
		// Cohere
		{
			name:        "Cohere key in env",
			input:       "COHERE_API_KEY=" + strings.Repeat("a", 40),
			wantMatch:   true,
			patternName: "cohere_key",
		},
		{
			name:        "Cohere key with CO_API_KEY",
			input:       "CO_API_KEY=" + strings.Repeat("a", 40),
			wantMatch:   true,
			patternName: "cohere_key",
		},
		// DeepSeek
		{
			name:        "DeepSeek key in env",
			input:       "DEEPSEEK_API_KEY=sk-" + strings.Repeat("a", 32),
			wantMatch:   true,
			patternName: "deepseek_key",
		},
		// Together AI
		{
			name:        "Together key in env",
			input:       "TOGETHER_API_KEY=" + strings.Repeat("a", 48),
			wantMatch:   true,
			patternName: "together_key",
		},
	}

	patterns := GetTokenPatterns()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := findPatternByName(patterns, tt.patternName)
			if pattern == nil {
				t.Skipf("%s pattern not implemented yet", tt.patternName)
				return
			}
			matches := pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}
