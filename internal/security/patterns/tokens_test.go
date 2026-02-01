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

// TestLLMKeysInJSONContext tests detection of LLM API keys in JSON configuration
func TestLLMKeysInJSONContext(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		// Google AI in JSON
		{
			name:        "Google AI key in JSON config",
			input:       `{"google_api_key": "` + buildGoogleAIKey() + `"}`,
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		{
			name:        "Google AI key in nested JSON",
			input:       `{"providers": {"gemini": {"api_key": "` + buildGoogleAIKey() + `"}}}`,
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		// xAI in JSON
		{
			name:        "xAI key in JSON config",
			input:       `{"xai_api_key": "` + buildXAIKey() + `"}`,
			patternName: "xai_key",
			wantMatch:   true,
		},
		// Groq in JSON
		{
			name:        "Groq key in JSON config",
			input:       `{"groq": {"api_key": "` + buildGroqKey() + `"}}`,
			patternName: "groq_key",
			wantMatch:   true,
		},
		// Hugging Face in JSON
		{
			name:        "HuggingFace token in JSON",
			input:       `{"hf_token": "` + buildHuggingFaceToken() + `"}`,
			patternName: "huggingface_token",
			wantMatch:   true,
		},
		// Replicate in JSON
		{
			name:        "Replicate key in JSON",
			input:       `{"replicate_api_token": "` + buildReplicateKey() + `"}`,
			patternName: "replicate_key",
			wantMatch:   true,
		},
		// Perplexity in JSON
		{
			name:        "Perplexity key in JSON",
			input:       `{"perplexity_api_key": "` + buildPerplexityKey() + `"}`,
			patternName: "perplexity_key",
			wantMatch:   true,
		},
		// Fireworks in JSON
		{
			name:        "Fireworks key in JSON",
			input:       `{"fireworks_api_key": "` + buildFireworksKey() + `"}`,
			patternName: "fireworks_key",
			wantMatch:   true,
		},
		// Anyscale in JSON
		{
			name:        "Anyscale key in JSON",
			input:       `{"anyscale_api_key": "` + buildAnyscaleKey() + `"}`,
			patternName: "anyscale_key",
			wantMatch:   true,
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

// TestLLMKeysInYAMLContext tests detection of LLM API keys in YAML configuration
func TestLLMKeysInYAMLContext(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		{
			name:        "Google AI key in YAML",
			input:       "google_api_key: " + buildGoogleAIKey(),
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		{
			name:        "xAI key in YAML",
			input:       "xai_api_key: " + buildXAIKey(),
			patternName: "xai_key",
			wantMatch:   true,
		},
		{
			name:        "Groq key in YAML",
			input:       "groq_api_key: " + buildGroqKey(),
			patternName: "groq_key",
			wantMatch:   true,
		},
		{
			name:        "HuggingFace token in YAML",
			input:       "hf_token: " + buildHuggingFaceToken(),
			patternName: "huggingface_token",
			wantMatch:   true,
		},
		{
			name:        "Replicate key in YAML",
			input:       "replicate_api_token: " + buildReplicateKey(),
			patternName: "replicate_key",
			wantMatch:   true,
		},
		{
			name:        "Perplexity key in YAML",
			input:       "perplexity_api_key: " + buildPerplexityKey(),
			patternName: "perplexity_key",
			wantMatch:   true,
		},
		{
			name:        "Fireworks key in YAML",
			input:       "fireworks_api_key: " + buildFireworksKey(),
			patternName: "fireworks_key",
			wantMatch:   true,
		},
		{
			name:        "Anyscale key in YAML",
			input:       "anyscale_api_key: " + buildAnyscaleKey(),
			patternName: "anyscale_key",
			wantMatch:   true,
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

// TestLLMKeysInCodeSnippets tests detection of LLM API keys in code examples
func TestLLMKeysInCodeSnippets(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		// Python code snippets
		{
			name:        "Google AI key in Python",
			input:       `genai.configure(api_key="` + buildGoogleAIKey() + `")`,
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		{
			name:        "Groq key in Python",
			input:       `client = Groq(api_key="` + buildGroqKey() + `")`,
			patternName: "groq_key",
			wantMatch:   true,
		},
		{
			name:        "HuggingFace token in Python",
			input:       `login(token="` + buildHuggingFaceToken() + `")`,
			patternName: "huggingface_token",
			wantMatch:   true,
		},
		// JavaScript/TypeScript snippets
		{
			name:        "xAI key in JavaScript",
			input:       `const client = new XAI({ apiKey: "` + buildXAIKey() + `" });`,
			patternName: "xai_key",
			wantMatch:   true,
		},
		{
			name:        "Replicate key in JavaScript",
			input:       `const replicate = new Replicate({ auth: "` + buildReplicateKey() + `" });`,
			patternName: "replicate_key",
			wantMatch:   true,
		},
		// Shell/Bash snippets
		{
			name:        "Perplexity key in curl command",
			input:       `curl -H "Authorization: Bearer ` + buildPerplexityKey() + `"`,
			patternName: "perplexity_key",
			wantMatch:   true,
		},
		{
			name:        "Fireworks key in export",
			input:       `export FIREWORKS_API_KEY=` + buildFireworksKey(),
			patternName: "fireworks_key",
			wantMatch:   true,
		},
		// Multi-line code
		{
			name: "Anyscale key in multi-line Python",
			input: `import anyscale
client = anyscale.Client(
    api_key="` + buildAnyscaleKey() + `"
)`,
			patternName: "anyscale_key",
			wantMatch:   true,
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

// TestLLMKeysFalsePositivePrevention tests that patterns don't match false positives
func TestLLMKeysFalsePositivePrevention(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		// Google AI - wrong prefix variations
		{
			name:        "Not Google AI - wrong second char",
			input:       "AIzaXy" + strings.Repeat("a", 33),
			patternName: "google_ai_key",
			wantMatch:   false,
		},
		{
			name:        "Not Google AI - too short",
			input:       "AIzaSy" + strings.Repeat("a", 20),
			patternName: "google_ai_key",
			wantMatch:   false,
		},
		// xAI - similar prefixes
		{
			name:        "Not xAI - xai without hyphen",
			input:       "xai" + strings.Repeat("a", 48),
			patternName: "xai_key",
			wantMatch:   false,
		},
		{
			name:        "Not xAI - too short after prefix",
			input:       "xai-" + strings.Repeat("a", 30),
			patternName: "xai_key",
			wantMatch:   false,
		},
		// Groq - similar prefixes
		{
			name:        "Not Groq - gsk without underscore",
			input:       "gsk" + strings.Repeat("a", 48),
			patternName: "groq_key",
			wantMatch:   false,
		},
		{
			name:        "Not Groq - wrong length",
			input:       "gsk_" + strings.Repeat("a", 30),
			patternName: "groq_key",
			wantMatch:   false,
		},
		// HuggingFace - similar patterns
		{
			name:        "Not HuggingFace - hf without underscore",
			input:       "hf" + strings.Repeat("a", 34),
			patternName: "huggingface_token",
			wantMatch:   false,
		},
		{
			name:        "Not HuggingFace - wrong length",
			input:       "hf_" + strings.Repeat("a", 20),
			patternName: "huggingface_token",
			wantMatch:   false,
		},
		// Replicate - similar prefixes
		{
			name:        "Not Replicate - r8 without underscore",
			input:       "r8" + strings.Repeat("a", 37),
			patternName: "replicate_key",
			wantMatch:   false,
		},
		{
			name:        "Not Replicate - wrong length",
			input:       "r8_" + strings.Repeat("a", 20),
			patternName: "replicate_key",
			wantMatch:   false,
		},
		// Perplexity - similar patterns
		{
			name:        "Not Perplexity - pplx without hyphen",
			input:       "pplx" + strings.Repeat("a", 48),
			patternName: "perplexity_key",
			wantMatch:   false,
		},
		{
			name:        "Not Perplexity - wrong length",
			input:       "pplx-" + strings.Repeat("a", 30),
			patternName: "perplexity_key",
			wantMatch:   false,
		},
		// Fireworks - edge cases
		{
			name:        "Not Fireworks - fw without underscore",
			input:       "fw" + strings.Repeat("a", 24),
			patternName: "fireworks_key",
			wantMatch:   false,
		},
		// Anyscale - edge cases
		{
			name:        "Not Anyscale - esecret without underscore",
			input:       "esecret" + strings.Repeat("a", 24),
			patternName: "anyscale_key",
			wantMatch:   false,
		},
		// Random strings that should not match
		{
			name:        "Random UUID should not match Google AI",
			input:       "550e8400-e29b-41d4-a716-446655440000",
			patternName: "google_ai_key",
			wantMatch:   false,
		},
		{
			name:        "Random base64 should not match Groq",
			input:       "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3ODkw",
			patternName: "groq_key",
			wantMatch:   false,
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

// Helper functions to build mixed alphanumeric keys dynamically
func buildMixedGoogleAIKey() string {
	return "AIzaSy" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz12345" + "67"
}

func buildMixedXAIKey() string {
	return "xai-" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz1234" + "567890" + "abcdef" + "ghij12"
}

func buildMixedGroqKey() string {
	return "gsk_" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz1234" + "567890" + "abcdef" + "gh1234"
}

func buildMixedHuggingFaceToken() string {
	return "hf_" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz1234" + "5678"
}

func buildMixedReplicateKey() string {
	return "r8_" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz1234" + "567890a"
}

func buildMixedPerplexityKey() string {
	return "pplx-" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "StUvWx" + "Yz1234" + "567890" + "abcdef" + "gh1234"
}

func buildMixedFireworksKey() string {
	return "fw_" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "1234"
}

func buildMixedAnyscaleKey() string {
	return "esecret_" + "AbCdEf" + "GhIjKl" + "MnOpQr" + "1234"
}

// TestLLMKeysWithMixedAlphanumeric tests keys with realistic mixed character patterns
func TestLLMKeysWithMixedAlphanumeric(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		// Google AI with mixed case
		{
			name:        "Google AI key with mixed alphanumeric",
			input:       buildMixedGoogleAIKey(),
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		// xAI with mixed case
		{
			name:        "xAI key with mixed alphanumeric",
			input:       buildMixedXAIKey(),
			patternName: "xai_key",
			wantMatch:   true,
		},
		// Groq with mixed case
		{
			name:        "Groq key with mixed alphanumeric",
			input:       buildMixedGroqKey(),
			patternName: "groq_key",
			wantMatch:   true,
		},
		// HuggingFace with mixed case
		{
			name:        "HuggingFace token with mixed alphanumeric",
			input:       buildMixedHuggingFaceToken(),
			patternName: "huggingface_token",
			wantMatch:   true,
		},
		// Replicate with mixed case
		{
			name:        "Replicate key with mixed alphanumeric",
			input:       buildMixedReplicateKey(),
			patternName: "replicate_key",
			wantMatch:   true,
		},
		// Perplexity with mixed case
		{
			name:        "Perplexity key with mixed alphanumeric",
			input:       buildMixedPerplexityKey(),
			patternName: "perplexity_key",
			wantMatch:   true,
		},
		// Fireworks with mixed case
		{
			name:        "Fireworks key with mixed alphanumeric",
			input:       buildMixedFireworksKey(),
			patternName: "fireworks_key",
			wantMatch:   true,
		},
		// Anyscale with mixed case
		{
			name:        "Anyscale key with mixed alphanumeric",
			input:       buildMixedAnyscaleKey(),
			patternName: "anyscale_key",
			wantMatch:   true,
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

// TestLLMKeysInLogOutput tests detection in log/error messages
func TestLLMKeysInLogOutput(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		{
			name:        "Google AI key in error log",
			input:       `ERROR: Invalid API key: ` + buildGoogleAIKey() + ` - please check your credentials`,
			patternName: "google_ai_key",
			wantMatch:   true,
		},
		{
			name:        "Groq key in debug log",
			input:       `[DEBUG] Using API key: ` + buildGroqKey(),
			patternName: "groq_key",
			wantMatch:   true,
		},
		{
			name:        "HuggingFace token in warning",
			input:       `Warning: Token ` + buildHuggingFaceToken() + ` is about to expire`,
			patternName: "huggingface_token",
			wantMatch:   true,
		},
		{
			name:        "xAI key in stack trace",
			input:       `at authenticate(key="` + buildXAIKey() + `")`,
			patternName: "xai_key",
			wantMatch:   true,
		},
		{
			name:        "Replicate key in HTTP response",
			input:       `{"error": "Invalid token", "token": "` + buildReplicateKey() + `"}`,
			patternName: "replicate_key",
			wantMatch:   true,
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

// TestOpenAIAnthropicImprovedPatterns tests the improved OpenAI and Anthropic patterns
func TestOpenAIAnthropicImprovedPatterns(t *testing.T) {
	// Helper to build OpenAI keys dynamically
	buildOpenAIKey := func(prefix string) string {
		return prefix + strings.Repeat("a", 40)
	}

	// Helper to build Anthropic keys dynamically
	buildAnthropicKey := func(variant string) string {
		return "sk-ant-" + variant + "-" + strings.Repeat("a", 30)
	}

	tests := []struct {
		name        string
		input       string
		patternName string
		wantMatch   bool
	}{
		// OpenAI variants
		{
			name:        "OpenAI legacy key",
			input:       buildOpenAIKey("sk-"),
			patternName: "openai_key",
			wantMatch:   true,
		},
		{
			name:        "OpenAI project key",
			input:       buildOpenAIKey("sk-proj-"),
			patternName: "openai_key",
			wantMatch:   true,
		},
		{
			name:        "OpenAI service account key",
			input:       buildOpenAIKey("sk-svcacct-"),
			patternName: "openai_key",
			wantMatch:   true,
		},
		{
			name:        "OpenAI admin key",
			input:       buildOpenAIKey("sk-admin-"),
			patternName: "openai_key",
			wantMatch:   true,
		},
		{
			name:        "OpenAI key in JSON",
			input:       `{"openai_api_key": "` + buildOpenAIKey("sk-proj-") + `"}`,
			patternName: "openai_key",
			wantMatch:   true,
		},
		{
			name:        "OpenAI key in env",
			input:       "OPENAI_API_KEY=" + buildOpenAIKey("sk-"),
			patternName: "openai_key",
			wantMatch:   true,
		},
		// Anthropic variants
		{
			name:        "Anthropic api03 key",
			input:       buildAnthropicKey("api03"),
			patternName: "anthropic_key",
			wantMatch:   true,
		},
		{
			name:        "Anthropic admin01 key",
			input:       buildAnthropicKey("admin01"),
			patternName: "anthropic_key",
			wantMatch:   true,
		},
		{
			name:        "Anthropic key in JSON",
			input:       `{"anthropic_api_key": "` + buildAnthropicKey("api03") + `"}`,
			patternName: "anthropic_key",
			wantMatch:   true,
		},
		{
			name:        "Anthropic key in env",
			input:       "ANTHROPIC_API_KEY=" + buildAnthropicKey("api03"),
			patternName: "anthropic_key",
			wantMatch:   true,
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

// TestAllLLMPatternsExist verifies all expected LLM patterns are registered
func TestAllLLMPatternsExist(t *testing.T) {
	expectedPatterns := []string{
		"openai_key",
		"anthropic_key",
		"google_ai_key",
		"xai_key",
		"groq_key",
		"huggingface_token",
		"huggingface_org_token",
		"replicate_key",
		"perplexity_key",
		"fireworks_key",
		"anyscale_key",
		"mistral_key",
		"cohere_key",
		"deepseek_key",
		"together_key",
	}

	patterns := GetTokenPatterns()

	for _, name := range expectedPatterns {
		t.Run(name, func(t *testing.T) {
			pattern := findPatternByName(patterns, name)
			assert.NotNil(t, pattern, "pattern %s should exist", name)
		})
	}
}
