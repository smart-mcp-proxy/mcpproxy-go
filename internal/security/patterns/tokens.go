package patterns

// GetTokenPatterns returns all API token detection patterns
func GetTokenPatterns() []*Pattern {
	return []*Pattern{
		// GitHub tokens
		githubPATPattern(),
		githubOAuthPattern(),
		githubAppPattern(),
		githubRefreshPattern(),
		// GitLab tokens
		gitlabPATPattern(),
		// Stripe tokens
		stripeKeyPattern(),
		// Slack tokens
		slackTokenPattern(),
		// SendGrid
		sendgridKeyPattern(),
		// AI API keys
		openaiKeyPattern(),
		anthropicKeyPattern(),
		// Generic tokens
		jwtTokenPattern(),
		bearerTokenPattern(),
	}
}

// GitHub Personal Access Token (classic and fine-grained)
func githubPATPattern() *Pattern {
	// ghp_ = classic PAT, github_pat_ = fine-grained PAT
	// Fine-grained format: github_pat_<base62>_<base62> (variable lengths)
	return NewPattern("github_pat").
		WithRegex(`(?:ghp_[a-zA-Z0-9]{36}|github_pat_[a-zA-Z0-9]+_[a-zA-Z0-9]{30,})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("GitHub Personal Access Token").
		Build()
}

// GitHub OAuth Token
func githubOAuthPattern() *Pattern {
	return NewPattern("github_oauth").
		WithRegex(`gho_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub OAuth access token").
		Build()
}

// GitHub App Installation Token
func githubAppPattern() *Pattern {
	return NewPattern("github_app").
		WithRegex(`ghs_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub App installation access token").
		Build()
}

// GitHub App Refresh Token
func githubRefreshPattern() *Pattern {
	return NewPattern("github_refresh").
		WithRegex(`ghr_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub App refresh token").
		Build()
}

// GitLab Personal Access Token
func gitlabPATPattern() *Pattern {
	return NewPattern("gitlab_pat").
		WithRegex(`glpat-[a-zA-Z0-9_-]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("GitLab Personal Access Token").
		Build()
}

// Stripe API Key (secret, publishable, restricted)
func stripeKeyPattern() *Pattern {
	// sk_ = secret, pk_ = publishable, rk_ = restricted
	return NewPattern("stripe_key").
		WithRegex(`(?:sk|pk|rk)_(?:live|test)_[a-zA-Z0-9]{24,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Stripe API key").
		Build()
}

// Slack Token (bot, user, app) and webhook
func slackTokenPattern() *Pattern {
	// xoxb = bot, xoxp = user, xapp = app, hooks.slack.com = webhook
	return NewPattern("slack_token").
		WithRegex(`(?:xox[bpas]-[0-9A-Za-z-]+|xapp-[0-9]-[A-Z0-9]+-[0-9]+-[a-zA-Z0-9]+|https://hooks\.slack\.com/services/[A-Z0-9]+/[A-Z0-9]+/[a-zA-Z0-9]+)`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("Slack token or webhook URL").
		Build()
}

// SendGrid API Key
func sendgridKeyPattern() *Pattern {
	return NewPattern("sendgrid_key").
		WithRegex(`SG\.[a-zA-Z0-9_-]{20,}\.[a-zA-Z0-9_-]{40,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("SendGrid API key").
		Build()
}

// OpenAI API Key
func openaiKeyPattern() *Pattern {
	// sk-proj- = project key, sk- = older format
	return NewPattern("openai_key").
		WithRegex(`sk-(?:proj-)?[a-zA-Z0-9]{32,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("OpenAI API key").
		Build()
}

// Anthropic API Key
func anthropicKeyPattern() *Pattern {
	return NewPattern("anthropic_key").
		WithRegex(`sk-ant-api[a-zA-Z0-9-]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Anthropic API key").
		Build()
}

// JWT Token (JSON Web Token)
func jwtTokenPattern() *Pattern {
	// JWT has 3 base64url parts separated by dots
	// Header starts with eyJ (base64 of '{"')
	return NewPattern("jwt_token").
		WithRegex(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]+`).
		WithCategory(CategoryAuthToken).
		WithSeverity(SeverityHigh).
		WithDescription("JSON Web Token (JWT)").
		Build()
}

// Bearer Token (generic)
func bearerTokenPattern() *Pattern {
	return NewPattern("bearer_token").
		WithRegex(`(?i)(?:bearer\s+)([a-zA-Z0-9_-]{20,})`).
		WithCategory(CategoryAuthToken).
		WithSeverity(SeverityMedium).
		WithDescription("Bearer authentication token").
		Build()
}
