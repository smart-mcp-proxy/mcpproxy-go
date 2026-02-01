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
